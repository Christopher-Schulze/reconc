package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actioninspect"
	"reconc.dev/reconc/internal/actionstate"
)

type legacyApprovalPendingError struct {
	state  string
	callID string
}

func (e *legacyApprovalPendingError) Error() string {
	return "legacy MCP approval requires form elicitation"
}

func (g *Gateway) resumeApproval(
	ctx context.Context,
	sdkRequest *mcp.CallToolRequest,
	wire upstreamWireCall,
	contract ToolContract,
	generation uint64,
	fallbackCallID string,
) (*mcp.CallToolResult, error) {
	pending, exists := g.takePending(sdkRequest.Params.RequestState)
	if !exists {
		return blockedGatewayResult(fallbackCallID, action.ReasonApprovalInvalid)
	}
	defer pending.release()
	if pending.upstreamProtocol != actionapproval.MCPProtocolVersion ||
		sdkRequest.ProtocolVersion() != pending.upstreamProtocol ||
		pending.contract.ContractDigest != contract.ContractDigest ||
		pending.generation != generation {
		return g.failPendingApproval(ctx, pending, actionapproval.StatusMalformed, action.ReasonToolContractStale)
	}
	receipt, err := actionapproval.ParseMCPApprovalRetry(
		pending.originalRPCID,
		wire.id,
		pending.originalParams,
		wire.params,
		pending.requestState,
	)
	if err != nil {
		reason := gatewayReason(err, action.ReasonApprovalInvalid)
		return g.failPendingApproval(ctx, pending, approvalFailureFinalizeStatus(err), reason)
	}
	callCtx, cancel := context.WithTimeout(ctx, g.config.CallTimeout)
	defer cancel()
	g.transitionMu.Lock()
	if pending.phase == action.PhasePostResult {
		response, err := g.consumePostApproval(callCtx, pending, receipt)
		g.transitionMu.Unlock()
		return response, err
	}
	progress, progressErr := newCallProgress(wire.params)
	if progressErr != nil {
		g.transitionMu.Unlock()
		return g.failPendingApproval(
			callCtx, pending, actionapproval.StatusMalformed, action.ReasonInvalidRequest,
		)
	}
	call, response := g.consumeApproval(callCtx, pending, receipt)
	g.transitionMu.Unlock()
	if response != nil || call == nil {
		return response, nil
	}
	call.wire = wire
	call.progress = progress
	return g.executeCall(callCtx, call)
}

