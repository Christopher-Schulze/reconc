package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

type shutdownOrderRecorder struct {
	mu      sync.Mutex
	once    sync.Once
	done    chan struct{}
	process *ownedProcess
	order   []string
}

func (r *shutdownOrderRecorder) finish(label string, waitErr error) {
	r.mu.Lock()
	r.order = append(r.order, label)
	r.mu.Unlock()
	r.once.Do(func() {
		r.process.waitMu.Lock()
		if waitErr != nil {
			r.process.waitErr = waitErr
		}
		r.process.exited = true
		r.process.waitMu.Unlock()
		close(r.done)
	})
}

type shutdownPipe struct{ recorder *shutdownOrderRecorder }

func (*shutdownPipe) Write(body []byte) (int, error) { return len(body), nil }

func (p *shutdownPipe) Close() error {
	p.recorder.finish("process", nil)
	return nil
}

type shutdownBoundary struct{}

func (*shutdownBoundary) Attach(*os.Process) error { return nil }
func (*shutdownBoundary) Terminate() error         { return nil }
func (*shutdownBoundary) Kill() error              { return nil }
func (*shutdownBoundary) Close() error             { return nil }

type shutdownDownstream struct{ recorder *shutdownOrderRecorder }

func (*shutdownDownstream) ProtocolVersion() string { return gatewayProtocolCurrent }
func (*shutdownDownstream) ListTools(context.Context, string) (ToolPage, error) {
	return ToolPage{}, nil
}
func (*shutdownDownstream) CallTool(
	context.Context,
	string,
	json.RawMessage,
	ProgressSink,
) (CallResult, error) {
	return CallResult{}, nil
}
func (d *shutdownDownstream) Close() error {
	d.recorder.finish("downstream", errors.New("child exited while the transport closed"))
	return nil
}
func (*shutdownDownstream) Wait() error { return nil }

func TestGatewayMarksProcessShutdownBeforeClosingDownstreamTransport(t *testing.T) {
	stderrDone := make(chan struct{})
	close(stderrDone)
	recorder := &shutdownOrderRecorder{done: make(chan struct{})}
	process := &ownedProcess{
		stdin: &shutdownPipe{recorder: recorder}, boundary: &shutdownBoundary{},
		done: recorder.done, stderrDone: stderrDone,
	}
	recorder.process = process
	ctx, cancel := context.WithCancel(context.Background())
	gateway := &Gateway{
		ctx: ctx, cancel: cancel, process: process,
		downstream: &shutdownDownstream{recorder: recorder},
		pending:    make(map[string]pendingApproval),
		semaphore:  make(chan struct{}, MaxConcurrentCalls),
	}
	if err := gateway.Close(); err != nil {
		t.Fatalf("gateway shutdown misclassified its child exit: %v", err)
	}
	recorder.mu.Lock()
	order := append([]string(nil), recorder.order...)
	recorder.mu.Unlock()
	if len(order) != 2 || order[0] != "downstream" || order[1] != "process" {
		t.Fatalf("shutdown order = %#v", order)
	}
}

func TestOwnedProcessStillReportsExitObservedBeforeShutdown(t *testing.T) {
	done := make(chan struct{})
	close(done)
	stderrDone := make(chan struct{})
	close(stderrDone)
	process := &ownedProcess{
		boundary: &shutdownBoundary{}, done: done, stderrDone: stderrDone,
		waitErr: errors.New("pre-existing child exit"), exited: true,
	}
	process.expectShutdown()
	if err := process.Close(); err == nil || !strings.Contains(err.Error(), "pre-existing child exit") {
		t.Fatalf("pre-existing child exit was not reported: %v", err)
	}
}

func TestCleanupProcessErrorIgnoresExpectedTerminationResults(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr bool
	}{
		{name: "nil"},
		{name: "already done", err: os.ErrProcessDone},
		{name: "terminated exit", err: &exec.ExitError{}},
		{name: "unexpected", err: errors.New("cleanup failed"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := cleanupProcessError("cleanup", test.err)
			if (err != nil) != test.wantErr {
				t.Fatalf("cleanupProcessError() = %v, want error %t", err, test.wantErr)
			}
		})
	}
}

func TestBoundedStderrRedactsAndBoundsFlood(t *testing.T) {
	secret := "api_key=Q7m9V2p4R8x6L3n5"
	body := secret + strings.Repeat("x", MaxStderrBytes*2)
	stderr := &boundedStderr{}
	stderr.drain(io.NopCloser(strings.NewReader(body)))
	if stderr.total != uint64(len(body)) || len(stderr.retained) != MaxStderrBytes {
		t.Fatalf("stderr bounds = total %d, retained %d", stderr.total, len(stderr.retained))
	}
	summary := stderr.summary(context.Background())
	if strings.Contains(summary, secret) || !strings.Contains(summary, "classifications=secret") ||
		!strings.Contains(summary, "retained=262144") {
		t.Fatalf("stderr summary = %q", summary)
	}
}

func TestBoundedStderrByteCountSaturates(t *testing.T) {
	stderr := &boundedStderr{total: math.MaxUint64 - 1}
	stderr.drain(io.NopCloser(strings.NewReader("overflow")))
	if stderr.total != math.MaxUint64 {
		t.Fatalf("stderr total = %d, want saturation", stderr.total)
	}
}
