package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
)

type gatewayCall struct {
	callID                string
	wire                  upstreamWireCall
	contract              ToolContract
	generation            uint64
	snapshot              PolicySnapshot
	request               action.Request
	preRequest            action.Request
	tool                  action.Tool
	evaluation            action.EvaluationInput
	decision              action.EvaluationResult
	downstreamDecision    action.EvaluationResult
	ledger                *callLedger
	budget                action.BudgetSnapshot
	reservation           *actionstate.Reservation
	stateVersion          string
	canonicalArguments    json.RawMessage
	approvalReserved      bool
	approvalCommitted     bool
	postApprovalReserved  bool
	postApprovalCommitted bool
	resultIsError         bool
	actualResultBytes     uint64
	upstreamProtocol      string
	repositoryPaths       []RepositoryPathBinding
	progress              *callProgress
	legacyApprovalState   string
}

func (g *Gateway) handleTool(
	ctx context.Context,
	sdkRequest *mcp.CallToolRequest,
	registered ToolContract,
) (*mcp.CallToolResult, error) {
	callID, err := actionstate.NewRandomCallID()
	if err != nil {
		return nil, fmt.Errorf("generate gateway call identity: %w", err)
	}
	if sdkRequest == nil || sdkRequest.Params == nil {
		return blockedGatewayResult(callID, action.ReasonInvalidRequest)
	}
	if !g.beginCall() {
		return blockedGatewayResult(callID, action.ReasonShutdown)
	}
	wire, err := g.upstreamWire.take(sdkRequest.Params)
	if err != nil {
		return blockedGatewayResult(callID, action.ReasonProtocolError)
	}
	if !gatewayProtocolSupported(sdkRequest.ProtocolVersion()) {
		return blockedGatewayResult(callID, action.ReasonProtocolError)
	}
	select {
	case g.semaphore <- struct{}{}:
		defer func() { <-g.semaphore }()
	case <-ctx.Done():
		return blockedGatewayResult(callID, gatewayReason(ctx.Err(), action.ReasonCancelled))
	case <-g.ctx.Done():
		return blockedGatewayResult(callID, action.ReasonShutdown)
	}
	contract, generation, exists := g.tool(sdkRequest.Params.Name)
	if !exists || contract.ContractDigest != registered.ContractDigest {
		return blockedGatewayResult(callID, action.ReasonToolContractStale)
	}
	if sdkRequest.Params.RequestState != "" || len(sdkRequest.Params.InputResponses) != 0 {
		return g.resumeApproval(ctx, sdkRequest, wire, contract, generation, callID)
	}
	return g.startCall(ctx, sdkRequest, wire, contract, generation, callID)
}

func (g *Gateway) startCall(
	ctx context.Context,
	sdkRequest *mcp.CallToolRequest,
	wire upstreamWireCall,
	contract ToolContract,
	generation uint64,
	callID string,
) (*mcp.CallToolResult, error) {
	arguments, value, err := canonicalArguments(sdkRequest.Params.Arguments)
	if err != nil {
		return blockedGatewayResult(callID, gatewayReason(err, action.ReasonInvalidRequest))
	}
	if err := contract.InputSchema.Validate(value); err != nil {
		return blockedGatewayResult(callID, action.ReasonSchemaInvalid)
	}
	progress, err := newCallProgress(wire.params)
	if err != nil {
		return blockedGatewayResult(callID, action.ReasonInvalidRequest)
	}
	callCtx, cancel := g.callContext(ctx)
	defer cancel()
	call, response := g.prepareCall(
		callCtx, wire, contract, generation, callID, arguments, sdkRequest.ProtocolVersion(),
	)
	if response != nil || call == nil {
		return response, nil
	}
	call.progress = progress
	if call.legacyApprovalState != "" {
		return g.elicitLegacyApproval(callCtx, call.legacyApprovalState, call.callID, progress)
	}
	return g.executeCall(callCtx, call)
}

func canonicalArguments(raw json.RawMessage) (json.RawMessage, action.Value, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	value, err := action.ParseObjectJSON(raw)
	if err != nil {
		return nil, action.Value{}, err
	}
	canonical, err := value.MarshalJSON()
	if err != nil {
		return nil, action.Value{}, err
	}
	return canonical, value, nil
}

