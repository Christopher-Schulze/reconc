package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reconc.dev/reconc/internal/action"
)

type callProgress struct {
	mu              sync.Mutex
	token           any
	queue           chan ProgressEvent
	done            chan struct{}
	startOnce       sync.Once
	finishOnce      sync.Once
	events          uint64
	bytes           uint64
	inspectionTime  time.Duration
	lastProgress    action.Decimal
	hasLastProgress bool
	stopped         bool
	closed          bool
	workerErr       error
	terminalReason  action.ReasonCode
	failureRecorded bool
	cloneParams     func([]byte) []byte
}

type normalizedProgress struct {
	payload       json.RawMessage
	message       string
	progress      float64
	total         float64
	progressExact action.Decimal
}

func newCallProgress(params json.RawMessage) (*callProgress, error) {
	token, present, err := upstreamProgressToken(params)
	if err != nil || !present {
		return nil, err
	}
	return &callProgress{token: token}, nil
}

func upstreamProgressToken(params json.RawMessage) (any, bool, error) {
	value, err := action.ParseObjectJSON(params)
	if err != nil {
		return nil, false, fmt.Errorf("decode upstream progress metadata: %w", err)
	}
	meta, present := value.Lookup("_meta")
	if !present {
		return nil, false, nil
	}
	if meta.Kind() != action.ValueObject {
		return nil, false, fmt.Errorf("upstream request metadata is not an object")
	}
	token, present := meta.Lookup("progressToken")
	if !present {
		return nil, false, nil
	}
	if text, ok := token.Text(); ok {
		if len(text) > MaxProgressTokenBytes {
			return nil, false, fmt.Errorf("upstream progress token exceeds %d bytes", MaxProgressTokenBytes)
		}
		return text, true, nil
	}
	if token.Kind() != action.ValueNumber {
		return nil, false, fmt.Errorf("upstream progress token is not a string or integer")
	}
	body, err := token.MarshalJSON()
	if err != nil {
		return nil, false, fmt.Errorf("canonicalize upstream progress token: %w", err)
	}
	integer, err := strconv.ParseInt(string(body), 10, 64)
	if err != nil {
		return nil, false, fmt.Errorf("upstream numeric progress token is not a signed 64-bit integer")
	}
	return integer, true, nil
}

func (g *Gateway) progressSink(ctx context.Context, call *gatewayCall) ProgressSink {
	if call == nil || call.progress == nil {
		return nil
	}
	call.progress.start(ctx, func(event ProgressEvent) error {
		return g.handleProgress(ctx, call, event)
	})
	return func(_ context.Context, event ProgressEvent) error {
		return call.progress.enqueue(ctx, event)
	}
}

func (p *callProgress) start(ctx context.Context, handle func(ProgressEvent) error) {
	p.startOnce.Do(func() {
		p.queue = make(chan ProgressEvent, MaxProgressQueueEvents)
		p.done = make(chan struct{})
		go func() {
			defer close(p.done)
			for {
				select {
				case <-ctx.Done():
					p.setWorkerError(ctx.Err(), false)
					return
				case event, ok := <-p.queue:
					if !ok {
						if err := ctx.Err(); err != nil {
							p.setWorkerError(err, false)
						}
						return
					}
					if err := ctx.Err(); err != nil {
						p.setWorkerError(err, false)
						return
					}
					if err := handle(event); err != nil {
						p.setWorkerError(err, true)
						return
					}
				}
			}
		}()
	})
}

func (p *callProgress) enqueue(ctx context.Context, event ProgressEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	admitted, reason := p.prepareLocked(ctx, event)
	if reason != "" {
		p.setTerminalReasonLocked(reason)
		return fmt.Errorf("suppress downstream progress: %s", reason)
	}
	select {
	case <-ctx.Done():
		p.stopped = true
		reason := gatewayReason(ctx.Err(), action.ReasonCancelled)
		p.setTerminalReasonLocked(reason)
		return fmt.Errorf("suppress downstream progress: %s", reason)
	case p.queue <- admitted:
		return nil
	default:
		p.stopped = true
		p.setTerminalReasonLocked(action.ReasonLimitExceeded)
		return fmt.Errorf("suppress downstream progress: %s", action.ReasonLimitExceeded)
	}
}

func (p *callProgress) prepare(ctx context.Context, event ProgressEvent) (ProgressEvent, action.ReasonCode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.prepareLocked(ctx, event)
}

func (p *callProgress) prepareLocked(ctx context.Context, event ProgressEvent) (ProgressEvent, action.ReasonCode) {
	if reason := p.admitLocked(ctx, event); reason != "" {
		return ProgressEvent{}, reason
	}
	cloneParams := p.cloneParams
	if cloneParams == nil {
		cloneParams = bytes.Clone
	}
	event.Params = cloneParams(event.Params)
	return event, ""
}

func (p *callProgress) finish() (action.ReasonCode, bool, error) {
	if p == nil || p.done == nil {
		return "", false, nil
	}
	p.finishOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		close(p.queue)
		p.mu.Unlock()
	})
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.terminalReason, p.failureRecorded, p.workerErr
}

