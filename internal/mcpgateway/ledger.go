package mcpgateway

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
)

type callLedger struct {
	store   *actionledger.Store
	mode    action.LedgerMode
	policy  action.LedgerPolicy
	call    actionledger.CallBinding
	request action.Request
	start   time.Time
	mu      sync.Mutex
	last    time.Time
}

func (l *callLedger) setResult(request action.Request) {
	l.mu.Lock()
	l.request = request
	l.mu.Unlock()
}

func newCallLedger(
	gateway *Gateway,
	snapshot PolicySnapshot,
	request action.Request,
	toolID string,
) (*callLedger, error) {
	plan := snapshot.Plan.Plan()
	policy := action.LedgerPolicy{Mode: action.LedgerOff, ToolIdentity: action.LedgerDeclarationID, SelectedFields: []action.LedgerField{}}
	if plan.Ledger != nil {
		policy = *plan.Ledger
		policy.SelectedFields = append([]action.LedgerField(nil), plan.Ledger.SelectedFields...)
	}
	canonicalRequest := request
	canonicalRequest.StateVersion = "mutable-state-version"
	body, err := action.CanonicalRequest(canonicalRequest)
	if err != nil {
		return nil, err
	}
	requestIdentity := ""
	tool := actionledger.ToolIdentity{Mode: policy.ToolIdentity, Value: toolID}
	err = gateway.storage.WithIdentity(func(key *actionstate.IdentityKey) error {
		requestIdentity = key.Identity(
			actionstate.DomainUpstream,
			body,
			[]byte(gateway.boundContext.ContextIdentity),
			[]byte(gateway.server.ExecutableDigest),
		)
		if toolID == "" || policy.ToolIdentity == action.LedgerKeyedName {
			tool.Mode = action.LedgerKeyedName
			tool.Value = key.Identity(actionstate.DomainLedger, []byte("tool-name"), []byte(request.Tool))
		} else if policy.ToolIdentity == action.LedgerExactName {
			tool.Value = request.Tool
		}
		return nil
	})
	if err != nil || requestIdentity == "" || tool.Value == "" {
		return nil, fmt.Errorf("derive action ledger call identities: %w", err)
	}
	labels := make([]string, len(gateway.boundContext.Credentials))
	for index, credential := range gateway.boundContext.Credentials {
		labels[index] = credential.Label
	}
	return &callLedger{
		store: gateway.ledger, mode: policy.Mode, policy: policy, request: request,
		start: time.Now().UTC(),
		call: actionledger.CallBinding{
			CallID: request.CallID, RequestIdentity: requestIdentity,
			RepositoryIdentity: request.RepositoryIdentity,
			PolicyDigest:       request.PolicyDigest, LockDigest: request.LockDigest,
			ServerLabel: request.ServerLabel, ServerFingerprint: request.ServerFingerprint,
			Tool: tool, ToolContractDigest: request.ToolContractDigest,
			Principal: gateway.boundContext.Principal, CredentialLabels: labels,
			RunIdentity: gateway.boundContext.RunIdentity, SessionIdentity: gateway.boundContext.SessionIdentity,
			ContextIdentity:   gateway.boundContext.ContextIdentity,
			ContextProvenance: action.ProvenanceOperatorBound,
		},
	}, nil
}

func (l *callLedger) requestAccepted(ctx context.Context) error {
	items, err := countValueItems(*l.request.Arguments, 0)
	if err != nil {
		return err
	}
	body, err := l.request.Arguments.MarshalJSON()
	if err != nil {
		return err
	}
	record, err := l.baseRecord(actionledger.EventRequestAccepted, action.PhasePreCall, action.EvaluationResult{})
	if err != nil {
		return err
	}
	record.RequestAccepted = &actionledger.RequestAccepted{ArgumentBytes: uint64(len(body)), ArgumentItems: items}
	return l.record(ctx, record)
}

func (l *callLedger) preDecision(ctx context.Context, result action.EvaluationResult, cached bool) error {
	record, err := l.baseRecord(actionledger.EventPreDecision, action.PhasePreCall, result)
	if err != nil {
		return err
	}
	record.PreDecision = &actionledger.PreDecision{Outcome: result.PhaseOutcome, Cached: cached}
	return l.record(ctx, record)
}

func (l *callLedger) progressDecision(
	ctx context.Context,
	result action.EvaluationResult,
	cached bool,
) error {
	record, err := l.baseRecord(actionledger.EventPreDecision, action.PhaseProgress, result)
	if err != nil {
		return err
	}
	record.PreDecision = &actionledger.PreDecision{Outcome: result.PhaseOutcome, Cached: cached}
	return l.record(ctx, record)
}

