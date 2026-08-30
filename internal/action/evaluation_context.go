package action

import (
	"context"
	"errors"
	"time"
)

const evaluationCollectionPollInterval = 8

type evaluationControl struct {
	ctx         context.Context
	done        <-chan struct{}
	deadline    time.Time
	hasDeadline bool
	now         func() time.Time
	poll        func() ReasonCode
}

func newEvaluationControl(ctx context.Context) evaluationControl {
	if ctx == nil || ctx.Done() == nil {
		return evaluationControl{}
	}
	deadline, hasDeadline := ctx.Deadline()
	return evaluationControl{
		ctx: ctx, done: ctx.Done(), deadline: deadline, hasDeadline: hasDeadline,
	}
}

func (c *evaluationControl) stopReason() ReasonCode {
	if c == nil {
		return ""
	}
	if c.poll != nil {
		return c.poll()
	}
	if c.hasDeadline {
		now := time.Now()
		if c.now != nil {
			now = c.now()
		}
		if !now.Before(c.deadline) {
			return ReasonDeadlineExceeded
		}
	}
	if c.done == nil {
		return ""
	}
	select {
	case <-c.done:
	default:
		return ""
	}
	if c.ctx != nil && errors.Is(c.ctx.Err(), context.DeadlineExceeded) {
		return ReasonDeadlineExceeded
	}
	return ReasonCancelled
}

func evaluationStopped(reason ReasonCode) bool {
	return reason == ReasonDeadlineExceeded || reason == ReasonCancelled
}
