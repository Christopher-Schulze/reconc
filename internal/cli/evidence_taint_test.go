package cli

import (
	"bytes"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestPolicyPassAndCICannotBypassEvidenceTaint(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	if result := agentsession.RunSessionStart(repo, []byte(`{"session_id":"tainted-cli"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	// A claim beyond its per-item bound is a real overflow that cannot be
	// retained; long commands are bounded instead and never taint.
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "claim", repo, strings.Repeat("x", 8*1024)}, "test", &stdout, &stderr); err == nil {
		t.Fatal("oversized claim was recorded without overflow")
	}

	stdout.Reset()
	stderr.Reset()
	err := Run([]string{"check", repo}, "test", &stdout, &stderr)
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "persisted evidence is uncertified at claims/item_bytes") {
		t.Fatalf("check manufactured a policy pass: err=%v stdout=%s", err, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = Run([]string{"ci", repo}, "test", &stdout, &stderr)
	if ExitCode(err) != 2 || !strings.Contains(err.Error(), "persisted evidence is uncertified at claims/item_bytes") {
		t.Fatalf("ci bypassed evidence taint: err=%v stdout=%s", err, stdout.String())
	}
}
