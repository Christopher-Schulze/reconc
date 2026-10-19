package retention

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandTextRetentionProtectsActiveSession(t *testing.T) {
	for _, total := range []bool{false, true} {
		t.Run(map[bool]string{false: "class", true: "total"}[total], func(t *testing.T) {
			repo, stateRoot := t.TempDir(), t.TempDir()
			now := time.Now().UTC()
			project := ProjectDir(stateRoot, repo)
			name := ".command-" + strings.Repeat("a", 64) + ".txt"
			active := filepath.Join(project, "sessions", "active"+name)
			stale := filepath.Join(project, "sessions", "stale"+name)
			writeTimed(t, active, []byte("active"), now.Add(-72*time.Hour))
			writeTimed(t, stale, []byte("stale"), now.Add(-72*time.Hour))
			policy := DefaultPolicy()
			if total {
				policy.StateTotalBytes = 1
			} else {
				policy.Sessions = ClassPolicy{MaxFiles: 0, MaxBytes: 0}
			}
			report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, ActiveSession: "active", Policy: policy, Now: now, TempRoot: t.TempDir()})
			if _, err := os.Stat(active); err != nil {
				t.Fatalf("active command removed: %v; report=%+v", err, report)
			}
			if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("inactive command survived pressure: %v", err)
			}
		})
	}
}

func TestEvidenceTaintCoordinationLockSurvivesAllStatePressure(t *testing.T) {
	repo, stateRoot := t.TempDir(), t.TempDir()
	now := time.Now().UTC()
	path := filepath.Join(ProjectDir(stateRoot, repo), "locks", EvidenceTaintLockName)
	writeTimed(t, path, []byte{}, now.Add(-72*time.Hour))
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy()
	policy.Locks = ClassPolicy{MaxFiles: 0, MaxBytes: 0}
	policy.StateTotalBytes = 0
	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("coordination lock inode was replaced or removed: %v report=%+v", err, report)
	}
}

func TestTaintedSessionStateSurvivesTotalPressure(t *testing.T) {
	repo, stateRoot := t.TempDir(), t.TempDir()
	now := time.Now().UTC()
	project := ProjectDir(stateRoot, repo)
	taint, _ := writeTaintResolutionFixture(t, project, repo, "origin", now)
	writeJSONFixture(t, filepath.Join(project, "evidence-taint.json"), taint, now)
	path := filepath.Join(project, "sessions", "origin.json")
	writeTimed(t, path, []byte("{}"), now.Add(-72*time.Hour))
	policy := DefaultPolicy()
	policy.StateTotalBytes = 0
	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, ActiveSession: "successor", Policy: policy, Now: now, TempRoot: t.TempDir()})
	if body, err := os.ReadFile(path); err != nil || string(body) != "{}" {
		t.Fatalf("total pressure removed unresolved origin state: %q %v; report=%+v", body, err, report)
	}
}

func TestGlobalRetentionPreservesUnresolvedEvidenceTaint(t *testing.T) {
	repo, stateRoot := t.TempDir(), t.TempDir()
	now := time.Now().UTC()
	blocked := ProjectDir(stateRoot, t.TempDir())
	path := filepath.Join(blocked, "evidence-taint.json")
	writeTimed(t, path, []byte("unresolved evidence"), now.Add(-365*24*time.Hour))
	policy := DefaultPolicy()
	policy.ProjectRoots = ClassPolicy{MaxFiles: 0, MaxBytes: 0, MaxAge: time.Hour}
	report := Run(Options{RepoRoot: repo, StateRoot: stateRoot, Policy: policy, Now: now, TempRoot: t.TempDir()})
	if body, err := os.ReadFile(path); err != nil || string(body) != "unresolved evidence" {
		t.Fatalf("global cleanup erased an unresolved evidence boundary: %q %v; report=%+v", body, err, report)
	}
}

func TestGlobalRetentionRechecksTaintAfterAcquiringProjectLeases(t *testing.T) {
	project := t.TempDir()
	info, err := os.Stat(project)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "evidence-taint.json")
	item := candidate{path: project, info: info, dir: true, probeProjectRoot: true}
	report := Report{}
	removed := removeCandidateWithHooks(item, false, &report, candidateRemovalHooks{afterValidation: func(candidate) error {
		return os.WriteFile(path, []byte("newly published taint"), 0o600)
	}})
	if removed {
		t.Fatal("project cleanup ignored a taint published after its initial scan")
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "newly published taint" {
		t.Fatalf("late evidence taint was lost: %q %v; report=%+v", body, err, report)
	}
}