func (g *Gateway) elicitLegacyApproval(
	ctx context.Context,
	state string,
	fallbackCallID string,
	progress *callProgress,
) (*mcp.CallToolResult, error) {
	pending, exists := g.peekPending(state)
	if !exists {
		return blockedGatewayResult(fallbackCallID, action.ReasonApprovalInvalid)
	}
	if pending.callID != "" {
		fallbackCallID = pending.callID
	}
	if pending.upstreamProtocol != gatewayProtocolLegacy {
		pending, exists = g.takePending(state)
		if !exists {
			return blockedGatewayResult(fallbackCallID, action.ReasonApprovalInvalid)
		}
		defer pending.release()
		return g.failPendingApproval(
			ctx, pending, actionapproval.StatusMalformed, action.ReasonApprovalInvalid,
		)
	}
	inputRequired, err := actionapproval.BuildMCPInputRequired(
		pending.approvalRequest,
		pending.requestState,
	)
	if err != nil {
		pending, exists = g.takePending(state)
		if !exists {
			return blockedGatewayResult(fallbackCallID, action.ReasonApprovalInvalid)
		}
		defer pending.release()
		return g.failPendingApproval(
			ctx, pending, actionapproval.StatusMalformed, action.ReasonApprovalInvalid,
		)
	}
	request := inputRequired.InputRequests[actionapproval.MCPApprovalInputID]
	session := g.upstreamSession()
	if session == nil {
		pending, exists = g.takePending(state)
		if !exists {
			return blockedGatewayResult(fallbackCallID, action.ReasonApprovalInvalid)
		}
		defer pending.release()
		return g.failPendingApproval(
			ctx, pending, actionapproval.StatusUnavailable, action.ReasonApprovalRequired,
		)
	}
	result, elicitErr := session.Elicit(ctx, &mcp.ElicitParams{
		Mode: request.Params.Mode, Message: request.Params.Message,
		RequestedSchema: request.Params.RequestedSchema,
	})
	pending, exists = g.takePending(state)
	if !exists {
		reason := action.ReasonApprovalInvalid
		if g.ctx.Err() != nil {
			reason = action.ReasonShutdown
		}
		return blockedGatewayResult(fallbackCallID, reason)
	}
	defer pending.release()
	if elicitErr != nil {
		return g.failPendingApproval(
			ctx, pending, actionapproval.StatusUnavailable, action.ReasonApprovalRequired,
		)
	}
	body, err := json.Marshal(result)
	if err != nil {
		return g.failPendingApproval(
			ctx, pending, actionapproval.StatusMalformed, action.ReasonProtocolError,
		)
	}
	receipt, err := actionapproval.ParseMCPElicitationResponse(body)
	if err != nil {
		reason := gatewayReason(err, action.ReasonApprovalInvalid)
		return g.failPendingApproval(ctx, pending, approvalFailureFinalizeStatus(err), reason)
	}
	g.transitionMu.Lock()
	if pending.phase == action.PhasePostResult {
		response, consumeErr := g.consumePostApproval(ctx, pending, receipt)
		g.transitionMu.Unlock()
		return response, consumeErr
	}
	call, response := g.consumeApproval(ctx, pending, receipt)
	g.transitionMu.Unlock()
	if response != nil || call == nil {
		return response, nil
	}
	call.wire = upstreamWireCall{
		id: bytes.Clone(pending.originalRPCID), params: bytes.Clone(pending.originalParams),
	}
	call.progress = progress
	return g.executeCall(ctx, call)
}

