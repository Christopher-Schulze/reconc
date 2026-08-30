package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actioninspect"
	"reconc.dev/reconc/internal/actionledger"
	"reconc.dev/reconc/internal/actionstate"
)

func (g *Gateway) executeCall(
	ctx context.Context,
	call *gatewayCall,
) (*mcp.CallToolResult, error) {
	result, err := g.downstream.CallTool(
		ctx,
		call.contract.Name,
		call.canonicalArguments,
		g.progressSink(ctx, call),
	)
	if err != nil {
		return g.failUnknownDownstream(ctx, call, err)
	}
	if progressErr := g.finishProgress(call); progressErr != nil {
		g.diagnostic("progress pipeline failed: " + progressErr.Error())
	}
	response, finishErr := g.finishCall(ctx, call, result)
	if state, callID, pending := legacyPendingState(finishErr); pending {
		return g.elicitLegacyApproval(ctx, state, callID, call.progress)
	}
	return response, finishErr
}

func (g *Gateway) failUnknownDownstream(
	ctx context.Context,
	call *gatewayCall,
	cause error,
) (*mcp.CallToolResult, error) {
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	ctx = terminalCtx
	if progressErr := g.finishProgress(call); progressErr != nil {
		g.diagnostic("progress pipeline failed: " + progressErr.Error())
	}
	reason := gatewayReason(cause, action.ReasonDownstreamUnknown)
	if reason != action.ReasonCancelled && reason != action.ReasonDeadlineExceeded &&
		reason != action.ReasonShutdown {
		reason = action.ReasonDownstreamUnknown
	}
	failure := postFailureDecision(call, reason)
	if call.reservation == nil {
		if err := call.ledger.downstream(ctx, failure, actionledger.DownstreamUnknown); err != nil {
			return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
		}
		if err := call.ledger.terminalFailure(
			ctx, action.PhasePostResult, reason, action.LifecycleActive, true, true,
		); err != nil {
			return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
		}
	} else {
		version, transitionErr := g.markIndeterminateAfterFailure(ctx, call, cause)
		if err := call.ledger.downstream(ctx, failure, actionledger.DownstreamUnknown); err != nil {
			if transitionErr != nil {
				g.diagnostic("record downstream failure after unresolved reservation: " + errors.Join(transitionErr, err).Error())
			}
			return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
		}
		if transitionErr != nil {
			return blockedGatewayResult(
				call.callID,
				gatewayReason(transitionErr, action.ReasonReservationIndeterminate),
			)
		}
		if err := call.ledger.budget(
			ctx,
			blockDecision(call.decision, action.ReasonReservationIndeterminate),
			actionledger.BudgetIndeterminate,
			call.budget,
			version,
			0,
			false,
			false,
		); err != nil {
			return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
		}
	}
	if summary := g.process.StderrSummary(ctx); summary != "" {
		g.diagnostic(summary)
	}
	return blockedGatewayResult(call.callID, reason)
}

func (g *Gateway) finishCall(
	ctx context.Context,
	call *gatewayCall,
	downstream CallResult,
) (*mcp.CallToolResult, error) {
	decoded, decodeErr := actioninspect.DecodeMCPToolResult(
		downstream.Canonical,
		downstream.Protocol,
	)
	if decodeErr != nil {
		return g.failMalformedDownstream(ctx, call, downstream.Canonical)
	}
	defer decoded.Release()
	actualBytes := uint64(len(downstream.Canonical))
	call.resultIsError = decoded.IsError
	call.actualResultBytes = actualBytes
	stateVersion, stateErr := g.state.CurrentStateVersion(ctx)
	if stateErr != nil {
		stateVersion = call.stateVersion
	}
	postRequest, normalizeErr := g.normalizedRequest(
		call.snapshot,
		call.contract,
		call.callID,
		stateVersion,
		action.PhasePostResult,
		downstream.Canonical,
	)
	if normalizeErr == nil {
		call.ledger.setResult(postRequest)
	}
	downstreamDecision := postSuccessDecision(call)
	if normalizeErr != nil {
		downstreamDecision = incompletePermittingDecision(
			downstreamDecision,
			gatewayReason(normalizeErr, action.ReasonLimitExceeded),
		)
	}
	if stateErr != nil {
		downstreamDecision = incompletePermittingDecision(
			downstreamDecision,
			gatewayReason(stateErr, action.ReasonStateUnavailable),
		)
	}
	call.downstreamDecision = downstreamDecision
	if err := call.ledger.downstream(ctx, downstreamDecision, actionledger.DownstreamSucceeded); err != nil {
		return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
	}
	if normalizeErr != nil {
		return g.withholdUnnormalizedPostFailure(
			ctx,
			call,
			gatewayReason(normalizeErr, action.ReasonLimitExceeded),
		)
	}
	if stateErr != nil {
		return g.withholdPostFailure(
			ctx, call, postRequest, gatewayReason(stateErr, action.ReasonStateUnavailable),
		)
	}
	return g.inspectAndDeliver(ctx, call, postRequest, decoded, downstream.Canonical)
}

