package retention

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunBoundsPreDecisionsAndTaintResolutions(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy ClassPolicy
	}{
		{name: "age", policy: ClassPolicy{MaxFiles: 8, MaxBytes: 1 << 20, MaxAge: time.Hour}},
		{name: "count", policy: ClassPolicy{MaxFiles: 1, MaxBytes: 1 << 20}},
		{name: "bytes", policy: ClassPolicy{MaxFiles: 8, MaxBytes: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			stateRoot := t.TempDir()
			now := time.Now().UTC()
			project := ProjectDir(stateRoot, repo)
			preOld := filepath.Join(project, "pre-decisions", "old.json")
			preNew := filepath.Join(project, "pre-decisions", "new.json")
			writeTimed(t, preOld, []byte("old"), now.Add(-3*time.Hour))
			writeTimed(t, preNew, []byte("new"), now.Add(-2*time.Hour))
			_, resolutionOld := writeTaintResolutionFixture(t, project, repo, "old", now.Add(-3*time.Hour))
			_, resolutionNew := writeTaintResolutionFixture(t, project, repo, "new", now.Add(-2*time.Hour))

			policy := DefaultPolicy()
			policy.PreDecisions = test.policy
			policy.TaintResolutions = test.policy
			report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
			if len(report.Errors) != 0 {
				t.Fatalf("retention errors: %v", report.Errors)
			}
			for _, path := range []string{preOld, resolutionOld} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("bounded artifact survived: %s: %v", path, err)
				}
			}
			if test.name == "count" {
				for _, path := range []string{preNew, resolutionNew} {
					if _, err := os.Lstat(path); err != nil {
						t.Fatalf("newest artifact was not retained: %s: %v", path, err)
					}
				}
			} else {
				for _, path := range []string{preNew, resolutionNew} {
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("bounded newest artifact survived: %s: %v", path, err)
					}
				}
			}
		})
	}
}

func TestRunProtectsActivePreDecisionAndAuditRelevantResolution(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	activeID := "active-session"
	preDecision := filepath.Join(project, "pre-decisions", SessionFileID(activeID)+".json")
	writeTimed(t, preDecision, []byte("active"), now.Add(-48*time.Hour))
	taint, matching := writeTaintResolutionFixture(t, project, repo, "matching", now.Add(-48*time.Hour))
	_, stale := writeTaintResolutionFixture(t, project, repo, "stale", now.Add(-72*time.Hour))
	writeJSONFixture(t, filepath.Join(project, "evidence-taint.json"), taint, now)

	policy := DefaultPolicy()
	policy.PreDecisions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.TaintResolutions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.StateTotalBytes = 0
	report := Run(Options{
		RepoRoot: repo, StateRoot: stateRoot, ActiveSession: activeID,
		Policy: policy, Now: now, TempRoot: t.TempDir(),
	})
	for _, path := range []string{preDecision, matching, stale} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("active-session artifact was removed: %s: %v; report=%+v", path, err, report)
		}
	}
	if !strings.Contains(strings.Join(report.Errors, "\n"), "protected state uses") {
		t.Fatalf("protected budget pressure was not reported: %+v", report)
	}

	report = Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if _, err := os.Lstat(matching); err != nil {
		t.Fatalf("current-taint resolution was removed: %v; report=%+v", err, report)
	}
	if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("historical resolution survived inactive pruning: %v; report=%+v", err, report)
	}
}

func TestRunPreservesMalformedAndSymlinkStateArtifacts(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	malformed := filepath.Join(project, "evidence-taint-resolutions", strings.Repeat("a", 64)+".json")
	writeTimed(t, malformed, []byte("not-json"), now.Add(-48*time.Hour))
	foreign := filepath.Join(t.TempDir(), "foreign.json")
	writeTimed(t, foreign, []byte("foreign"), now.Add(-48*time.Hour))
	symlink := filepath.Join(project, "pre-decisions", "linked.json")
	if err := os.MkdirAll(filepath.Dir(symlink), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, symlink); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	policy := DefaultPolicy()
	policy.PreDecisions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.TaintResolutions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.StateTotalBytes = 0
	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if _, err := os.Lstat(malformed); err != nil {
		t.Fatalf("malformed receipt was deleted: %v", err)
	}
	if info, err := os.Lstat(symlink); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink entry changed: info=%v err=%v", info, err)
	}
	if body, err := os.ReadFile(foreign); err != nil || string(body) != "foreign" {
		t.Fatalf("symlink target changed: body=%q err=%v", body, err)
	}
	errorsText := strings.Join(report.Errors, "\n")
	for _, want := range []string{"invalid JSON", "not a regular file"} {
		if !strings.Contains(errorsText, want) {
			t.Fatalf("missing %q error: %+v", want, report)
		}
	}
}