func (p *callProgress) setWorkerError(err error, failureRecorded bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.workerErr == nil {
		p.workerErr = err
	}
	p.failureRecorded = p.failureRecorded || failureRecorded
	p.stopped = true
}

func (p *callProgress) setTerminalReasonLocked(reason action.ReasonCode) {
	if !p.failureRecorded && p.terminalReason == "" {
		p.terminalReason = reason
	}
}

func (g *Gateway) finishProgress(call *gatewayCall) error {
	if call == nil || call.progress == nil {
		return nil
	}
	reason, failureRecorded, workerErr := call.progress.finish()
	if reason == "" || failureRecorded {
		return workerErr
	}
	terminalCtx, cancel := terminalContext(g.ctx)
	defer cancel()
	return errors.Join(workerErr, g.recordProgressFailure(terminalCtx, call, reason))
}

func (g *Gateway) handleProgress(
	ctx context.Context,
	call *gatewayCall,
	event ProgressEvent,
) error {
	started := time.Now()
	progress, err := normalizeProgress(event.Params)
	if err != nil {
		call.progress.stop()
		return errors.Join(
			err,
			g.recordProgressFailure(ctx, call, action.ReasonProtocolError),
		)
	}
	if !call.progress.advance(progress.progressExact) {
		return errors.Join(
			fmt.Errorf("downstream progress did not strictly increase"),
			g.recordProgressFailure(ctx, call, action.ReasonProtocolError),
		)
	}
	decision, cached := g.evaluateProgress(ctx, call, progress.payload)
	if reason := call.progress.charge(time.Since(started)); reason != "" {
		return errors.Join(
			fmt.Errorf("suppress downstream progress: %s", reason),
			g.recordProgressFailure(ctx, call, reason),
		)
	}
	if err := call.ledger.progressDecision(ctx, decision, cached); err != nil {
		call.progress.stop()
		return fmt.Errorf("record progress decision: %w", err)
	}
	if decision.Decision != action.DecisionAllow && decision.Decision != action.DecisionWarn ||
		decision.Failure != nil {
		if err := call.ledger.progressSuppressed(
			ctx, decision, 0, 0,
		); err != nil {
			call.progress.stop()
			return fmt.Errorf("record suppressed progress: %w", err)
		}
		return nil
	}
	session := g.upstreamSession()
	if session == nil {
		call.progress.stop()
		return errors.Join(
			fmt.Errorf("upstream MCP session is unavailable"),
			g.recordProgressFailure(ctx, call, action.ReasonProtocolError),
		)
	}
	if err := session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
		ProgressToken: call.progress.token,
		Message:       progress.message,
		Progress:      progress.progress,
		Total:         progress.total,
	}); err != nil {
		call.progress.stop()
		return errors.Join(
			fmt.Errorf("forward inspected progress: %w", err),
			g.recordProgressFailure(ctx, call, gatewayReason(err, action.ReasonProtocolError)),
		)
	}
	return nil
}

func (g *Gateway) evaluateProgress(
	ctx context.Context,
	call *gatewayCall,
	payload json.RawMessage,
) (action.EvaluationResult, bool) {
	inspectionCtx, cancel := context.WithTimeout(ctx, ProgressEventTimeout)
	defer cancel()
	request, err := g.progressRequest(inspectionCtx, call, payload)
	if err != nil {
		return progressFailureDecision(call, gatewayReason(err, action.ReasonPolicyStale)), false
	}
	tool, _, err := call.snapshot.Plan.BudgetContract(request)
	if err != nil {
		return progressFailureDecision(call, action.ReasonPolicyMissing), false
	}
	evidence, err := g.evidence(inspectionCtx, call.snapshot, call.preRequest, tool)
	if err != nil {
		return progressFailureDecision(call, action.ReasonInspectionIncomplete), false
	}
	inspector, err := g.inspectionEngine(call.snapshot.Plan)
	if err != nil {
		return progressFailureDecision(call, action.ReasonInspectionIncomplete), false
	}
	inspection, err := inspector.Inspect(inspectionCtx, request, nil, nil)
	if err != nil {
		return progressFailureDecision(
			call,
			inspectionFailureReason(gatewayReason(err, action.ReasonInspectionIncomplete)),
		), false
	}
	input := g.evaluationInput(
		call.snapshot,
		request,
		action.BudgetSnapshot{},
		action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
		evidence,
	)
	input.Inspection = inspection
	input.ResampledIdentities = call.snapshot.Evaluator.IdentitySnapshot(input)
	decision, cached := g.evaluate(call.snapshot.Evaluator, input)
	if inspectionCtx.Err() != nil {
		return progressFailureDecision(call, action.ReasonDeadlineExceeded), false
	}
	return decision, cached
}

func (g *Gateway) progressRequest(
	ctx context.Context,
	call *gatewayCall,
	payload json.RawMessage,
) (action.Request, error) {
	if err := g.resampleCallBoundary(
		ctx, call.snapshot, call.contract, call.generation, call.repositoryPaths,
	); err != nil {
		return action.Request{}, err
	}
	return g.normalizedRequest(
		call.snapshot,
		call.contract,
		call.callID,
		call.stateVersion,
		action.PhaseProgress,
		payload,
	)
}