func (g *Gateway) requestPostApproval(
	ctx context.Context,
	call *gatewayCall,
	rawResult json.RawMessage,
) (*mcp.CallToolResult, error) {
	if g.config.ApprovalPolicyID == "" {
		decision := postApprovalBlockedDecision(call.decision, action.ReasonAuthorityUnavailable)
		return g.withholdResult(ctx, call, decision, nil)
	}
	issued, err := g.state.IssueApproval(ctx, actionstate.ApprovalIssueRequest{
		Binding: actionstate.ApprovalBinding{
			Plan: call.snapshot.Plan, Evaluation: call.evaluation,
			BudgetRequest: call.preRequest,
			Context:       g.boundContext, Authority: g.config.PolicyAuthority, Server: g.server,
		},
		AuthorityPolicyID: g.config.ApprovalPolicyID,
		TTL:               actionapproval.MaximumApprovalTTL,
	})
	if err != nil {
		g.diagnostic("post-result approval issuance failed: " + string(
			gatewayReason(err, action.ReasonAuthorityUnavailable),
		))
		decision := postApprovalBlockedDecision(
			call.decision,
			gatewayReason(err, action.ReasonAuthorityUnavailable),
		)
		return g.withholdResult(ctx, call, decision, nil)
	}
	call.stateVersion = issued.StateVersion
	call.postApprovalReserved = approvalBudgetPresent(call.budget)
	if call.postApprovalReserved {
		if err := call.ledger.postApprovalReservation(
			ctx, call.decision, call.budget, issued.StateVersion,
		); err != nil {
			finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusUnavailable)
			call.stateVersion = finalized.StateVersion
			return g.blockApprovalLedgerFailure(ctx, call, err)
		}
	}
	if err := call.ledger.approval(ctx, call.decision, issued.Evidence); err != nil {
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusUnavailable)
		call.stateVersion = finalized.StateVersion
		return g.blockApprovalLedgerFailure(ctx, call, err)
	}
	baseParams, err := actionapproval.CanonicalMCPApprovalBaseParams(call.wire.params)
	if err != nil {
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusMalformed)
		return g.finalizePostApproval(ctx, call, finalized, action.ReasonProtocolError)
	}
	pending := pendingApproval{
		phase: action.PhasePostResult, callID: call.callID,
		approvalRequest: issued.Request,
		requestState:    issued.RequestState, originalRPCID: bytes.Clone(call.wire.id),
		originalParams: baseParams, canonicalArguments: bytes.Clone(call.canonicalArguments),
		contract: call.contract, generation: call.generation, snapshot: call.snapshot,
		preRequest: call.preRequest, evaluation: call.evaluation, decision: call.decision,
		downstreamDecision: call.downstreamDecision,
		reservation:        call.reservation, budget: call.budget,
		issuanceVersion: issued.StateVersion, ledger: call.ledger,
		postApprovalReserved:  call.postApprovalReserved,
		postApprovalCommitted: call.postApprovalCommitted,
		resultIsError:         call.resultIsError, actualResultBytes: call.actualResultBytes,
		upstreamProtocol: call.upstreamProtocol, downstreamProtocol: g.downstream.ProtocolVersion(),
		rawResult:       bytes.Clone(rawResult),
		repositoryPaths: cloneRepositoryPathBindings(call.repositoryPaths),
	}
	if err := g.storePending(issued.RequestState, pending); err != nil {
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusUnavailable)
		return g.finalizePostApproval(ctx, call, finalized, action.ReasonStateUnavailable)
	}
	if call.upstreamProtocol == gatewayProtocolLegacy {
		return nil, &legacyApprovalPendingError{state: issued.RequestState, callID: call.callID}
	}
	if call.upstreamProtocol != actionapproval.MCPProtocolVersion {
		g.removePending(issued.RequestState)
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusUnavailable)
		return g.finalizePostApproval(
			ctx, call, finalized, action.ReasonApprovalRequired,
		)
	}
	inputRequired, err := actionapproval.BuildMCPInputRequired(issued.Request, issued.RequestState)
	if err != nil {
		g.removePending(issued.RequestState)
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusMalformed)
		return g.finalizePostApproval(ctx, call, finalized, action.ReasonApprovalInvalid)
	}
	response, err := sdkApprovalResult(inputRequired)
	if err != nil {
		g.removePending(issued.RequestState)
		finalized := g.finalizeIssuedApproval(ctx, issued, actionapproval.StatusUnavailable)
		return g.finalizePostApproval(ctx, call, finalized, action.ReasonProtocolError)
	}
	return response, nil
}

