package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actioninspect"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	gatewayProtocolCurrent               = "2026-07-28"
	gatewayProtocolLegacy                = "2025-11-25"
	repositoryManagedAuthorityDiagnostic = "reconc mcp gateway: repository-managed policy authority enabled; repository policy may be modified and refreshed by repository authority"
)

var supportedGatewayProtocols = []string{gatewayProtocolCurrent, gatewayProtocolLegacy}

func gatewayProtocolSupported(version string) bool {
	return version == gatewayProtocolCurrent || version == gatewayProtocolLegacy
}

type Gateway struct {
	config Config

	ctx    context.Context
	cancel context.CancelFunc

	lease    *actionstate.IdentityKeyLease
	state    *actionstate.Store
	storage  actionstate.PrivateProjectStorage
	ledger   *actionledger.Store
	registry actionstate.LoadedApprovalRegistry

	boundContext actionstate.BoundContext
	server       actionstate.ObservedServer
	bindings     []actionstate.EnvironmentBinding
	snapshot     PolicySnapshot
	cache        *action.DecisionCache

	process       *ownedProcess
	downstream    Downstream
	upstreamMu    sync.Mutex
	upstream      *mcp.Server
	sessionMu     sync.RWMutex
	session       *mcp.ServerSession
	upstreamNames map[string]struct{}
	upstreamWire  *upstreamObserver

	toolsMu      sync.RWMutex
	tools        map[string]ToolContract
	generation   uint64
	refreshMu    sync.Mutex
	stateMu      sync.Mutex
	lifecycleMu  sync.Mutex
	diagnosticMu sync.Mutex
	closing      bool
	fatalErr     error

	pendingMu sync.Mutex
	pending   map[string]pendingApproval

	semaphore         chan struct{}
	refreshRequests   chan struct{}
	refreshWorkerDone chan struct{}
	fatalErrors       chan error
	closeOnce         sync.Once
	closeErr          error
}

type pendingApproval struct {
	phase                 action.Phase
	callID                string
	requestState          string
	approvalRequest       actionapproval.Request
	originalRPCID         json.RawMessage
	originalParams        json.RawMessage
	canonicalArguments    json.RawMessage
	contract              ToolContract
	generation            uint64
	snapshot              PolicySnapshot
	preRequest            action.Request
	evaluation            action.EvaluationInput
	decision              action.EvaluationResult
	downstreamDecision    action.EvaluationResult
	reservation           *actionstate.Reservation
	budget                action.BudgetSnapshot
	issuanceVersion       string
	ledger                *callLedger
	approvalReserved      bool
	postApprovalReserved  bool
	postApprovalCommitted bool
	resultIsError         bool
	actualResultBytes     uint64
	upstreamProtocol      string
	downstreamProtocol    string
	rawResult             json.RawMessage
	repositoryPaths       []RepositoryPathBinding
}

func (p *pendingApproval) release() {
	if p == nil {
		return
	}
	clear(p.originalRPCID)
	clear(p.originalParams)
	clear(p.canonicalArguments)
	clear(p.rawResult)
	*p = pendingApproval{}
}

func Run(ctx context.Context, config Config) (resultErr error) {
	gateway, err := startGateway(ctx, config)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, gateway.Close()) }()
	return gateway.serve()
}

