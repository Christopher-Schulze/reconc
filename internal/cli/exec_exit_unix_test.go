//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestExecSignalExitStatusMatchesRecordedEvidence(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	t.Setenv(agentsession.StateRootEnv, filepath.Join(t.TempDir(), "state"))
	if _, err := agentsession.InitializeSessionState(repo, "exec-signal"); err != nil {
		t.Fatalf("session start: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{"exec", repo, "--shell", "--", "kill -TERM $$"}, "0.9.7-test", &stdout, &stderr)
	if ExitCode(err) != 143 || !strings.Contains(err.Error(), "exit code 143") {
		t.Fatalf("signal exit = %d, err=%v; want 143", ExitCode(err), err)
	}
	evidence, evidenceErr := agentsession.ActiveEvidence(repo)
	if evidenceErr != nil {
		t.Fatal(evidenceErr)
	}
	if len(evidence.CommandResults) != 1 {
		t.Fatalf("command evidence = %+v", evidence.CommandResults)
	}
	result := evidence.CommandResults[0]
	if result.ExitCode == nil || *result.ExitCode != 143 || result.Outcome != "failure" {
		t.Fatalf("recorded signal evidence = %+v; want failure/143", result)
	}
}
