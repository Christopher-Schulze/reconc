package actionapproval

import (
	"context"
	"errors"
	"fmt"
	"time"

	"reconc.dev/reconc/internal/action"
)

// Provider is a transport-neutral authority boundary. Implementations own
// their external UI or process and must stop promptly when ctx is cancelled.
type Provider interface {
	RequestApproval(ctx context.Context, request Request) ([]byte, error)
}

func RequestFromProvider(
	ctx context.Context,
	provider Provider,
	request Request,
	timeout time.Duration,
) ([]byte, error) {
	if ctx == nil || provider == nil || request.Validate() != nil {
		return nil, approvalError(action.ReasonAuthorityUnavailable, "approval provider input is unavailable", nil)
	}
	if timeout <= 0 || timeout > DefaultApprovalWaitTimeout {
		return nil, approvalError(action.ReasonAuthorityUnavailable, "approval provider timeout is invalid", nil)
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := waitCtx.Err(); err != nil {
		return nil, approvalError(action.ReasonCancelled, "approval request was cancelled", err)
	}
	body, err := provider.RequestApproval(waitCtx, cloneRequest(request))
	if contextErr := waitCtx.Err(); contextErr != nil {
		if errors.Is(contextErr, context.Canceled) {
			return nil, approvalError(action.ReasonCancelled, "approval request was cancelled", contextErr)
		}
		return nil, approvalError(action.ReasonAuthorityUnavailable, "approval authority timed out", contextErr)
	}
	if err != nil {
		return nil, approvalError(action.ReasonAuthorityUnavailable, "approval authority failed", err)
	}
	if len(body) == 0 || len(body) > MaxApprovalObjectBytes {
		return nil, approvalError(
			action.ReasonApprovalInvalid,
			fmt.Sprintf("approval authority response must contain 1 to %d bytes", MaxApprovalObjectBytes),
			nil,
		)
	}
	return append([]byte(nil), body...), nil
}