func (g *Gateway) failMalformedDownstream(
	ctx context.Context,
	call *gatewayCall,
	raw json.RawMessage,
) (*mcp.CallToolResult, error) {
	actualBytes := uint64(len(raw))
	stateVersion, settleErr := g.settleCallState(ctx, call, true, actualBytes)
	var transitionErr error
	if settleErr != nil && call.reservation != nil && stateVersion == "" {
		stateVersion, transitionErr = g.markIndeterminateAfterFailure(ctx, call, settleErr)
	}
	failure := postFailureDecision(call, action.ReasonProtocolError)
	if err := call.ledger.downstream(ctx, failure, actionledger.DownstreamFailed); err != nil {
		if transitionErr != nil {
			g.diagnostic("record malformed downstream failure after unresolved reservation: " + errors.Join(transitionErr, err).Error())
		}
		return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
	}
	if transitionErr != nil {
		return blockedGatewayResult(
			call.callID,
			gatewayReason(transitionErr, action.ReasonReservationIndeterminate),
		)
	}
	if call.reservation != nil {
		kind := actionledger.BudgetSettled
		budgetDecision := failure
		if settleErr != nil {
			kind = actionledger.BudgetIndeterminate
			budgetDecision = postFailureDecision(call, action.ReasonReservationIndeterminate)
		}
		if err := call.ledger.budget(
			ctx, budgetDecision, kind, call.budget, stateVersion,
			actualBytes, false, false,
		); err != nil {
			return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
		}
	}
	return blockedGatewayResult(call.callID, action.ReasonProtocolError)
}

func (g *Gateway) settleCallState(
	ctx context.Context,
	call *gatewayCall,
	isError bool,
	actualBytes uint64,
) (string, error) {
	if call.reservation == nil {
		return g.state.CurrentStateVersion(ctx)
	}
	outcome := actionstate.OutcomeSucceeded
	if isError {
		outcome = actionstate.OutcomeFailed
	}
	version, err := g.state.Settle(
		ctx,
		call.reservation.Identity,
		call.stateVersion,
		outcome,
		actualBytes,
	)
	if err == nil {
		if version == "" {
			return "", errors.New("reservation settlement returned no state version")
		}
		return version, nil
	}
	if version != "" {
		return version, err
	}
	current, currentErr := g.state.CurrentStateVersion(ctx)
	if currentErr != nil {
		return "", errors.Join(err, currentErr)
	}
	version, retryErr := g.state.Settle(ctx, call.reservation.Identity, current, outcome, actualBytes)
	if retryErr == nil && version == "" {
		return "", errors.Join(err, errors.New("retried reservation settlement returned no state version"))
	}
	return version, retryErr
}

func (g *Gateway) settleAndRecord(
	ctx context.Context,
	call *gatewayCall,
) (action.ReasonCode, error) {
	version, err := g.settleCallState(
		ctx, call, call.resultIsError, call.actualResultBytes,
	)
	if err != nil {
		if call.reservation != nil && version == "" {
			settleErr := err
			var transitionErr error
			version, transitionErr = g.markIndeterminateAfterFailure(ctx, call, settleErr)
			if transitionErr != nil {
				return gatewayReason(transitionErr, action.ReasonReservationIndeterminate), transitionErr
			}
			err = settleErr
		}
		call.stateVersion = version
		if call.reservation != nil {
			ledgerErr := call.ledger.budget(
				ctx,
				postFailureDecision(call, action.ReasonReservationIndeterminate),
				actionledger.BudgetIndeterminate,
				call.budget,
				version,
				0,
				false,
				false,
			)
			if ledgerErr != nil {
				return action.ReasonLedgerUnavailable, errors.Join(err, ledgerErr)
			}
		}
		return gatewayReason(err, action.ReasonReservationIndeterminate), err
	}
	call.stateVersion = version
	if call.reservation == nil {
		return "", nil
	}
	err = call.ledger.budget(
		ctx,
		call.downstreamDecision,
		actionledger.BudgetSettled,
		call.budget,
		version,
		call.actualResultBytes,
		call.postApprovalReserved,
		call.postApprovalCommitted,
	)
	if err != nil {
		return action.ReasonLedgerUnavailable, err
	}
	return "", nil
}