func (g *Gateway) prepareCall(
	ctx context.Context,
	wire upstreamWireCall,
	contract ToolContract,
	generation uint64,
	callID string,
	arguments json.RawMessage,
	upstreamProtocol string,
) (*gatewayCall, *mcp.CallToolResult) {
	snapshot, err := g.freshSnapshot(ctx)
	if err != nil {
		return nil, blockedGatewayResultValue(callID, gatewayReason(err, action.ReasonPolicyStale))
	}
	inspector, err := g.inspectionEngine(snapshot.Plan)
	if err != nil {
		return nil, blockedGatewayResultValue(callID, action.ReasonInspectionIncomplete)
	}
	stateVersion, err := g.state.CurrentStateVersion(ctx)
	if err != nil {
		return nil, blockedGatewayResultValue(
			callID,
			gatewayReason(err, action.ReasonStateUnavailable),
		)
	}
	request, err := g.normalizedRequest(
		snapshot, contract, callID, stateVersion, action.PhasePreCall, arguments,
	)
	if err != nil {
		return nil, blockedGatewayResultValue(callID, gatewayReason(err, action.ReasonInvalidRequest))
	}
	tool, _, err := snapshot.Plan.BudgetContract(request)
	if err != nil {
		return nil, blockedGatewayResultValue(callID, gatewayReason(err, action.ReasonPolicyMissing))
	}
	evidence, err := g.evidence(ctx, snapshot, request, tool)
	if err != nil {
		return nil, blockedGatewayResultValue(callID, action.ReasonInspectionIncomplete)
	}
	inspection, err := inspector.Inspect(ctx, request, nil, nil)
	if err != nil {
		return nil, blockedGatewayResultValue(
			callID,
			gatewayReason(err, action.ReasonInspectionIncomplete),
		)
	}
	var reserve actionstate.ReserveResult
	for conflictRetries := 0; ; conflictRetries++ {
		reserve, err = g.state.Reserve(ctx, actionstate.ReserveRequest{
			Plan: snapshot.Plan, Request: request, Context: g.boundContext,
			Authority: g.config.PolicyAuthority, Server: g.server,
		})
		if err == nil || !errors.Is(err, actionstate.ErrStateVersionChanged) ||
			conflictRetries >= MaxReservationConflictRetries {
			break
		}
		stateVersion, err = g.state.CurrentStateVersion(ctx)
		if err != nil {
			return nil, blockedGatewayResultValue(
				callID,
				gatewayReason(err, action.ReasonStateUnavailable),
			)
		}
		request.StateVersion = stateVersion
	}
	if err != nil {
		return nil, blockedGatewayResultValue(callID, gatewayReason(err, action.ReasonStateUnavailable))
	}
	ledger, ledgerErr := newCallLedger(g, snapshot, request, tool.ID)
	if ledgerErr != nil {
		if reserve.Reservation != nil {
			g.releaseReservation(ctx, reserve.Reservation, reserve.Snapshot.StateVersion)
		}
		return nil, blockedGatewayResultValue(callID, action.ReasonLedgerUnavailable)
	}
	if err := ledger.requestAccepted(ctx); err != nil {
		if reserve.Reservation != nil {
			g.releaseReservation(ctx, reserve.Reservation, reserve.Snapshot.StateVersion)
		}
		return nil, blockedGatewayResultValue(callID, action.ReasonLedgerUnavailable)
	}
	request.StateVersion = reserve.Snapshot.StateVersion
	budget := reserve.Snapshot
	input := g.evaluationInput(
		snapshot,
		request,
		budget,
		action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
		evidence,
	)
	input.Inspection = inspection
	input.ResampledIdentities = snapshot.Evaluator.IdentitySnapshot(input)
	decision, cached := g.evaluate(ctx, snapshot.Evaluator, input)
	if decision.Failure != nil {
		g.diagnostic("pre-call evaluation blocked: " + string(decision.Failure.Code))
	}
	ledger.setResult(request)
	if err := ledger.preDecision(ctx, decision, cached); err != nil {
		g.releaseReservation(ctx, reserve.Reservation, reserve.Snapshot.StateVersion)
		return nil, blockedGatewayResultValue(callID, action.ReasonLedgerUnavailable)
	}
	call := &gatewayCall{
		callID: callID, wire: wire, contract: contract, generation: generation,
		snapshot: snapshot, request: request, preRequest: request, tool: tool, evaluation: input,
		decision: decision, ledger: ledger, budget: budget,
		reservation: reserve.Reservation, stateVersion: reserve.Snapshot.StateVersion,
		canonicalArguments: bytes.Clone(arguments), upstreamProtocol: upstreamProtocol,
		repositoryPaths: cloneRepositoryPathBindings(evidence.RepositoryPaths),
	}
	if decision.Decision == action.DecisionRequireApproval {
		return g.requestApproval(ctx, call)
	}
	if reserve.Reservation != nil {
		if err := ledger.budget(
			ctx, decision, actionledger.BudgetReserved, budget,
			reserve.Snapshot.StateVersion, 0, false, false,
		); err != nil {
			g.releaseReservation(ctx, reserve.Reservation, reserve.Snapshot.StateVersion)
			return nil, blockedGatewayResultValue(callID, action.ReasonLedgerUnavailable)
		}
	}
	if decision.Decision == action.DecisionBlock || decision.Failure != nil {
		g.denyCall(ctx, call, false)
		return nil, blockedGatewayResultValue(callID, decision.Reason)
	}
	if decision.Decision != action.DecisionAllow && decision.Decision != action.DecisionWarn {
		g.releaseCall(ctx, call, action.ReasonInternalInvariant, false)
		return nil, blockedGatewayResultValue(callID, action.ReasonInternalInvariant)
	}
	if err := g.commitDispatch(ctx, call); err != nil {
		return nil, blockedGatewayResultValue(callID, gatewayReason(err, action.ReasonStateUnavailable))
	}
	return call, nil
}