func TestRunMalformedLiveTaintProtectsEveryResolution(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	_, first := writeTaintResolutionFixture(t, project, repo, "first", now.Add(-72*time.Hour))
	_, second := writeTaintResolutionFixture(t, project, repo, "second", now.Add(-48*time.Hour))
	writeTimed(t, filepath.Join(project, "evidence-taint.json"), []byte("{broken\n"), now)

	policy := DefaultPolicy()
	policy.TaintResolutions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.StateTotalBytes = 0
	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	for _, path := range []string{first, second} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("resolution was removed while live taint was unreadable: %s: %v", path, err)
		}
	}
	errorsText := strings.Join(report.Errors, "\n")
	for _, want := range []string{"resolve taint-resolution protection", "protected state uses"} {
		if !strings.Contains(errorsText, want) {
			t.Fatalf("missing %q error: %+v", want, report)
		}
	}
}

func TestRunDryRunAndStateTotalIncludeNewArtifactClasses(t *testing.T) {
	repo := t.TempDir()
	stateRoot := t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	preDecision := filepath.Join(project, "pre-decisions", "dry-run.json")
	writeTimed(t, preDecision, []byte("cache"), now.Add(-48*time.Hour))
	_, resolution := writeTaintResolutionFixture(t, project, repo, "dry-run", now.Add(-48*time.Hour))
	resolutionInfo, err := os.Stat(resolution)
	if err != nil {
		t.Fatal(err)
	}

	policy := DefaultPolicy()
	policy.PreDecisions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.TaintResolutions = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Nanosecond}
	policy.StateTotalBytes = 1 << 20
	report := Run(Options{
		RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now,
		TempRoot: t.TempDir(), DryRun: true,
	})
	for _, path := range []string{preDecision, resolution} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("dry run removed %s: %v", path, err)
		}
	}
	stateTotal := classReportByName(t, report, "state-total")
	wantBytes := int64(len("cache")) + resolutionInfo.Size()
	if stateTotal.BytesBefore != wantBytes || stateTotal.BytesAfter != 0 || stateTotal.BytesFreed != wantBytes {
		t.Fatalf("state-total projection = %+v, want before/freed %d", stateTotal, wantBytes)
	}
	for _, name := range []string{"pre-decisions", "evidence-taint-resolutions"} {
		class := classReportByName(t, report, name)
		if class.FilesDeleted != 1 || class.BytesAfter != 0 {
			t.Fatalf("dry-run class %s = %+v", name, class)
		}
	}
}

func TestTaintResolutionRemovalRevalidatesContent(t *testing.T) {
	repo := t.TempDir()
	project := t.TempDir()
	now := time.Now().UTC()
	_, path := writeTaintResolutionFixture(t, project, repo, "race", now.Add(-48*time.Hour))
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	item := candidate{
		path: path, name: filepath.Base(path), size: info.Size(), mtime: info.ModTime(), info: info,
		validate: inspectTaintResolutionArtifact(repo),
	}
	report := Report{}
	removed := removeCandidateWithHooks(item, false, &report, candidateRemovalHooks{
		afterValidation: func(item candidate) error {
			body, readErr := os.ReadFile(item.path)
			if readErr != nil {
				return readErr
			}
			body[0] = '['
			if writeErr := os.WriteFile(item.path, body, 0o600); writeErr != nil {
				return writeErr
			}
			return os.Chtimes(item.path, item.mtime, item.mtime)
		},
	})
	if removed {
		t.Fatal("concurrently rewritten receipt was deleted")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("rewritten receipt was not preserved: %v", err)
	}
	if !strings.Contains(strings.Join(report.Errors, "\n"), "revalidate content") {
		t.Fatalf("content race was not reported: %+v", report)
	}
}

func writeTaintResolutionFixture(t *testing.T, project, repoRoot, sessionID string, modTime time.Time) (retainedEvidenceTaint, string) {
	t.Helper()
	taint := retainedEvidenceTaint{
		FormatVersion: evidenceTaintVersion,
		RepoRoot:      repoRoot,
		SessionID:     sessionID,
		Field:         "commands",
		Limit:         "fixture limit",
	}
	token, err := retainedEvidenceTaintToken(taint, repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolution := retainedTaintResolution{
		FormatVersion: evidenceTaintResolutionVersion,
		Token:         token,
		Reason:        "verified fixture",
		Taint:         taint,
	}
	path := filepath.Join(project, "evidence-taint-resolutions", token+".json")
	writeJSONFixture(t, path, resolution, modTime)
	return taint, path
}

func writeJSONFixture(t *testing.T, path string, value any, modTime time.Time) {
	t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTimed(t, path, append(body, '\n'), modTime)
}

func classReportByName(t *testing.T, report Report, name string) ClassReport {
	t.Helper()
	for _, class := range report.Classes {
		if class.Name == name {
			return class
		}
	}
	t.Fatalf("missing class %q in %+v", name, report)
	return ClassReport{}
}