func (g *Gateway) markIndeterminate(ctx context.Context, call *gatewayCall) (string, error) {
	if g == nil || g.state == nil || call == nil || call.reservation == nil {
		return "", errors.New("indeterminate reservation transition is unavailable")
	}
	version, err := g.state.MarkIndeterminate(
		ctx,
		call.reservation.Identity,
		call.stateVersion,
	)
	if version != "" {
		return version, err
	}
	if err == nil {
		return "", errors.New("indeterminate reservation transition returned no state version")
	}
	current, currentErr := g.state.CurrentStateVersion(ctx)
	if currentErr != nil {
		return "", errors.Join(err, currentErr)
	}
	version, retryErr := g.state.MarkIndeterminate(ctx, call.reservation.Identity, current)
	if version != "" {
		return version, retryErr
	}
	if retryErr != nil {
		return "", errors.Join(err, retryErr)
	}
	return "", errors.Join(err, errors.New("retried indeterminate reservation transition returned no state version"))
}

func (g *Gateway) markIndeterminateAfterFailure(
	ctx context.Context,
	call *gatewayCall,
	cause error,
) (string, error) {
	if cause == nil {
		cause = errors.New("gateway lifecycle failed before terminal reservation state")
	}
	expectedVersion := ""
	reservation := "unavailable"
	if call != nil {
		expectedVersion = call.stateVersion
		if call.reservation != nil {
			reservation = call.reservation.Identity
		}
	}
	version, err := g.markIndeterminate(ctx, call)
	if version != "" {
		call.stateVersion = version
		if err != nil {
			joined := errors.Join(
				cause,
				fmt.Errorf(
					"reservation %q committed indeterminate state version %q but transition finalization failed: %w",
					reservation,
					version,
					err,
				),
			)
			g.diagnostic("indeterminate reservation transition completed with an error: " + joined.Error())
			return version, joined
		}
		return version, nil
	}
	if err != nil {
		transitionErr := &actionstate.StateError{
			Code: action.ReasonReservationIndeterminate,
			Message: fmt.Sprintf(
				"reservation %q remains unresolved from state version %q",
				reservation,
				expectedVersion,
			),
			Cause: errors.Join(cause, err),
		}
		g.diagnostic("indeterminate reservation transition failed: " + transitionErr.Error())
		return "", transitionErr
	}
	return "", errors.New("indeterminate reservation transition returned no state version")
}

func (g *Gateway) inspectAndDeliver(
	ctx context.Context,
	call *gatewayCall,
	postRequest action.Request,
	decoded *actioninspect.MCPToolResult,
	raw json.RawMessage,
) (*mcp.CallToolResult, error) {
	tool, _, err := call.snapshot.Plan.BudgetContract(postRequest)
	if err != nil {
		return g.withholdPostFailure(ctx, call, postRequest, gatewayReason(err, action.ReasonPolicyMissing))
	}
	evidence, err := g.evidence(ctx, call.snapshot, call.preRequest, tool)
	if err != nil {
		return g.withholdPostFailure(ctx, call, postRequest, action.ReasonInspectionIncomplete)
	}
	inspection, schemaStatus, schemaErr := g.inspectResult(
		ctx,
		call,
		postRequest,
		decoded,
	)
	input := g.evaluationInput(
		call.snapshot,
		postRequest,
		action.BudgetSnapshot{},
		action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
		evidence,
	)
	input.Inspection = inspection
	input.ResampledIdentities = call.snapshot.Evaluator.IdentitySnapshot(input)
	decision, _ := g.evaluate(ctx, call.snapshot.Evaluator, input)
	ledgerEvidence := inspection
	if ledgerEvidence == nil {
		ledgerEvidence, err = cleanLedgerInspection(schemaStatus, decoded, raw)
		if err != nil {
			decision = gatewayFailureResult(input, action.ReasonLimitExceeded)
			ledgerEvidence = incompleteLedgerInspection(schemaStatus, raw)
		}
	}
	if schemaErr != nil {
		decision = gatewayFailureResult(input, action.ReasonSchemaInvalid)
		ledgerEvidence = incompleteLedgerInspection(schemaStatus, raw)
	}
	if err := call.ledger.inspection(ctx, decision, *ledgerEvidence); err != nil {
		return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
	}
	call.request = postRequest
	call.tool = tool
	call.evaluation = input
	call.decision = decision
	call.stateVersion = postRequest.StateVersion
	if decision.Decision == action.DecisionAllow || decision.Decision == action.DecisionWarn {
		return g.forwardResult(ctx, call, decision, raw)
	}
	if decision.Decision == action.DecisionRequireApproval {
		return g.requestPostApproval(ctx, call, raw)
	}
	return g.withholdResult(ctx, call, decision, ledgerEvidence)
}