func (g *Gateway) consumeApproval(
	ctx context.Context,
	pending pendingApproval,
	receipt []byte,
) (*gatewayCall, *mcp.CallToolResult) {
	call := callFromPending(pending)
	if err := g.resampleCallBoundary(
		ctx, pending.snapshot, pending.contract, pending.generation, pending.repositoryPaths,
	); err != nil {
		result := g.finalizePendingApproval(ctx, pending, actionapproval.StatusUnavailable)
		g.recordTerminalizedApproval(ctx, call, result, action.ReasonPolicyStale, false)
		return nil, blockedGatewayResultValue(pending.callID, action.ReasonPolicyStale)
	}
	freshInput, freshDecision, reason := g.refreshPreApprovalEvaluation(ctx, pending)
	if reason != "" {
		result := g.finalizePendingApproval(ctx, pending, actionapproval.StatusMalformed)
		call.decision = freshDecision
		g.recordTerminalizedApproval(ctx, call, result, reason, false)
		return nil, blockedGatewayResultValue(pending.callID, reason)
	}
	pending.evaluation = freshInput
	pending.decision = freshDecision
	call.evaluation = freshInput
	call.decision = freshDecision
	consumed, err := g.state.ConsumeApproval(ctx, actionstate.ApprovalConsumeRequest{
		Binding: actionstate.ApprovalBinding{
			Plan: pending.snapshot.Plan, Evaluation: pending.evaluation,
			BudgetRequest: pending.preRequest,
			Context:       g.boundContext, Authority: g.config.PolicyAuthority, Server: g.server,
		},
		Registry: pendingRegistry(g), Receipt: receipt,
		RequestState: pending.requestState, ExpectedStateVersion: pending.issuanceVersion,
	})
	if err != nil || consumed.Status != actionapproval.StatusApproved {
		if consumed.StateVersion == "" {
			consumed = g.finalizePendingApproval(ctx, pending, approvalFailureFinalizeStatus(err))
		}
		reason := gatewayReason(err, approvalLedgerReason(consumed.Status))
		g.recordTerminalizedApproval(ctx, call, consumed, reason, false)
		return nil, blockedGatewayResultValue(pending.callID, reason)
	}
	if err := call.ledger.approval(ctx, call.decision, consumed.Evidence); err != nil {
		g.terminalizeApprovedReservation(ctx, call, consumed.StateVersion)
		return nil, blockedGatewayResultValue(pending.callID, action.ReasonLedgerUnavailable)
	}
	request := pending.evaluation.Request
	request.StateVersion = consumed.StateVersion
	retry, err := g.state.Reserve(ctx, actionstate.ReserveRequest{
		Plan: pending.snapshot.Plan, Request: request, Context: g.boundContext,
		Authority: g.config.PolicyAuthority, Server: g.server,
	})
	if err != nil {
		call.stateVersion = consumed.StateVersion
		call.approvalCommitted = true
		g.denyCall(ctx, call, true)
		return nil, blockedGatewayResultValue(pending.callID, gatewayReason(err, action.ReasonStateUnavailable))
	}
	request.StateVersion = retry.Snapshot.StateVersion
	tool, _, err := pending.snapshot.Plan.BudgetContract(request)
	if err != nil {
		call.stateVersion = retry.Snapshot.StateVersion
		call.approvalCommitted = true
		g.denyCall(ctx, call, true)
		return nil, blockedGatewayResultValue(pending.callID, action.ReasonPolicyMissing)
	}
	evidence, err := g.evidence(ctx, pending.snapshot, request, tool)
	if err != nil {
		call.stateVersion = retry.Snapshot.StateVersion
		call.approvalCommitted = true
		g.denyCall(ctx, call, true)
		return nil, blockedGatewayResultValue(pending.callID, action.ReasonInspectionIncomplete)
	}
	inspector, err := g.inspectionEngine(pending.snapshot.Plan)
	if err != nil {
		call.stateVersion = retry.Snapshot.StateVersion
		call.approvalCommitted = true
		g.denyCall(ctx, call, true)
		return nil, blockedGatewayResultValue(pending.callID, action.ReasonInspectionIncomplete)
	}
	inspection, err := inspector.Inspect(ctx, request, nil, nil)
	if err != nil {
		call.stateVersion = retry.Snapshot.StateVersion
		call.approvalCommitted = true
		g.denyCall(ctx, call, true)
		return nil, blockedGatewayResultValue(pending.callID, gatewayReason(err, action.ReasonInspectionIncomplete))
	}
	input := g.evaluationInput(pending.snapshot, request, retry.Snapshot, consumed.Approval, evidence)
	input.Inspection = inspection
	input.ResampledIdentities = pending.snapshot.Evaluator.IdentitySnapshot(input)
	decision, _ := g.evaluate(pending.snapshot.Evaluator, input)
	call.request = request
	call.preRequest = request
	call.tool = tool
	call.evaluation = input
	call.decision = decision
	call.budget = retry.Snapshot
	call.reservation = retry.Reservation
	call.stateVersion = retry.Snapshot.StateVersion
	call.approvalCommitted = true
	call.repositoryPaths = cloneRepositoryPathBindings(evidence.RepositoryPaths)
	call.ledger.setResult(request)
	if decision.Decision == action.DecisionBlock || decision.Failure != nil {
		g.denyCall(ctx, call, true)
		return nil, blockedGatewayResultValue(pending.callID, decision.Reason)
	}
	if decision.Decision != action.DecisionAllow && decision.Decision != action.DecisionWarn &&
		decision.Decision != action.DecisionRequireApproval {
		g.denyCall(ctx, call, true)
		return nil, blockedGatewayResultValue(pending.callID, action.ReasonInternalInvariant)
	}
	if err := g.commitDispatch(ctx, call); err != nil {
		return nil, blockedGatewayResultValue(pending.callID, gatewayReason(err, action.ReasonStateUnavailable))
	}
	return call, nil
}