func normalizeProgress(raw json.RawMessage) (normalizedProgress, error) {
	params, err := action.ParseObjectJSON(raw)
	if err != nil {
		return normalizedProgress{}, fmt.Errorf("decode downstream progress: %w", err)
	}
	members, _ := params.Members()
	filtered := make([]action.Member, 0, 3)
	var output normalizedProgress
	var progressSeen bool
	var tokenSeen bool
	zero := action.ZeroDecimal()
	for _, member := range members {
		switch member.Name {
		case "_meta":
			if member.Value.Kind() != action.ValueObject {
				return normalizedProgress{}, fmt.Errorf("downstream progress metadata is not an object")
			}
		case "progressToken":
			if _, ok := member.Value.Text(); !ok {
				return normalizedProgress{}, fmt.Errorf("downstream progress token is not a string")
			}
			tokenSeen = true
		case "message":
			message, ok := member.Value.Text()
			if !ok {
				return normalizedProgress{}, fmt.Errorf("downstream progress message is not a string")
			}
			output.message = message
			filtered = append(filtered, member)
		case "progress":
			decimal, ok := member.Value.Decimal()
			if !ok || decimal.Compare(zero) < 0 {
				return normalizedProgress{}, fmt.Errorf("downstream progress value is invalid")
			}
			output.progressExact = decimal
			output.progress, err = progressFloat(decimal)
			if err != nil {
				return normalizedProgress{}, err
			}
			progressSeen = true
			filtered = append(filtered, member)
		case "total":
			decimal, ok := member.Value.Decimal()
			if !ok || decimal.Compare(zero) < 0 {
				return normalizedProgress{}, fmt.Errorf("downstream progress total is invalid")
			}
			output.total, err = progressFloat(decimal)
			if err != nil {
				return normalizedProgress{}, err
			}
			filtered = append(filtered, member)
		default:
			return normalizedProgress{}, fmt.Errorf("downstream progress contains unsupported field %q", member.Name)
		}
	}
	if !tokenSeen || !progressSeen {
		return normalizedProgress{}, fmt.Errorf("downstream progress omitted progress")
	}
	value, err := action.Object(filtered)
	if err != nil {
		return normalizedProgress{}, err
	}
	output.payload, err = value.MarshalJSON()
	if err != nil {
		return normalizedProgress{}, err
	}
	if len(output.payload) > MaxProgressEventBytes {
		return normalizedProgress{}, fmt.Errorf("downstream progress payload exceeds %d bytes", MaxProgressEventBytes)
	}
	return output, nil
}

func progressFloat(decimal action.Decimal) (float64, error) {
	value, err := strconv.ParseFloat(decimal.String(), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("downstream progress number is outside finite float64")
	}
	return value, nil
}

func (p *callProgress) admit(ctx context.Context, event ProgressEvent) action.ReasonCode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.admitLocked(ctx, event)
}

func (p *callProgress) admitLocked(ctx context.Context, event ProgressEvent) action.ReasonCode {
	if p.stopped || p.closed {
		return action.ReasonLimitExceeded
	}
	if err := ctx.Err(); err != nil {
		p.stopped = true
		return gatewayReason(err, action.ReasonCancelled)
	}
	p.events++
	if p.events > MaxProgressEvents || event.FrameBytes == 0 ||
		event.FrameBytes > MaxProgressEventBytes || p.bytes > MaxProgressBytes-event.FrameBytes {
		p.stopped = true
		return action.ReasonLimitExceeded
	}
	p.bytes += event.FrameBytes
	return ""
}

func (p *callProgress) advance(progress action.Decimal) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.hasLastProgress && progress.Compare(p.lastProgress) <= 0 {
		p.stopped = true
		return false
	}
	p.lastProgress = progress
	p.hasLastProgress = true
	return true
}

func (p *callProgress) charge(duration time.Duration) action.ReasonCode {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return action.ReasonLimitExceeded
	}
	p.inspectionTime += duration
	if duration > ProgressEventTimeout || p.inspectionTime > ProgressTotalTimeout {
		p.stopped = true
		return action.ReasonDeadlineExceeded
	}
	return ""
}

func (p *callProgress) stop() {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
}

func (g *Gateway) recordProgressFailure(
	ctx context.Context,
	call *gatewayCall,
	reason action.ReasonCode,
) error {
	decision := progressFailureDecision(call, reason)
	if err := call.ledger.progressDecision(ctx, decision, false); err != nil {
		return err
	}
	return call.ledger.progressSuppressed(ctx, decision, 0, 0)
}

func progressFailureDecision(
	call *gatewayCall,
	reason action.ReasonCode,
) action.EvaluationResult {
	decision := blockDecision(call.decision, reason)
	decision.PhaseOutcome = action.OutcomeSuppressed
	decision.Completeness = phaseIncomplete(decision.Completeness, reason)
	return decision
}