func (g *Gateway) inspectResult(
	ctx context.Context,
	call *gatewayCall,
	request action.Request,
	decoded *actioninspect.MCPToolResult,
) (*action.InspectionEvidence, action.InspectionSchemaStatus, error) {
	schemaStatus := action.InspectionSchemaNotDeclared
	if call.contract.OutputSchema != nil {
		if !decoded.HasStructuredContent {
			return nil, action.InspectionSchemaRequired, errors.New("structured content is required")
		}
		if err := call.contract.OutputSchema.Validate(decoded.StructuredContent); err != nil {
			return nil, action.InspectionSchemaInvalid, err
		}
		schemaStatus = action.InspectionSchemaValid
	}
	inspector, err := g.inspectionEngine(call.snapshot.Plan)
	if err != nil {
		return nil, schemaStatus, err
	}
	evidence, err := inspector.Inspect(ctx, request, decoded, call.contract.OutputSchema)
	return evidence, schemaStatus, err
}

func cleanLedgerInspection(
	schema action.InspectionSchemaStatus,
	decoded *actioninspect.MCPToolResult,
	raw json.RawMessage,
) (*action.InspectionEvidence, error) {
	items, err := countValueItems(decoded.Root, 0)
	if err != nil {
		return nil, err
	}
	return &action.InspectionEvidence{
		Status: action.InspectionClean, RuleIDs: []string{}, Categories: []action.DetectorCategory{},
		PackIdentities: []string{}, SchemaStatus: schema, SchemaIdentity: "absent",
		Fields: []action.InspectionFieldEvidence{}, UnsupportedContent: []action.InspectionContentEvidence{},
		ScannedBytes: uint64(len(raw)), ScannedItems: items,
	}, nil
}

func incompleteLedgerInspection(
	schema action.InspectionSchemaStatus,
	raw json.RawMessage,
) *action.InspectionEvidence {
	return &action.InspectionEvidence{
		Status: action.InspectionIncomplete, Decision: action.DecisionBlock,
		Reason: action.ReasonSchemaInvalid, RuleIDs: []string{}, Categories: []action.DetectorCategory{},
		PackIdentities: []string{}, SchemaStatus: schema, SchemaIdentity: "absent",
		Fields: []action.InspectionFieldEvidence{}, UnsupportedContent: []action.InspectionContentEvidence{},
		ScannedBytes: uint64(len(raw)), ScannedItems: 0,
	}
}

func (g *Gateway) forwardResult(
	ctx context.Context,
	call *gatewayCall,
	decision action.EvaluationResult,
	raw json.RawMessage,
) (*mcp.CallToolResult, error) {
	value, err := action.ParseObjectJSON(raw)
	if err != nil {
		return g.withholdUnnormalizedPostFailure(ctx, call, action.ReasonProtocolError)
	}
	items, err := countValueItems(value, 0)
	if err != nil {
		return g.withholdUnnormalizedPostFailure(ctx, call, action.ReasonLimitExceeded)
	}
	if reason, settleErr := g.settleAndRecord(ctx, call); settleErr != nil {
		return blockedGatewayResult(call.callID, reason)
	}
	if err := call.ledger.delivery(
		ctx,
		decision,
		actionledger.DeliveryForwarded,
		uint64(len(raw)),
		items,
	); err != nil {
		return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
	}
	return callToolResult(raw)
}

