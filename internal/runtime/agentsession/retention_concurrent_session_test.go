package agentsession

import (
	"os"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/retention"
)

// A second agent session in the same checkout keeps working while another
// session is active. Routine retention under the default policy must not
// remove the older full-command objects its state still references, or the
// next load fails and taints the whole repository.
func TestRoutineRetentionKeepsConcurrentSessionCommandTexts(t *testing.T) {
	repo := setupPolicyRepo(t)
	stateDir := os.Getenv(StateRootEnv)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	appendLongCommands := func(session string, marker rune, count int) {
		t.Helper()
		for index := range count {
			command := "echo " + strings.Repeat(string(marker+rune(index)), 2<<20)
			if _, err := MutateSessionState(root, session, func(state SessionState) SessionState {
				return AppendCommand(state, command)
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := InitializeSessionState(root, "concurrent"); err != nil {
		t.Fatal(err)
	}
	appendLongCommands("concurrent", 'a', 3)
	time.Sleep(20 * time.Millisecond)
	if _, err := InitializeSessionState(root, "active"); err != nil {
		t.Fatal(err)
	}
	appendLongCommands("active", 'x', 2)
	time.Sleep(20 * time.Millisecond)
	if _, err := MutateSessionState(root, "concurrent", func(state SessionState) SessionState {
		return AppendClaim(state, "still-working")
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeActiveSession(root, "active"); err != nil {
		t.Fatal(err)
	}

	report := retention.Run(retention.Options{
		RepoRoot: root, StateRoot: stateDir, ActiveSession: "active",
		Policy: retention.DefaultPolicy(), Now: time.Now().UTC(), TempRoot: t.TempDir(),
	})
	if len(report.Errors) != 0 {
		t.Fatalf("retention errors: %v", report.Errors)
	}
	stop := RunStop(root, []byte(`{"session_id":"concurrent"}`))
	if stop.ExitCode != 0 {
		t.Fatalf("concurrent session Stop after retention: exit=%d stderr=%.400s", stop.ExitCode, stop.Stderr)
	}
	if _, err := LoadSessionState(root, "concurrent"); err != nil {
		t.Fatalf("concurrent session state no longer loads after retention: %v", err)
	}
	status, err := ReadEvidenceTaintStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.Present {
		t.Fatalf("routine retention tainted the repository: %s/%s", status.Field, status.Limit)
	}
}