func (l *callLedger) progressSuppressed(
	ctx context.Context,
	result action.EvaluationResult,
	bytes uint64,
	items uint32,
) error {
	record, err := l.baseRecord(actionledger.EventFinalDelivery, action.PhaseProgress, result)
	if err != nil {
		return err
	}
	record.Delivery = &actionledger.FinalDelivery{
		Status: actionledger.DeliverySuppressed, ByteLength: bytes, ItemCount: items,
	}
	return l.record(ctx, record)
}

func (l *callLedger) approval(
	ctx context.Context,
	result action.EvaluationResult,
	evidence actionstate.ApprovalEvidence,
) error {
	record, err := l.baseRecord(actionledger.EventApprovalTransition, evidence.Phase, result)
	if err != nil {
		return err
	}
	record.Decision.Decision = action.DecisionRequireApproval
	record.Decision.Reason = approvalLedgerReason(evidence.Status)
	record.Approval = &actionledger.ApprovalTransition{
		RequestID: evidence.RequestID, Status: evidence.Status,
		AuthorityPolicyID: evidence.AuthorityPolicyID,
		AuthorityKeyID:    evidence.AuthorityKeyID, ReceiptID: evidence.ReceiptID,
		ReceiptIdentity: evidence.ReceiptIdentity,
	}
	return l.record(ctx, record)
}

func approvalLedgerReason(status actionapproval.Status) action.ReasonCode {
	switch status {
	case actionapproval.StatusPending, actionapproval.StatusApproved:
		return action.ReasonApprovalRequired
	case actionapproval.StatusRejected:
		return action.ReasonApprovalRejected
	case actionapproval.StatusExpired:
		return action.ReasonApprovalExpired
	case actionapproval.StatusCancelled:
		return action.ReasonCancelled
	case actionapproval.StatusReplayed:
		return action.ReasonApprovalReplayed
	case actionapproval.StatusMalformed:
		return action.ReasonApprovalInvalid
	default:
		return action.ReasonAuthorityUnavailable
	}
}

func (l *callLedger) budget(
	ctx context.Context,
	result action.EvaluationResult,
	kind actionledger.BudgetTransitionKind,
	snapshot action.BudgetSnapshot,
	stateVersion string,
	actualResultBytes uint64,
	approvalReserved bool,
	approvalCommitted bool,
) error {
	if len(snapshot.Candidates) == 0 {
		return nil
	}
	reserved, consumed, err := budgetDeltas(
		kind,
		snapshot.Candidates,
		actualResultBytes,
		approvalReserved,
		approvalCommitted,
	)
	if err != nil {
		return err
	}
	ids := make([]string, len(snapshot.Candidates))
	for index, candidate := range snapshot.Candidates {
		ids[index] = candidate.BudgetID
	}
	sort.Strings(ids)
	record, err := l.baseRecord(actionledger.EventBudgetTransition, resultPhase(result), result)
	if err != nil {
		return err
	}
	reservation := snapshot.ReservationIdentity
	if reservation == "" {
		reservation = "absent"
	}
	record.Budget = &actionledger.BudgetTransition{
		Kind: kind, ReservationIdentity: reservation, StateVersion: stateVersion,
		BudgetIDs: ids, ReservedDelta: reserved, ConsumedDelta: consumed,
	}
	return l.record(ctx, record)
}

func (l *callLedger) postApprovalReservation(
	ctx context.Context,
	result action.EvaluationResult,
	snapshot action.BudgetSnapshot,
	stateVersion string,
) error {
	if !approvalBudgetPresent(snapshot) {
		return nil
	}
	ids := make([]string, len(snapshot.Candidates))
	delta := actionledger.BudgetDelta{}
	for index, candidate := range snapshot.Candidates {
		ids[index] = candidate.BudgetID
		if candidate.Limits.ApprovalCount != 0 {
			var err error
			delta.ApprovalCount, err = addInt64(delta.ApprovalCount, 1)
			if err != nil {
				return err
			}
		}
	}
	sort.Strings(ids)
	record, err := l.baseRecord(actionledger.EventBudgetTransition, action.PhasePostResult, result)
	if err != nil {
		return err
	}
	record.Budget = &actionledger.BudgetTransition{
		Kind: actionledger.BudgetReserved, ReservationIdentity: snapshot.ReservationIdentity,
		StateVersion: stateVersion, BudgetIDs: ids, ReservedDelta: delta,
		ConsumedDelta: actionledger.BudgetDelta{},
	}
	return l.record(ctx, record)
}

