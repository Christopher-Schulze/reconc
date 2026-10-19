package retention

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sessionWithDependents writes a session state file, one full-command text
// object and one evidence chain for id and returns their paths.
func sessionWithDependents(t *testing.T, project, id string, stateTime, dependentTime time.Time) (state, text, chain string) {
	t.Helper()
	state = filepath.Join(project, "sessions", id+".json")
	text = filepath.Join(project, "sessions", id+".command-"+strings.Repeat("b", 64)+".txt")
	writeTimed(t, text, []byte(strings.Repeat("x", 4096)), dependentTime)
	chain = writeEvidenceDirectoryFixture(t, project, id, 2, 512, dependentTime)
	writeTimed(t, state, []byte("{}"), stateTime)
	return state, text, chain
}

func TestRetentionKeepsDependentsOfSurvivingSessionStates(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy func(*Policy)
	}{
		{name: "session class bytes", policy: func(policy *Policy) {
			policy.Sessions = ClassPolicy{MaxFiles: 8, MaxBytes: 64}
		}},
		{name: "session class files", policy: func(policy *Policy) {
			policy.Sessions = ClassPolicy{MaxFiles: 2, MaxBytes: 1 << 20}
		}},
		{name: "evidence class", policy: func(policy *Policy) {
			policy.EvidenceSegments = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
		}},
		{name: "state total", policy: func(policy *Policy) {
			policy.StateTotalBytes = 64
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, stateRoot := t.TempDir(), t.TempDir()
			now := time.Now().UTC()
			project := ProjectDir(stateRoot, repo)
			// A concurrent session that is not the active one keeps working:
			// its state file is recent, its referenced evidence is older.
			state, text, chain := sessionWithDependents(t, project, "concurrent", now, now.Add(-time.Hour))
			writeTimed(t, filepath.Join(project, "sessions", "active.json"), []byte("{}"), now)
			policy := DefaultPolicy()
			test.policy(&policy)
			report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, ActiveSession: "active", Policy: policy, Now: now, TempRoot: t.TempDir()})
			if len(report.Errors) != 0 {
				t.Fatalf("retention errors: %v", report.Errors)
			}
			for _, path := range []string{state, text, chain} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("retention removed %s while its session state survives: %v; report=%+v", path, err, report)
				}
			}
		})
	}
}

func TestRetentionRemovesDependentsWithTheirSessionState(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy func(*Policy)
	}{
		{name: "session class", policy: func(policy *Policy) {
			policy.Sessions = ClassPolicy{MaxFiles: 8, MaxBytes: 1 << 20, MaxAge: time.Hour}
		}},
		{name: "state total", policy: func(policy *Policy) {
			policy.StateTotalBytes = 0
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, stateRoot := t.TempDir(), t.TempDir()
			now := time.Now().UTC()
			project := ProjectDir(stateRoot, repo)
			state, text, chain := sessionWithDependents(t, project, "finished", now.Add(-48*time.Hour), now.Add(-49*time.Hour))
			policy := DefaultPolicy()
			test.policy(&policy)
			report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
			if len(report.Errors) != 0 {
				t.Fatalf("retention errors: %v", report.Errors)
			}
			for _, path := range []string{state, text, chain} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s survived the removal of its session state: %v; report=%+v", path, err, report)
				}
			}
		})
	}
}

func TestRetentionDryRunProjectsDependentRemoval(t *testing.T) {
	repo, stateRoot := t.TempDir(), t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	state, text, chain := sessionWithDependents(t, project, "finished", now.Add(-48*time.Hour), now.Add(-49*time.Hour))
	policy := DefaultPolicy()
	policy.Sessions = ClassPolicy{MaxFiles: 8, MaxBytes: 1 << 20, MaxAge: time.Hour}
	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir(), DryRun: true})
	if len(report.Errors) != 0 {
		t.Fatalf("retention errors: %v", report.Errors)
	}
	for _, path := range []string{state, text, chain} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry run removed %s: %v", path, err)
		}
	}
	if sessions := classReportByName(t, report, "sessions"); sessions.FilesDeleted != 2 || sessions.FilesKept != 0 {
		t.Fatalf("dry-run sessions projection = %+v, want state and text removed", sessions)
	}
	if segments := classReportByName(t, report, "evidence-segments"); segments.FilesDeleted != 1 || segments.FilesKept != 0 {
		t.Fatalf("dry-run evidence projection = %+v, want the owned chain removed", segments)
	}
}