func (g *Gateway) refreshPreApprovalEvaluation(
	ctx context.Context,
	pending pendingApproval,
) (action.EvaluationInput, action.EvaluationResult, action.ReasonCode) {
	request := pending.evaluation.Request
	tool, _, err := pending.snapshot.Plan.BudgetContract(request)
	if err != nil {
		return action.EvaluationInput{}, pending.decision, action.ReasonPolicyMissing
	}
	evidence, err := g.evidence(ctx, pending.snapshot, request, tool)
	if err != nil {
		return action.EvaluationInput{}, pending.decision, action.ReasonInspectionIncomplete
	}
	inspector, err := g.inspectionEngine(pending.snapshot.Plan)
	if err != nil {
		return action.EvaluationInput{}, pending.decision, action.ReasonInspectionIncomplete
	}
	inspection, err := inspector.Inspect(ctx, request, nil, nil)
	if err != nil {
		return action.EvaluationInput{}, pending.decision, inspectionFailureReason(
			gatewayReason(err, action.ReasonInspectionIncomplete),
		)
	}
	input := g.evaluationInput(
		pending.snapshot,
		request,
		pending.evaluation.Budget,
		action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
		evidence,
	)
	input.Inspection = inspection
	input.ResampledIdentities = pending.snapshot.Evaluator.IdentitySnapshot(input)
	decision, _ := g.evaluate(pending.snapshot.Evaluator, input)
	if decision.Failure != nil || decision.Decision != action.DecisionRequireApproval ||
		decision.RequiredApprovalIdentity != pending.decision.RequiredApprovalIdentity {
		return input, decision, action.ReasonApprovalInvalid
	}
	return input, decision, ""
}

func (g *Gateway) consumePostApproval(
	ctx context.Context,
	pending pendingApproval,
	receipt []byte,
) (*mcp.CallToolResult, error) {
	call := callFromPending(pending)
	if err := g.resampleCallBoundary(
		ctx, pending.snapshot, pending.contract, pending.generation, pending.repositoryPaths,
	); err != nil {
		result := g.finalizePendingApproval(ctx, pending, actionapproval.StatusUnavailable)
		return g.finalizePostApproval(ctx, call, result, action.ReasonPolicyStale)
	}
	freshInput, freshDecision, reason := g.refreshPostApprovalEvaluation(ctx, pending)
	if reason != "" {
		result := g.finalizePendingApproval(ctx, pending, actionapproval.StatusMalformed)
		call.decision = freshDecision
		return g.finalizePostApproval(ctx, call, result, reason)
	}
	pending.evaluation = freshInput
	pending.decision = freshDecision
	call.evaluation = freshInput
	call.decision = freshDecision
	consumed, err := g.state.ConsumeApproval(ctx, actionstate.ApprovalConsumeRequest{
		Binding: actionstate.ApprovalBinding{
			Plan: pending.snapshot.Plan, Evaluation: pending.evaluation,
			BudgetRequest: pending.preRequest,
			Context:       g.boundContext, Authority: g.config.PolicyAuthority, Server: g.server,
		},
		Registry: pendingRegistry(g), Receipt: receipt,
		RequestState: pending.requestState, ExpectedStateVersion: pending.issuanceVersion,
	})
	if err != nil || consumed.Status != actionapproval.StatusApproved {
		if consumed.StateVersion == "" {
			consumed = g.finalizePendingApproval(ctx, pending, approvalFailureFinalizeStatus(err))
		}
		reason := gatewayReason(err, approvalLedgerReason(consumed.Status))
		return g.finalizePostApproval(ctx, call, consumed, reason)
	}
	if err := call.ledger.approval(ctx, call.decision, consumed.Evidence); err != nil {
		call.postApprovalCommitted = true
		call.stateVersion = consumed.StateVersion
		return g.blockApprovalLedgerFailure(ctx, call, err)
	}
	call.postApprovalCommitted = true
	call.stateVersion = consumed.StateVersion
	input := pending.evaluation
	input.Approval = consumed.Approval
	input.ResampledIdentities = pending.snapshot.Evaluator.IdentitySnapshot(input)
	decision := approvedPostDecision(pending.decision)
	call.request = input.Request
	call.evaluation = input
	call.decision = decision
	call.stateVersion = consumed.StateVersion
	if decision.Decision == action.DecisionRequireApproval {
		return g.forwardResult(ctx, call, decision, pending.rawResult)
	}
	return g.withholdResult(
		ctx,
		call,
		postApprovalBlockedDecision(decision, action.ReasonInternalInvariant),
		nil,
	)
}