func (g *Gateway) requestApproval(
	ctx context.Context,
	call *gatewayCall,
) (*gatewayCall, *mcp.CallToolResult) {
	if g.config.ApprovalPolicyID == "" {
		g.denyCall(ctx, call, false)
		return nil, blockedGatewayResultValue(call.callID, action.ReasonAuthorityUnavailable)
	}
	issued, err := g.state.IssueApproval(ctx, actionstate.ApprovalIssueRequest{
		Binding: actionstate.ApprovalBinding{
			Plan: call.snapshot.Plan, Evaluation: call.evaluation,
			Context: g.boundContext, Authority: g.config.PolicyAuthority, Server: g.server,
		},
		AuthorityPolicyID: g.config.ApprovalPolicyID,
		TTL:               actionapproval.MaximumApprovalTTL,
	})
	if err != nil {
		g.denyCall(ctx, call, false)
		return nil, blockedGatewayResultValue(call.callID, gatewayReason(err, action.ReasonAuthorityUnavailable))
	}
	call.stateVersion = issued.StateVersion
	call.approvalReserved = approvalBudgetPresent(call.budget)
	if call.reservation != nil {
		if err := call.ledger.budget(
			ctx, call.decision, actionledger.BudgetReserved, call.budget,
			issued.StateVersion, 0, call.approvalReserved, false,
		); err != nil {
			g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusUnavailable)
			return nil, blockedGatewayResultValue(call.callID, action.ReasonLedgerUnavailable)
		}
	}
	if err := call.ledger.approval(ctx, call.decision, issued.Evidence); err != nil {
		g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusUnavailable)
		return nil, blockedGatewayResultValue(call.callID, action.ReasonLedgerUnavailable)
	}
	baseParams, err := actionapproval.CanonicalMCPApprovalBaseParams(call.wire.params)
	if err != nil {
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusMalformed)
		g.recordTerminalizedApproval(
			ctx, call, finalized, action.ReasonProtocolError, false,
		)
		return nil, blockedGatewayResultValue(call.callID, action.ReasonProtocolError)
	}
	pending := pendingApproval{
		phase: action.PhasePreCall, callID: call.callID,
		approvalRequest: issued.Request,
		requestState:    issued.RequestState, originalRPCID: bytes.Clone(call.wire.id),
		originalParams: baseParams, canonicalArguments: bytes.Clone(call.canonicalArguments),
		contract: call.contract, generation: call.generation, snapshot: call.snapshot,
		preRequest: call.preRequest,
		evaluation: call.evaluation, decision: call.decision,
		reservation: call.reservation, budget: call.budget,
		issuanceVersion: issued.StateVersion, ledger: call.ledger,
		approvalReserved: call.approvalReserved, upstreamProtocol: call.upstreamProtocol,
		repositoryPaths: cloneRepositoryPathBindings(call.repositoryPaths),
	}
	if err := g.storePending(issued.RequestState, pending); err != nil {
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusUnavailable)
		g.recordTerminalizedApproval(
			ctx, call, finalized, action.ReasonStateUnavailable, false,
		)
		return nil, blockedGatewayResultValue(call.callID, action.ReasonStateUnavailable)
	}
	protocol := call.upstreamProtocol
	if protocol == gatewayProtocolLegacy {
		call.legacyApprovalState = issued.RequestState
		return call, nil
	}
	if protocol != actionapproval.MCPProtocolVersion {
		g.removePending(issued.RequestState)
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusUnavailable)
		g.recordTerminalizedApproval(
			ctx, call, finalized, action.ReasonAuthorityUnavailable, false,
		)
		return nil, blockedGatewayResultValue(call.callID, action.ReasonApprovalRequired)
	}
	inputRequired, err := actionapproval.BuildMCPInputRequired(issued.Request, issued.RequestState)
	if err != nil {
		g.removePending(issued.RequestState)
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusMalformed)
		g.recordTerminalizedApproval(
			ctx, call, finalized, action.ReasonApprovalInvalid, false,
		)
		return nil, blockedGatewayResultValue(call.callID, action.ReasonApprovalInvalid)
	}
	response, err := sdkApprovalResult(inputRequired)
	if err != nil {
		g.removePending(issued.RequestState)
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusUnavailable)
		g.recordTerminalizedApproval(
			ctx, call, finalized, action.ReasonProtocolError, false,
		)
		return nil, blockedGatewayResultValue(call.callID, action.ReasonProtocolError)
	}
	return nil, response
}

