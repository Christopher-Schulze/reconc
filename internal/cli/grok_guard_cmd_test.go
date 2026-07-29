package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestGrokPreToolGuardPassesExactRuntimeDecision(t *testing.T) {
	originalRuntime := grokPreToolGuardRuntime
	originalTimeout := grokPreToolGuardTimeout
	defer func() {
		grokPreToolGuardRuntime = originalRuntime
		grokPreToolGuardTimeout = originalTimeout
	}()
	grokPreToolGuardRuntime = func(_ context.Context, args []string, stdout, stderr io.Writer) error {
		if len(args) != 2 || args[0] != "grok-pre-tool-use" || args[1] != "." {
			t.Fatalf("guarded runtime args = %v", args)
		}
		_, _ = io.WriteString(stdout, `{"decision":"allow"}`+"\n")
		_, _ = io.WriteString(stderr, "diagnostic\n")
		return nil
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "grok-pre-tool-guard", "."}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != `{"decision":"allow"}`+"\n" || stderr.String() != "diagnostic\n" {
		t.Fatalf("guard passthrough stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestGrokPreToolGuardTimesOutWithExplicitDeny(t *testing.T) {
	originalRuntime := grokPreToolGuardRuntime
	originalTimeout := grokPreToolGuardTimeout
	defer func() {
		grokPreToolGuardRuntime = originalRuntime
		grokPreToolGuardTimeout = originalTimeout
	}()
	cancelled := make(chan struct{})
	grokPreToolGuardRuntime = func(ctx context.Context, _ []string, _, _ io.Writer) error {
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}
	grokPreToolGuardTimeout = 10 * time.Millisecond
	var stdout, stderr bytes.Buffer
	err := Run([]string{"hook", "grok-pre-tool-guard", "."}, "test", &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("timed-out runtime did not observe cancellation")
	}
	var decision map[string]string
	if decodeErr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &decision); decodeErr != nil {
		t.Fatalf("timeout decision JSON: %v\n%s", decodeErr, stdout.String())
	}
	if decision["decision"] != "deny" || !strings.Contains(decision["reason"], "timed out") ||
		!strings.Contains(stderr.String(), "denied before Grok's host timeout") {
		t.Fatalf("timeout guard stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