func budgetDeltas(
	kind actionledger.BudgetTransitionKind,
	candidates []action.BudgetCandidate,
	actualResultBytes uint64,
	approvalReserved bool,
	approvalCommitted bool,
) (actionledger.BudgetDelta, actionledger.BudgetDelta, error) {
	var reserved, consumed actionledger.BudgetDelta
	for _, candidate := range candidates {
		value, err := budgetUsageDelta(candidate.Required)
		if err != nil {
			return reserved, consumed, err
		}
		approval := actionledger.BudgetDelta{}
		if candidate.Limits.ApprovalCount != 0 && (approvalReserved || approvalCommitted) {
			approval.ApprovalCount = 1
		}
		switch kind {
		case actionledger.BudgetReserved:
			reserved, err = addBudgetDelta(reserved, value)
			if err == nil && approvalReserved {
				reserved, err = addBudgetDelta(reserved, approval)
			}
		case actionledger.BudgetReleased:
			reserved, err = addBudgetDelta(reserved, negateBudgetDelta(value))
			if err == nil && approvalReserved {
				reserved, err = addBudgetDelta(reserved, negateBudgetDelta(approval))
			}
			if err == nil && approvalCommitted {
				consumed, err = addBudgetDelta(consumed, approval)
			}
		case actionledger.BudgetDispatched:
			dispatch := dispatchConsumedDelta(value)
			reserved, err = addBudgetDelta(reserved, negateBudgetDelta(dispatch))
			if err == nil && approvalReserved {
				reserved, err = addBudgetDelta(reserved, negateBudgetDelta(approval))
			}
			if err == nil {
				consumed, err = addBudgetDelta(consumed, dispatch)
			}
			if err == nil && approvalCommitted {
				consumed, err = addBudgetDelta(consumed, approval)
			}
		case actionledger.BudgetSettled:
			settlement := postDispatchReservedDelta(value)
			reserved, err = addBudgetDelta(reserved, negateBudgetDelta(settlement))
			if err == nil && approvalReserved {
				reserved, err = addBudgetDelta(reserved, negateBudgetDelta(approval))
			}
			if err == nil && approvalCommitted {
				consumed, err = addBudgetDelta(consumed, approval)
			}
			if err == nil && candidate.Required.ResultBytes != 0 {
				if actualResultBytes > math.MaxInt64 {
					return reserved, consumed, fmt.Errorf("actual result-byte ledger delta exceeds signed range")
				}
				consumed.ResultBytes, err = addInt64(consumed.ResultBytes, int64(actualResultBytes))
			}
		case actionledger.BudgetDenied:
			reserved, err = addBudgetDelta(reserved, negateBudgetDelta(value))
			if err == nil && approvalReserved {
				reserved, err = addBudgetDelta(reserved, negateBudgetDelta(approval))
			}
			if err == nil && approvalCommitted {
				consumed, err = addBudgetDelta(consumed, approval)
			}
			if candidate.Limits.DeniedCount != 0 {
				consumed.DeniedCount, err = addInt64(consumed.DeniedCount, 1)
			}
		case actionledger.BudgetIndeterminate:
			return actionledger.BudgetDelta{}, actionledger.BudgetDelta{}, nil
		}
		if err != nil {
			return reserved, consumed, err
		}
	}
	return reserved, consumed, nil
}

func budgetUsageDelta(value action.BudgetUsage) (actionledger.BudgetDelta, error) {
	values := []uint64{
		value.CallCount, value.DeniedCount, value.ApprovalCount, value.ArgumentBytes,
		value.ResultBytes, value.CostUnits, value.Concurrent, value.RateWindow,
	}
	for _, item := range values {
		if item > math.MaxInt64 {
			return actionledger.BudgetDelta{}, fmt.Errorf("budget ledger delta exceeds signed range")
		}
	}
	return actionledger.BudgetDelta{
		CallCount: int64(value.CallCount), DeniedCount: int64(value.DeniedCount),
		ApprovalCount: int64(value.ApprovalCount), ArgumentBytes: int64(value.ArgumentBytes),
		ResultBytes: int64(value.ResultBytes), CostUnits: int64(value.CostUnits),
		Concurrent: int64(value.Concurrent), RateWindow: int64(value.RateWindow),
	}, nil
}