func approvalBudgetPresent(snapshot action.BudgetSnapshot) bool {
	for _, candidate := range snapshot.Candidates {
		if candidate.Limits.ApprovalCount != 0 {
			return true
		}
	}
	return false
}

func (g *Gateway) commitDispatch(ctx context.Context, call *gatewayCall) error {
	if err := g.resampleCallBoundary(
		ctx, call.snapshot, call.contract, call.generation, call.repositoryPaths,
	); err != nil {
		g.releaseCall(ctx, call, action.ReasonPolicyStale, call.approvalCommitted)
		return err
	}
	dispatchDecision := approvedDispatchDecision(call.decision, call.approvalCommitted)
	reservation := "absent"
	if call.reservation != nil {
		version, err := g.state.MarkDispatched(ctx, call.reservation.Identity, call.stateVersion)
		if err != nil {
			g.releaseCall(ctx, call, gatewayReason(err, action.ReasonStateUnavailable), call.approvalCommitted)
			return err
		}
		call.stateVersion = version
		reservation = call.reservation.Identity
		if err := call.ledger.budget(
			ctx, dispatchDecision, actionledger.BudgetDispatched, call.budget,
			version, 0, call.approvalReserved, call.approvalCommitted,
		); err != nil {
			if _, transitionErr := g.markIndeterminateAfterFailure(ctx, call, err); transitionErr != nil {
				return transitionErr
			}
			return err
		}
	}
	if err := call.ledger.dispatch(ctx, dispatchDecision, reservation); err != nil {
		if call.reservation != nil {
			if _, transitionErr := g.markIndeterminateAfterFailure(ctx, call, err); transitionErr != nil {
				return transitionErr
			}
		}
		return err
	}
	call.decision = dispatchDecision
	return nil
}

func approvedDispatchDecision(
	decision action.EvaluationResult,
	approvalCommitted bool,
) action.EvaluationResult {
	if decision.Decision != action.DecisionRequireApproval || !approvalCommitted {
		return decision
	}
	decision.PhaseOutcome = action.OutcomeDispatchEligible
	decision.Failure = nil
	return decision
}

func blockedGatewayResult(callID string, reason action.ReasonCode) (*mcp.CallToolResult, error) {
	return blockedGatewayResultValue(callID, reason), nil
}

func blockedGatewayResultValue(callID string, reason action.ReasonCode) *mcp.CallToolResult {
	return safeGatewayResult(
		"blocked", reason, fmt.Sprintf("Reconc blocked this tool call (%s).", reason), callID,
		"not_dispatched", "blocked",
	)
}

func (g *Gateway) storePending(state string, pending pendingApproval) error {
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()
	if state == "" || len(g.pending) >= MaxPendingApprovals {
		pending.release()
		return fmt.Errorf("pending approval capacity is exhausted")
	}
	if _, duplicate := g.pending[state]; duplicate {
		pending.release()
		return fmt.Errorf("pending approval state is duplicated")
	}
	g.pending[state] = pending
	return nil
}