func startGateway(parent context.Context, config Config) (*Gateway, error) {
	if parent == nil {
		return nil, fmt.Errorf("gateway context is required")
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if config.CallTimeout == 0 {
		config.CallTimeout = DefaultCallTimeout
	}
	config.Arguments = append([]string(nil), config.Arguments...)
	config.CredentialLabels = append([]string(nil), config.CredentialLabels...)
	config.InheritedEnvNames = append([]string(nil), config.InheritedEnvNames...)
	startupCtx, startupCancel := context.WithTimeout(parent, StartupTimeout)
	defer startupCancel()
	snapshot, err := config.PolicyLoader.Load(startupCtx, config.Repository)
	if err != nil {
		return nil, fmt.Errorf("load current action policy: %w", err)
	}
	if err := validatePolicySnapshot(snapshot); err != nil {
		return nil, err
	}
	if err := config.PolicyAuthority.VerifyLockDigest(snapshot.LockDigest); err != nil {
		return nil, fmt.Errorf("verify policy authority: %w", err)
	}
	home, err := actionstate.ResolveHome(config.ReconcHome)
	if err != nil {
		return nil, err
	}
	lease, err := actionstate.AcquireIdentityKey(startupCtx, home)
	if err != nil {
		return nil, fmt.Errorf("acquire action identity key: %w", err)
	}
	gatewayCtx, cancel := context.WithCancel(parent)
	gateway := &Gateway{
		config: config, ctx: gatewayCtx, cancel: cancel, lease: lease,
		snapshot: snapshot, cache: action.NewDecisionCache(),
		pending: make(map[string]pendingApproval), semaphore: make(chan struct{}, MaxConcurrentCalls),
		refreshRequests: make(chan struct{}, 1), fatalErrors: make(chan error, 1),
	}
	fail := func(cause error) (*Gateway, error) {
		return nil, errors.Join(cause, gateway.Close())
	}
	resolvedRepository, repositoryIdentity, err := actionstate.ObserveRepository(lease.Key, snapshot.Repository)
	if err != nil {
		return fail(err)
	}
	if resolvedRepository != snapshot.Repository {
		return fail(fmt.Errorf("policy repository identity is not canonical"))
	}
	contextInput := actionstate.OperatorContext{
		Principal: config.Principal, Role: config.Role, Environment: config.Environment,
		Credentials: credentialBindings(config.CredentialLabels), ServerLabel: config.ServerLabel,
		RunID: config.RunID, SessionID: config.SessionID,
	}
	gateway.boundContext, err = contextInput.Bind(lease.Key)
	if err != nil {
		return fail(fmt.Errorf("bind operator context: %w", err))
	}
	workingDirectory := config.ServerWorkingDir
	if workingDirectory == "" {
		workingDirectory = snapshot.Repository
	}
	bindings, environment, err := selectedEnvironment(config.InheritedEnvNames)
	if err != nil {
		return fail(err)
	}
	initialServer, err := actionstate.ObserveServer(
		lease.Key, config.Command, config.Arguments, workingDirectory, bindings,
	)
	if err != nil {
		return fail(fmt.Errorf("observe downstream server: %w", err))
	}
	gateway.server, err = actionstate.ObserveServer(
		lease.Key, initialServer.ExecutablePath, config.Arguments, workingDirectory, bindings,
	)
	if err != nil {
		return fail(fmt.Errorf("bind resolved downstream server: %w", err))
	}
	if err := gateway.server.Validate(lease.Key); err != nil {
		return fail(err)
	}
	gateway.bindings = append([]actionstate.EnvironmentBinding(nil), bindings...)
	gateway.state, err = actionstate.OpenStore(actionstate.StoreOptions{
		Home: home, Repository: snapshot.Repository, KeyLease: lease,
	})
	if err != nil {
		return fail(fmt.Errorf("open action state: %w", err))
	}
	gateway.storage, err = gateway.state.PrivateProjectStorage()
	if err != nil {
		return fail(err)
	}
	if repositoryIdentity != gateway.storage.RepositoryIdentity() {
		return fail(fmt.Errorf("action state repository identity drifted"))
	}
	gateway.ledger, err = actionledger.OpenStore(gateway.storage)
	if err != nil {
		return fail(fmt.Errorf("open action ledger: %w", err))
	}
	if config.ApprovalAuthorities != "" {
		gateway.registry, err = actionstate.LoadApprovalAuthorityRegistry(
			config.ApprovalAuthorities, snapshot.Repository,
		)
		if err != nil {
			return fail(fmt.Errorf("load approval authorities: %w", err))
		}
	}
	if err := validateApprovalConfiguration(
		snapshot.Plan,
		gateway.registry,
		config.ApprovalPolicyID,
	); err != nil {
		return fail(err)
	}
	if _, err := gateway.state.ReconcileExpiredApprovals(startupCtx); err != nil {
		return fail(fmt.Errorf("reconcile expired approvals: %w", err))
	}
	_, err = actioninspect.NewEngine(snapshot.Plan, lease.Key)
	if err != nil {
		return fail(fmt.Errorf("prepare action inspection: %w", err))
	}
	gateway.process, err = startOwnedProcess(gateway.server, config.Arguments, environment)
	if err != nil {
		return fail(err)
	}
	resampled, err := actionstate.ObserveServer(
		lease.Key, gateway.server.ExecutablePath, config.Arguments,
		gateway.server.WorkingDirectory, bindings,
	)
	if err != nil || !reflect.DeepEqual(resampled, gateway.server) {
		return fail(fmt.Errorf("downstream server identity changed during launch"))
	}
	gateway.downstream, err = defaultDownstreamFactory(
		startupCtx, gateway.process, config.Version, gateway.toolListChanged,
	)
	if err != nil {
		return fail(err)
	}
	if !gatewayProtocolSupported(gateway.downstream.ProtocolVersion()) {
		return fail(fmt.Errorf("downstream MCP protocol is unsupported"))
	}
	if err := gateway.refreshTools(startupCtx); err != nil {
		return fail(err)
	}
	if config.PolicyAuthority.Mode == action.AuthorityRepositoryManaged {
		gateway.diagnostic(repositoryManagedAuthorityDiagnostic)
	}
	gateway.refreshWorkerDone = make(chan struct{})
	go gateway.runToolRefreshes()
	return gateway, nil
}

func validateApprovalConfiguration(
	plan *action.CompiledPlan,
	registry actionstate.LoadedApprovalRegistry,
	policyID string,
) error {
	if plan == nil {
		return fmt.Errorf("approval policy plan is unavailable")
	}
	if policyID != "" && !registry.HasPolicy(policyID) {
		return fmt.Errorf("configured approval authority policy is absent from the operator registry")
	}
	if planRequiresApproval(plan) && policyID == "" {
		return fmt.Errorf("action policy can require approval but no operator approval authority is configured")
	}
	return nil
}

func planRequiresApproval(compiled *action.CompiledPlan) bool {
	plan := compiled.Plan()
	if plan.Defaults.DeclaredTool == action.DecisionRequireApproval {
		return true
	}
	for _, rule := range plan.Rules {
		if rule.Decision == action.DecisionRequireApproval ||
			rule.OnIndeterminate == action.DecisionRequireApproval {
			return true
		}
	}
	for _, detector := range plan.Detectors {
		if detector.PreCallDecision == action.DecisionRequireApproval {
			return true
		}
	}
	return false
}

func validateConfig(config Config) error {
	if config.Repository == "" || config.ServerLabel == "" || config.Command == "" ||
		config.Version == "" || config.Input == nil || config.Output == nil ||
		config.Diagnostics == nil || config.PolicyLoader == nil {
		return fmt.Errorf("gateway repository, server, command, version, streams, and policy loader are required")
	}
	if err := config.PolicyAuthority.Validate(); err != nil {
		return err
	}
	if config.Principal == "" {
		return fmt.Errorf("gateway principal is required")
	}
	if config.CallTimeout == 0 {
		config.CallTimeout = DefaultCallTimeout
	}
	if config.CallTimeout < time.Millisecond || config.CallTimeout > MaximumCallTimeout {
		return fmt.Errorf("gateway timeout must be between 1ms and %s", MaximumCallTimeout)
	}
	if (config.ApprovalAuthorities == "") != (config.ApprovalPolicyID == "") {
		return fmt.Errorf("approval authorities and approval policy must be configured together")
	}
	return nil
}

func validatePolicySnapshot(snapshot PolicySnapshot) error {
	if snapshot.Repository == "" || snapshot.Evaluator == nil || snapshot.Plan == nil ||
		len(snapshot.SourceDigest) != 64 || len(snapshot.LockDigest) != 64 {
		return fmt.Errorf("action policy snapshot is incomplete")
	}
	resolved, err := pathidentity.ResolveExisting(snapshot.Repository)
	if err != nil || resolved != snapshot.Repository {
		return fmt.Errorf("action policy repository is not a canonical existing path")
	}
	evaluator, err := action.NewEvaluator(snapshot.Plan)
	if err != nil || evaluator.PlanIdentity() != snapshot.Evaluator.PlanIdentity() {
		return fmt.Errorf("action policy evaluator and plan identities disagree")
	}
	for _, digest := range []string{snapshot.SourceDigest, snapshot.LockDigest} {
		for _, character := range digest {
			if character < '0' || character > '9' && character < 'a' || character > 'f' {
				return fmt.Errorf("action policy digest is not lowercase SHA-256")
			}
		}
	}
	return nil
}

func selectedEnvironment(names []string) ([]actionstate.EnvironmentBinding, []string, error) {
	bindings := make([]actionstate.EnvironmentBinding, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		key := name
		if runtime.GOOS == "windows" {
			key = strings.ToLower(name)
		}
		if name == "" {
			return nil, nil, fmt.Errorf("inherited environment name is empty")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, nil, fmt.Errorf("inherited environment name %q is duplicated", name)
		}
		seen[key] = struct{}{}
		value, exists := os.LookupEnv(name)
		if !exists {
			return nil, nil, fmt.Errorf("selected inherited environment name %q is unavailable", name)
		}
		bindings = append(bindings, actionstate.EnvironmentBinding{Name: name, Value: value})
	}
	sort.Slice(bindings, func(i, j int) bool {
		left, right := bindings[i].Name, bindings[j].Name
		if runtime.GOOS == "windows" {
			left, right = strings.ToLower(left), strings.ToLower(right)
		}
		return left < right
	})
	environment := make([]string, len(bindings))
	for index, binding := range bindings {
		environment[index] = binding.Name + "=" + binding.Value
	}
	return bindings, environment, nil
}

func credentialBindings(labels []string) []actionstate.CredentialBinding {
	bindings := make([]actionstate.CredentialBinding, len(labels))
	for index, label := range labels {
		bindings[index] = actionstate.CredentialBinding{Label: label}
	}
	return bindings
}

func (g *Gateway) discoverTools(ctx context.Context) ([]ToolContract, error) {
	discoveryCtx, cancel := context.WithTimeout(ctx, ToolDiscoveryTimeout)
	defer cancel()
	validator, err := newCatalogValidator()
	if err != nil {
		return nil, wrapBoundaryError("validate downstream tool catalog", err)
	}
	seenCursors := make(map[string]struct{})
	cursor := ""
	for pageNumber := 0; pageNumber < MaxToolPages; pageNumber++ {
		pageCtx, pageCancel := context.WithTimeout(discoveryCtx, ToolPageTimeout)
		page, err := g.downstream.ListTools(pageCtx, cursor)
		pageCancel()
		if err != nil {
			return nil, fmt.Errorf("discover downstream tools: %w", err)
		}
		if err := validator.addPage(discoveryCtx, page); err != nil {
			return nil, wrapBoundaryError("validate downstream tool catalog", err)
		}
		if page.NextCursor == "" {
			contracts, err := validator.finish()
			if err != nil {
				return nil, wrapBoundaryError("validate downstream tool catalog", err)
			}
			return contracts, nil
		}
		if _, duplicate := seenCursors[page.NextCursor]; duplicate {
			return nil, fmt.Errorf("downstream tool pagination cursor repeated")
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
	return nil, fmt.Errorf("downstream tool discovery exceeds %d pages", MaxToolPages)
}

func (g *Gateway) refreshTools(ctx context.Context) error {
	g.refreshMu.Lock()
	defer g.refreshMu.Unlock()
	contracts, err := g.discoverTools(ctx)
	if err != nil {
		return err
	}
	tools := toolMap(contracts)
	g.toolsMu.Lock()
	g.tools = tools
	g.generation++
	g.cache = action.NewDecisionCache()
	g.toolsMu.Unlock()
	if err := g.publishUpstreamTools(contracts); err != nil {
		return wrapBoundaryError("publish validated downstream tool catalog", err)
	}
	return nil
}

func (g *Gateway) toolListChanged(context.Context) {
	select {
	case g.refreshRequests <- struct{}{}:
	default:
	}
}

func (g *Gateway) runToolRefreshes() {
	defer close(g.refreshWorkerDone)
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-g.refreshRequests:
			refreshCtx, cancel := context.WithTimeout(g.ctx, ToolDiscoveryTimeout)
			err := g.refreshTools(refreshCtx)
			cancel()
			if err != nil {
				fatalErr := fmt.Errorf("refresh downstream tool catalog: %w", err)
				g.recordFatalError(fatalErr)
				select {
				case g.fatalErrors <- fatalErr:
				default:
				}
				return
			}
		}
	}
}

func (g *Gateway) tool(name string) (ToolContract, uint64, bool) {
	g.toolsMu.RLock()
	defer g.toolsMu.RUnlock()
	contract, ok := g.tools[name]
	if ok {
		contract.Canonical = append(json.RawMessage(nil), contract.Canonical...)
	}
	return contract, g.generation, ok
}

func (g *Gateway) generationCurrent(generation uint64, contract ToolContract) bool {
	g.toolsMu.RLock()
	defer g.toolsMu.RUnlock()
	current, ok := g.tools[contract.Name]
	return ok && g.generation == generation && current.ContractDigest == contract.ContractDigest
}

func (g *Gateway) freshSnapshot(ctx context.Context) (PolicySnapshot, error) {
	resampleCtx, cancel := context.WithTimeout(ctx, ResampleTimeout)
	defer cancel()
	snapshot, err := g.config.PolicyLoader.Load(resampleCtx, g.snapshot.Repository)
	if err != nil {
		return PolicySnapshot{}, err
	}
	if err := validatePolicySnapshot(snapshot); err != nil {
		return PolicySnapshot{}, err
	}
	if snapshot.Repository != g.snapshot.Repository {
		return PolicySnapshot{}, fmt.Errorf("policy repository identity drifted")
	}
	if err := g.config.PolicyAuthority.VerifyLockDigest(snapshot.LockDigest); err != nil {
		return PolicySnapshot{}, err
	}
	if g.config.PolicyAuthority.Mode == action.AuthorityOperatorPinned &&
		(snapshot.SourceDigest != g.snapshot.SourceDigest ||
			snapshot.Evaluator.PlanIdentity() != g.snapshot.Evaluator.PlanIdentity()) {
		return PolicySnapshot{}, fmt.Errorf("operator-pinned policy source changed")
	}
	return snapshot, nil
}

func terminalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx != nil && ctx.Err() == nil {
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) >= CancellationGrace {
			return ctx, func() {}
		}
	}
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, CancellationGrace)
}