func addBudgetDelta(left, right actionledger.BudgetDelta) (actionledger.BudgetDelta, error) {
	values := []*int64{
		&left.CallCount, &left.DeniedCount, &left.ApprovalCount, &left.ArgumentBytes,
		&left.ResultBytes, &left.CostUnits, &left.Concurrent, &left.RateWindow,
	}
	rightValues := []int64{
		right.CallCount, right.DeniedCount, right.ApprovalCount, right.ArgumentBytes,
		right.ResultBytes, right.CostUnits, right.Concurrent, right.RateWindow,
	}
	for index, value := range rightValues {
		sum, err := addInt64(*values[index], value)
		if err != nil {
			return actionledger.BudgetDelta{}, err
		}
		*values[index] = sum
	}
	return left, nil
}

func addInt64(left, right int64) (int64, error) {
	if right > 0 && left > math.MaxInt64-right || right < 0 && left < math.MinInt64-right {
		return 0, fmt.Errorf("budget ledger delta overflow")
	}
	return left + right, nil
}

func negateBudgetDelta(value actionledger.BudgetDelta) actionledger.BudgetDelta {
	return actionledger.BudgetDelta{
		CallCount: -value.CallCount, DeniedCount: -value.DeniedCount,
		ApprovalCount: -value.ApprovalCount, ArgumentBytes: -value.ArgumentBytes,
		ResultBytes: -value.ResultBytes, CostUnits: -value.CostUnits,
		Concurrent: -value.Concurrent, RateWindow: -value.RateWindow,
	}
}

func dispatchConsumedDelta(value actionledger.BudgetDelta) actionledger.BudgetDelta {
	value.ResultBytes = 0
	value.Concurrent = 0
	value.ApprovalCount = 0
	return value
}

func postDispatchReservedDelta(value actionledger.BudgetDelta) actionledger.BudgetDelta {
	return actionledger.BudgetDelta{
		ResultBytes: value.ResultBytes,
		Concurrent:  value.Concurrent,
	}
}

func (l *callLedger) dispatch(ctx context.Context, result action.EvaluationResult, reservation string) error {
	record, err := l.baseRecord(actionledger.EventDownstreamDispatch, action.PhasePreCall, result)
	if err != nil {
		return err
	}
	if reservation == "" {
		reservation = "absent"
	}
	record.Dispatch = &actionledger.DownstreamDispatch{ReservationIdentity: reservation}
	return l.record(ctx, record)
}

func (l *callLedger) downstream(
	ctx context.Context,
	result action.EvaluationResult,
	status actionledger.DownstreamStatus,
) error {
	record, err := l.baseRecord(actionledger.EventDownstreamOutcome, action.PhasePostResult, result)
	if err != nil {
		return err
	}
	record.Downstream = &actionledger.DownstreamOutcome{Status: status}
	return l.record(ctx, record)
}

func (l *callLedger) inspection(
	ctx context.Context,
	result action.EvaluationResult,
	evidence action.InspectionEvidence,
) error {
	record, err := l.baseRecord(actionledger.EventResultInspection, action.PhasePostResult, result)
	if err != nil {
		return err
	}
	record.Inspection = &actionledger.ResultInspection{
		Status: evidence.Status, Categories: append([]action.DetectorCategory{}, evidence.Categories...),
		SchemaStatus: evidence.SchemaStatus, ScannedBytes: evidence.ScannedBytes,
		ScannedItems: evidence.ScannedItems, UnsupportedContent: uint32(len(evidence.UnsupportedContent)),
	}
	return l.record(ctx, record)
}

func (l *callLedger) delivery(
	ctx context.Context,
	result action.EvaluationResult,
	status actionledger.DeliveryStatus,
	bytes uint64,
	items uint32,
) error {
	record, err := l.baseRecord(actionledger.EventFinalDelivery, action.PhasePostResult, result)
	if err != nil {
		return err
	}
	record.Delivery = &actionledger.FinalDelivery{Status: status, ByteLength: bytes, ItemCount: items}
	return l.record(ctx, record)
}

func (l *callLedger) terminalFailure(
	ctx context.Context,
	phase action.Phase,
	reason action.ReasonCode,
	lifecycle action.LifecycleState,
	dispatchKnown bool,
	deliveryKnown bool,
) error {
	result := action.EvaluationResult{
		Decision: action.DecisionBlock, Reason: reason, MatchedRuleIDs: []string{},
		Completeness: action.CompleteEvidence(), PhaseOutcome: action.OutcomeWithheld,
	}
	if phase == action.PhasePreCall {
		result.PhaseOutcome = action.OutcomeDispatchBlocked
	}
	record, err := l.baseRecord(actionledger.EventTerminalFailure, phase, result)
	if err != nil {
		return err
	}
	record.Failure = &actionledger.TerminalFailure{
		Lifecycle: lifecycle, DispatchKnown: dispatchKnown, DeliveryKnown: deliveryKnown,
	}
	return l.record(ctx, record)
}

