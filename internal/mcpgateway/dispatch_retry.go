package mcpgateway

import (
	"context"
	"errors"
	"fmt"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionstate"
)

// markDispatchWithRetry never retries downstream IO. Only a typed CAS conflict
// on an unused, approval-free reservation can refresh its state-bound decision.
func (g *Gateway) markDispatchWithRetry(ctx context.Context, call *gatewayCall) (string, error) {
	for retries := 0; ; retries++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if retries > 0 {
			if err := g.refreshDispatchState(ctx, call); err != nil {
				if errors.Is(err, actionstate.ErrStateVersionChanged) && retries < MaxReservationConflictRetries {
					continue
				}
				return "", err
			}
		}
		if err := g.resampleCallBoundary(ctx, call.snapshot, call.contract, call.generation, call.repositoryPaths); err != nil {
			return "", err
		}
		if call.reservation == nil {
			return call.stateVersion, nil
		}
		version, err := g.state.MarkDispatched(ctx, call.reservation.Identity, call.stateVersion)
		if err == nil || !errors.Is(err, actionstate.ErrStateVersionChanged) ||
			retries >= MaxReservationConflictRetries || !approvalFreeDispatch(call) {
			return version, err
		}
	}
}

func approvalFreeDispatch(call *gatewayCall) bool {
	return !call.approvalReserved && !call.approvalCommitted &&
		call.evaluation.Approval.Status == action.ApprovalNone &&
		(call.decision.Decision == action.DecisionAllow || call.decision.Decision == action.DecisionWarn)
}

func (g *Gateway) refreshDispatchState(ctx context.Context, call *gatewayCall) error {
	version, err := g.state.CurrentStateVersion(ctx)
	if err != nil {
		return err
	}
	request := call.preRequest
	request.StateVersion = version
	reserved, err := g.state.Reserve(ctx, actionstate.ReserveRequest{
		Plan: call.snapshot.Plan, Request: request, Context: g.boundContext,
		Authority: g.config.PolicyAuthority, Server: g.server,
	})
	if err != nil {
		return err
	}
	if reserved.Reservation == nil || reserved.Reservation.Identity != call.reservation.Identity {
		return fmt.Errorf("dispatch retry lost its exact reservation")
	}
	request.StateVersion = reserved.Snapshot.StateVersion
	input := call.evaluation
	input.Request, input.Budget = request, reserved.Snapshot
	input.ResampledIdentities = call.snapshot.Evaluator.IdentitySnapshot(input)
	decision, _ := g.evaluate(ctx, call.snapshot.Evaluator, input)
	call.request, call.preRequest, call.evaluation = request, request, input
	call.budget, call.reservation, call.stateVersion = reserved.Snapshot, reserved.Reservation, reserved.Snapshot.StateVersion
	call.decision = decision
	call.ledger.setResult(request)
	if decision.Failure != nil {
		return &action.RequestError{Code: decision.Failure.Code, Message: "dispatch retry evaluation failed closed"}
	}
	if decision.Decision != action.DecisionAllow && decision.Decision != action.DecisionWarn {
		return &action.RequestError{Code: decision.Reason, Message: "dispatch retry is no longer approval-free and permitted"}
	}
	return nil
}
