package cli

import (
	"bytes"
	"errors"
	"testing"

	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestUnwrittenBlockingDecisionNeverExitsZero(t *testing.T) {
	blocking, ok := hooks.RuntimeEvent("cursor-pre-tool-use")
	if !ok || blocking.ErrorPolicy != hooks.FailureBlock {
		t.Fatalf("fixture route is not blocking: %+v", blocking)
	}
	observing, ok := hooks.RuntimeEvent("claude-post-tool-use")
	if !ok || observing.ErrorPolicy == hooks.FailureBlock {
		t.Fatalf("fixture route is blocking: %+v", observing)
	}
	deny := agentsession.Result{ExitCode: 0, Stdout: `{"permission":"deny"}`}
	var stderr bytes.Buffer
	if code := emitHookRuntimeDecision(blocking, deny, failingWriter{}, &stderr); code != 2 {
		t.Fatalf("lost blocking decision exited %d", code)
	}
	if code := emitHookRuntimeDecision(observing, deny, failingWriter{}, &stderr); code != 0 {
		t.Fatalf("observing route changed its exit code to %d", code)
	}
	var stdout bytes.Buffer
	if code := emitHookRuntimeDecision(blocking, deny, &stdout, &stderr); code != 0 || stdout.String() != deny.Stdout+"\n" {
		t.Fatalf("written decision = %d %q", code, stdout.String())
	}
}