func (l *callLedger) baseRecord(
	event actionledger.EventType,
	phase action.Phase,
	result action.EvaluationResult,
) (actionledger.Record, error) {
	selected, err := l.selectedFields(phase)
	if err != nil {
		return actionledger.Record{}, err
	}
	decision := actionledger.DecisionBinding{
		Phase: phase, Decision: result.Decision, Reason: result.Reason,
		RuleIDs: append([]string{}, result.MatchedRuleIDs...), Completeness: result.Completeness,
	}
	if event == actionledger.EventRequestAccepted {
		decision.Decision = ""
		decision.Reason = ""
		decision.RuleIDs = []string{}
		decision.Completeness = action.CompleteEvidence()
	}
	return actionledger.Record{
		Timestamp: l.timestamp(), LatencyMicros: l.latency(), Event: event,
		Call: l.call, Decision: decision, SelectedFields: selected,
	}, nil
}

func (l *callLedger) selectedFields(phase action.Phase) ([]actionledger.SelectedFieldEvidence, error) {
	if l.mode == action.LedgerOff {
		return []actionledger.SelectedFieldEvidence{}, nil
	}
	l.mu.Lock()
	request := l.request
	l.mu.Unlock()
	source := action.SourceArguments
	root := request.Arguments
	switch phase {
	case action.PhasePostResult:
		source = action.SourceResult
		root = request.Result
	case action.PhaseProgress, action.PhaseObservation:
		return []actionledger.SelectedFieldEvidence{}, nil
	}
	fields := make([]actionledger.SelectedFieldEvidence, 0)
	for index, declaration := range l.policy.SelectedFields {
		if declaration.Source != source {
			continue
		}
		if root == nil {
			return []actionledger.SelectedFieldEvidence{}, nil
		}
		selected, err := action.ResolvePointer(*root, declaration.Pointer)
		if err != nil {
			return nil, err
		}
		evidence, err := l.store.SelectedField(actionledger.SelectedFieldInput{
			DeclarationIndex: uint16(index), PolicyDigest: l.call.PolicyDigest,
			LockDigest: l.call.LockDigest, ToolContractDigest: l.call.ToolContractDigest,
			Source: source, Pointer: declaration.Pointer, Selected: selected,
		})
		if err != nil {
			return nil, err
		}
		fields = append(fields, evidence)
	}
	if fields == nil {
		fields = []actionledger.SelectedFieldEvidence{}
	}
	return fields, nil
}

func (l *callLedger) record(ctx context.Context, record actionledger.Record) error {
	result, err := l.store.Record(ctx, l.mode, record)
	if err != nil {
		return err
	}
	if !result.Proceed {
		return fmt.Errorf("required action ledger event was not recorded")
	}
	return nil
}

func (l *callLedger) timestamp() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	if !l.last.IsZero() && !now.After(l.last) {
		now = l.last.Add(time.Nanosecond)
	}
	l.last = now
	return now.Format(time.RFC3339Nano)
}

func (l *callLedger) latency() uint64 {
	duration := time.Since(l.start)
	if duration < 0 {
		return 0
	}
	if duration > 10*time.Minute {
		duration = 10 * time.Minute
	}
	return uint64(duration / time.Microsecond)
}

func resultPhase(result action.EvaluationResult) action.Phase {
	if result.PhaseOutcome == action.OutcomeDeliveryEligible || result.PhaseOutcome == action.OutcomeWithheld {
		return action.PhasePostResult
	}
	return action.PhasePreCall
}

func countValueItems(value action.Value, depth int) (uint32, error) {
	if depth > action.MaxJSONDepth {
		return 0, fmt.Errorf("value exceeds action JSON depth")
	}
	var count uint64
	switch value.Kind() {
	case action.ValueArray:
		items, _ := value.Items()
		count = uint64(len(items))
		for _, item := range items {
			child, err := countValueItems(item, depth+1)
			if err != nil {
				return 0, err
			}
			count += uint64(child)
		}
	case action.ValueObject:
		members, _ := value.Members()
		count = uint64(len(members))
		for _, member := range members {
			child, err := countValueItems(member.Value, depth+1)
			if err != nil {
				return 0, err
			}
			count += uint64(child)
		}
	}
	if count > action.MaxJSONItems {
		return 0, fmt.Errorf("value exceeds action JSON item boundary")
	}
	return uint32(count), nil
}