func (g *Gateway) Close() error {
	if g == nil {
		return nil
	}
	g.closeOnce.Do(func() {
		// Mark the child before cancellation or transport closure can make it exit.
		if g.process != nil {
			g.process.expectShutdown()
		}
		g.lifecycleMu.Lock()
		g.closing = true
		g.lifecycleMu.Unlock()
		if g.cancel != nil {
			g.cancel()
		}
		if session := g.upstreamSession(); session != nil {
			g.closeErr = errors.Join(g.closeErr, closeLifecycleError(session.Close()))
		}
		if g.downstream != nil {
			g.closeErr = errors.Join(g.closeErr, closeLifecycleError(g.downstream.Close()))
		}
		if g.process != nil {
			g.closeErr = errors.Join(g.closeErr, g.process.Close())
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		if err := g.waitForCalls(shutdownCtx); err != nil {
			g.closeErr = errors.Join(g.closeErr, err)
		} else if err := g.shutdownPending(shutdownCtx); err != nil {
			g.closeErr = errors.Join(g.closeErr, err)
		}
		if g.refreshWorkerDone != nil {
			select {
			case <-g.refreshWorkerDone:
			case <-shutdownCtx.Done():
				g.closeErr = errors.Join(g.closeErr, fmt.Errorf("tool refresh worker did not terminate"))
			}
		}
		g.closeErr = errors.Join(g.closeErr, g.fatalError())
		if g.lease != nil {
			g.closeErr = errors.Join(g.closeErr, g.lease.Close())
		}
	})
	return g.closeErr
}

func (g *Gateway) beginCall() bool {
	g.lifecycleMu.Lock()
	defer g.lifecycleMu.Unlock()
	return !g.closing
}

func (g *Gateway) recordFatalError(err error) {
	g.lifecycleMu.Lock()
	defer g.lifecycleMu.Unlock()
	if g.fatalErr == nil {
		g.fatalErr = err
	}
	g.closing = true
}

func (g *Gateway) fatalError() error {
	g.lifecycleMu.Lock()
	defer g.lifecycleMu.Unlock()
	return g.fatalErr
}

func (g *Gateway) waitForCalls(ctx context.Context) error {
	for index := 0; index < MaxConcurrentCalls; index++ {
		select {
		case g.semaphore <- struct{}{}:
		case <-ctx.Done():
			return fmt.Errorf("gateway calls did not terminate: %w", ctx.Err())
		}
	}
	return nil
}

func (g *Gateway) shutdownPending(ctx context.Context) error {
	g.pendingMu.Lock()
	pending := make([]pendingApproval, 0, len(g.pending))
	for _, approval := range g.pending {
		pending = append(pending, approval)
	}
	g.pending = make(map[string]pendingApproval)
	g.pendingMu.Unlock()
	defer func() {
		for index := range pending {
			pending[index].release()
		}
	}()
	sort.Slice(pending, func(i, j int) bool { return pending[i].callID < pending[j].callID })
	g.stateMu.Lock()
	defer g.stateMu.Unlock()
	for _, approval := range pending {
		result, err := g.state.FinalizeApproval(ctx, actionstate.ApprovalFinalizeRequest{
			RequestState: approval.requestState, ExpectedStateVersion: approval.issuanceVersion,
			Status: actionapproval.StatusCancelled,
		})
		if err != nil {
			return fmt.Errorf("finalize pending approval during shutdown: %w", err)
		}
		call := callFromPending(approval)
		if approval.phase == action.PhasePostResult {
			if _, err := g.finalizePostApproval(ctx, call, result, action.ReasonShutdown); err != nil {
				return err
			}
			continue
		}
		g.recordTerminalizedApproval(ctx, call, result, action.ReasonShutdown, false)
	}
	return nil
}

func closeLifecycleError(err error) error {
	if isNormalLifecycleError(err) || errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

func (g *Gateway) diagnostic(message string) {
	if g == nil || message == "" {
		return
	}
	message = boundedDiagnostic(message)
	if message == "" {
		return
	}
	g.diagnosticMu.Lock()
	defer g.diagnosticMu.Unlock()
	if g.config.Diagnostics == nil {
		return
	}
	_, _ = io.WriteString(g.config.Diagnostics, message+"\n")
}

func boundedDiagnostic(message string) string {
	if len(message) > MaxDiagnosticBytes {
		message = message[:MaxDiagnosticBytes]
	}
	message = strings.ToValidUTF8(message, "")
	message = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, message)
	return strings.Join(strings.Fields(message), " ")
}

func approvalStatus(err error) actionapproval.Status {
	var approvalErr *actionapproval.ApprovalError
	if errors.As(err, &approvalErr) {
		switch approvalErr.Code {
		case action.ReasonApprovalRejected:
			return actionapproval.StatusRejected
		case action.ReasonApprovalExpired:
			return actionapproval.StatusExpired
		case action.ReasonApprovalReplayed:
			return actionapproval.StatusReplayed
		case action.ReasonCancelled:
			return actionapproval.StatusCancelled
		case action.ReasonApprovalInvalid, action.ReasonProtocolError:
			return actionapproval.StatusMalformed
		}
	}
	return actionapproval.StatusUnavailable
}