func (g *Gateway) removePending(state string) {
	pending, exists := g.takePending(state)
	if exists {
		pending.release()
	}
}

func (g *Gateway) takePending(state string) (pendingApproval, bool) {
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()
	pending, ok := g.pending[state]
	if ok {
		delete(g.pending, state)
	}
	return pending, ok
}

func (g *Gateway) peekPending(state string) (pendingApproval, bool) {
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()
	pending, ok := g.pending[state]
	return pending, ok
}

func (g *Gateway) finalizeIssuedApproval(
	ctx context.Context,
	issued actionstate.ApprovalIssueResult,
	status actionapproval.Status,
) actionstate.ApprovalConsumeResult {
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	result, _ := g.state.FinalizeApproval(terminalCtx, actionstate.ApprovalFinalizeRequest{
		RequestState: issued.RequestState, ExpectedStateVersion: issued.StateVersion, Status: status,
	})
	return result
}

func (g *Gateway) releaseReservation(
	ctx context.Context,
	reservation *actionstate.Reservation,
	stateVersion string,
) string {
	if reservation == nil {
		return stateVersion
	}
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	version, err := g.state.Release(terminalCtx, reservation.Identity, stateVersion)
	if err == nil {
		return version
	}
	current, currentErr := g.state.CurrentStateVersion(terminalCtx)
	if currentErr != nil {
		return stateVersion
	}
	version, _ = g.state.Release(terminalCtx, reservation.Identity, current)
	return version
}

func (g *Gateway) denyCall(ctx context.Context, call *gatewayCall, approvalCommitted bool) {
	if call.reservation == nil {
		return
	}
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	version, err := g.state.RecordDenied(terminalCtx, call.reservation.Identity, call.stateVersion)
	if err != nil && version == "" {
		current, currentErr := g.state.CurrentStateVersion(terminalCtx)
		if currentErr == nil {
			version, err = g.state.RecordDenied(terminalCtx, call.reservation.Identity, current)
		}
	}
	if version == "" {
		_ = call.ledger.terminalFailure(
			terminalCtx, action.PhasePreCall, gatewayReason(err, action.ReasonStateUnavailable),
			action.LifecycleActive, true, true,
		)
		return
	}
	_ = call.ledger.budget(
		terminalCtx, blockDecision(call.decision, call.decision.Reason), actionledger.BudgetDenied,
		call.budget, version, 0, call.approvalReserved, approvalCommitted,
	)
}

func (g *Gateway) releaseCall(
	ctx context.Context,
	call *gatewayCall,
	reason action.ReasonCode,
	approvalCommitted bool,
) {
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	if call.reservation == nil {
		_ = call.ledger.terminalFailure(terminalCtx, action.PhasePreCall, reason, action.LifecycleActive, true, true)
		return
	}
	version := g.releaseReservation(terminalCtx, call.reservation, call.stateVersion)
	_ = call.ledger.budget(
		terminalCtx, blockDecision(call.decision, reason), actionledger.BudgetReleased,
		call.budget, version, 0, call.approvalReserved, approvalCommitted,
	)
}

func blockDecision(result action.EvaluationResult, reason action.ReasonCode) action.EvaluationResult {
	result.Decision = action.DecisionBlock
	result.Reason = reason
	result.PhaseOutcome = action.OutcomeFor(action.PhasePreCall, action.DecisionBlock)
	result.Failure = &action.Failure{Code: reason, Message: "gateway lifecycle failed closed"}
	return result
}

func (g *Gateway) recordTerminalizedApproval(
	ctx context.Context,
	call *gatewayCall,
	result actionstate.ApprovalConsumeResult,
	reason action.ReasonCode,
	approvalCommitted bool,
) {
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	if result.StateVersion == "" || result.Evidence.RequestID == "" {
		_ = call.ledger.terminalFailure(
			terminalCtx, action.PhasePreCall, action.ReasonStateUnavailable,
			action.LifecycleActive, true, true,
		)
		return
	}
	if err := call.ledger.approval(terminalCtx, call.decision, result.Evidence); err != nil {
		return
	}
	if call.reservation == nil {
		return
	}
	_ = call.ledger.budget(
		terminalCtx, blockDecision(call.decision, reason), actionledger.BudgetDenied,
		call.budget, result.StateVersion, 0, call.approvalReserved, approvalCommitted,
	)
}