func (g *Gateway) refreshPostApprovalEvaluation(
	ctx context.Context,
	pending pendingApproval,
) (action.EvaluationInput, action.EvaluationResult, action.ReasonCode) {
	stateVersion := pending.evaluation.Request.StateVersion
	request, err := g.normalizedRequest(
		pending.snapshot,
		pending.contract,
		pending.callID,
		stateVersion,
		action.PhasePostResult,
		pending.rawResult,
	)
	if err != nil {
		return action.EvaluationInput{}, pending.decision, gatewayReason(err, action.ReasonInvalidRequest)
	}
	tool, _, err := pending.snapshot.Plan.BudgetContract(request)
	if err != nil {
		return action.EvaluationInput{}, pending.decision, action.ReasonPolicyMissing
	}
	evidence, err := g.evidence(ctx, pending.snapshot, pending.preRequest, tool)
	if err != nil {
		return action.EvaluationInput{}, pending.decision, action.ReasonInspectionIncomplete
	}
	decoded, err := actioninspect.DecodeMCPToolResult(pending.rawResult, pending.downstreamProtocol)
	if err != nil {
		return action.EvaluationInput{}, pending.decision, action.ReasonProtocolError
	}
	defer decoded.Release()
	call := callFromPending(pending)
	inspection, _, err := g.inspectResult(ctx, call, request, decoded)
	if err != nil {
		return action.EvaluationInput{}, pending.decision, inspectionFailureReason(
			gatewayReason(err, action.ReasonInspectionIncomplete),
		)
	}
	input := g.evaluationInput(
		pending.snapshot,
		request,
		action.BudgetSnapshot{},
		action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
		evidence,
	)
	input.Inspection = inspection
	input.ResampledIdentities = pending.snapshot.Evaluator.IdentitySnapshot(input)
	decision, _ := g.evaluate(pending.snapshot.Evaluator, input)
	if decision.Failure != nil || decision.Decision != action.DecisionRequireApproval ||
		decision.RequiredApprovalIdentity != pending.decision.RequiredApprovalIdentity {
		return input, decision, action.ReasonApprovalInvalid
	}
	return input, decision, ""
}

func (g *Gateway) finalizePostApproval(
	ctx context.Context,
	call *gatewayCall,
	result actionstate.ApprovalConsumeResult,
	reason action.ReasonCode,
) (*mcp.CallToolResult, error) {
	if result.StateVersion == "" || result.Evidence.RequestID == "" {
		return blockedGatewayResult(call.callID, action.ReasonStateUnavailable)
	}
	if err := call.ledger.approval(ctx, call.decision, result.Evidence); err != nil {
		return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
	}
	call.stateVersion = result.StateVersion
	decision := postApprovalBlockedDecision(call.decision, reason)
	return g.withholdResult(ctx, call, decision, nil)
}

func approvedPostDecision(result action.EvaluationResult) action.EvaluationResult {
	result.PhaseOutcome = action.OutcomeDeliveryEligible
	result.Failure = nil
	return result
}