func (g *Gateway) withholdResult(
	ctx context.Context,
	call *gatewayCall,
	decision action.EvaluationResult,
	evidence *action.InspectionEvidence,
) (*mcp.CallToolResult, error) {
	var response *mcp.CallToolResult
	var err error
	if evidence != nil && evidence.Status == action.InspectionMatched &&
		evidence.Decision == action.DecisionBlock {
		var body []byte
		body, err = actioninspect.WithheldMCPResult(call.callID, evidence)
		if err == nil {
			response, err = callToolResult(body)
		}
	} else {
		response = safeGatewayResult(
			"withheld", decision.Reason, "Reconc withheld the downstream tool result.",
			call.callID, "succeeded", "withheld",
		)
	}
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode withheld MCP result evidence: %w", err)
	}
	if reason, settleErr := g.settleAndRecord(ctx, call); settleErr != nil {
		return blockedGatewayResult(call.callID, reason)
	}
	if err := call.ledger.delivery(
		ctx,
		decision,
		actionledger.DeliveryWithheld,
		uint64(len(body)),
		0,
	); err != nil {
		return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
	}
	return response, nil
}

func (g *Gateway) withholdPostFailure(
	ctx context.Context,
	call *gatewayCall,
	request action.Request,
	reason action.ReasonCode,
) (*mcp.CallToolResult, error) {
	reason = inspectionFailureReason(reason)
	input := g.evaluationInput(
		call.snapshot,
		request,
		action.BudgetSnapshot{},
		call.evaluation.Approval,
		EvidenceSnapshot{Taint: action.TaintSnapshot{Status: action.TaintUnknown, Identity: "taint-unknown"}},
	)
	decision := gatewayFailureResult(input, reason)
	evidence := action.InspectionEvidence{
		Status: action.InspectionIncomplete, Decision: action.DecisionBlock, Reason: reason,
		RuleIDs: []string{}, Categories: []action.DetectorCategory{}, PackIdentities: []string{},
		SchemaStatus: postFailureSchemaStatus(call), SchemaIdentity: "absent",
		Fields: []action.InspectionFieldEvidence{}, UnsupportedContent: []action.InspectionContentEvidence{},
	}
	if err := call.ledger.inspection(ctx, decision, evidence); err != nil {
		return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
	}
	return g.withholdResult(ctx, call, decision, &evidence)
}

func (g *Gateway) withholdUnnormalizedPostFailure(
	ctx context.Context,
	call *gatewayCall,
	reason action.ReasonCode,
) (*mcp.CallToolResult, error) {
	reason = inspectionFailureReason(reason)
	decision := postFailureDecision(call, reason)
	evidence := action.InspectionEvidence{
		Status: action.InspectionIncomplete, Decision: action.DecisionBlock, Reason: reason,
		RuleIDs: []string{}, Categories: []action.DetectorCategory{}, PackIdentities: []string{},
		SchemaStatus: postFailureSchemaStatus(call), SchemaIdentity: "absent",
		Fields: []action.InspectionFieldEvidence{}, UnsupportedContent: []action.InspectionContentEvidence{},
	}
	if err := call.ledger.inspection(ctx, decision, evidence); err != nil {
		return blockedGatewayResult(call.callID, action.ReasonLedgerUnavailable)
	}
	return g.withholdResult(ctx, call, decision, &evidence)
}

func inspectionFailureReason(reason action.ReasonCode) action.ReasonCode {
	switch reason {
	case action.ReasonInspectionIncomplete, action.ReasonUnsupportedContent,
		action.ReasonSchemaInvalid, action.ReasonLimitExceeded, action.ReasonInvalidUTF8,
		action.ReasonCancelled, action.ReasonDeadlineExceeded:
		return reason
	default:
		return action.ReasonInspectionIncomplete
	}
}

func postFailureSchemaStatus(call *gatewayCall) action.InspectionSchemaStatus {
	if call != nil && call.contract.OutputSchema != nil {
		return action.InspectionSchemaRequired
	}
	return action.InspectionSchemaNotDeclared
}

func postSuccessDecision(call *gatewayCall) action.EvaluationResult {
	result := call.decision
	if result.Decision == action.DecisionRequireApproval && call.approvalCommitted {
		result.Decision = action.DecisionAllow
		result.Reason = action.ReasonRuleMatched
	}
	result.PhaseOutcome = action.OutcomeDeliveryEligible
	result.Failure = nil
	return result
}

func postFailureDecision(call *gatewayCall, reason action.ReasonCode) action.EvaluationResult {
	result := blockDecision(call.decision, reason)
	result.PhaseOutcome = action.OutcomeWithheld
	result.Completeness = phaseIncomplete(result.Completeness, reason)
	return result
}

func incompletePermittingDecision(
	result action.EvaluationResult,
	reason action.ReasonCode,
) action.EvaluationResult {
	result.Completeness = phaseIncomplete(result.Completeness, reason)
	return result
}