func postApprovalBlockedDecision(
	result action.EvaluationResult,
	reason action.ReasonCode,
) action.EvaluationResult {
	result.Decision = action.DecisionBlock
	result.Reason = reason
	result.PhaseOutcome = action.OutcomeWithheld
	result.Failure = nil
	return result
}

func callFromPending(pending pendingApproval) *gatewayCall {
	return &gatewayCall{
		callID: pending.callID, contract: pending.contract, generation: pending.generation,
		snapshot: pending.snapshot, request: pending.evaluation.Request, preRequest: pending.preRequest,
		evaluation: pending.evaluation, decision: pending.decision,
		downstreamDecision: pending.downstreamDecision,
		ledger:             pending.ledger, budget: pending.budget, reservation: pending.reservation,
		stateVersion:          pending.issuanceVersion,
		canonicalArguments:    pending.canonicalArguments,
		approvalReserved:      pending.approvalReserved,
		postApprovalReserved:  pending.postApprovalReserved,
		postApprovalCommitted: pending.postApprovalCommitted,
		resultIsError:         pending.resultIsError,
		actualResultBytes:     pending.actualResultBytes,
		upstreamProtocol:      pending.upstreamProtocol,
		repositoryPaths:       cloneRepositoryPathBindings(pending.repositoryPaths),
	}
}

func (g *Gateway) failPendingApproval(
	ctx context.Context,
	pending pendingApproval,
	status actionapproval.Status,
	reason action.ReasonCode,
) (*mcp.CallToolResult, error) {
	g.transitionMu.Lock()
	defer g.transitionMu.Unlock()
	call := callFromPending(pending)
	result := g.finalizePendingApproval(ctx, pending, status)
	if pending.phase == action.PhasePostResult {
		return g.finalizePostApproval(ctx, call, result, reason)
	}
	g.recordTerminalizedApproval(ctx, call, result, approvalLedgerReason(status), false)
	return blockedGatewayResult(pending.callID, reason)
}

func (g *Gateway) finalizePendingApproval(
	ctx context.Context,
	pending pendingApproval,
	status actionapproval.Status,
) actionstate.ApprovalConsumeResult {
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	result, _ := g.state.FinalizeApproval(terminalCtx, actionstate.ApprovalFinalizeRequest{
		RequestState:         pending.requestState,
		ExpectedStateVersion: pending.issuanceVersion,
		Status:               status,
	})
	return result
}

func approvalFailureFinalizeStatus(err error) actionapproval.Status {
	status := approvalStatus(err)
	if status == actionapproval.StatusRejected {
		return actionapproval.StatusCancelled
	}
	if status == actionapproval.StatusApproved || status == actionapproval.StatusPending {
		return actionapproval.StatusUnavailable
	}
	return status
}

func (g *Gateway) blockApprovalLedgerFailure(
	ctx context.Context,
	call *gatewayCall,
	cause error,
) (*mcp.CallToolResult, error) {
	if call == nil {
		err := errors.Join(cause, errors.New("approval ledger failure call is unavailable"))
		g.diagnostic("approval ledger failure could not be terminalized: " + err.Error())
		return nil, err
	}
	if call.reservation != nil {
		if _, transitionErr := g.markIndeterminateAfterFailure(ctx, call, cause); transitionErr != nil {
			return blockedGatewayResult(
				call.callID,
				gatewayReason(transitionErr, action.ReasonReservationIndeterminate),
			)
		}
	}
	return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
}

func pendingRegistry(g *Gateway) actionstate.LoadedApprovalRegistry {
	return g.registry
}

func (g *Gateway) terminalizeApprovedReservation(
	ctx context.Context,
	call *gatewayCall,
	stateVersion string,
) {
	call.stateVersion = stateVersion
	call.approvalCommitted = true
	g.denyCall(ctx, call, true)
}

func legacyPendingState(err error) (string, string, bool) {
	var pending *legacyApprovalPendingError
	if !errors.As(err, &pending) || pending == nil || pending.state == "" {
		return "", "", false
	}
	return pending.state, pending.callID, true
}
