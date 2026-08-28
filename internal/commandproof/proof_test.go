package commandproof

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProofBindsSuccessToCurrentStagedIndex(t *testing.T) {
	repo := newProofRepo(t)
	stageFile(t, repo, "candidate.txt", "first\n")
	snapshot, err := CaptureStagedClean(repo)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stored, err := StoreSuccess(snapshot, "go test ./...", "direct", now.Add(-time.Second), now)
	if err != nil {
		t.Fatal(err)
	}
	proofs, err := LoadCurrentSuccesses(repo, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 1 || proofs[0].Digest != stored.Digest || proofs[0].Command != "go test ./..." {
		t.Fatalf("unexpected current proofs: %+v", proofs)
	}

	stageFile(t, repo, "candidate.txt", "second\n")
	proofs, err = LoadCurrentSuccesses(repo, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 0 {
		t.Fatalf("proof survived staged-index change: %+v", proofs)
	}
}

func TestTamperedAndExpiredProofsAreNotEvidence(t *testing.T) {
	repo := newProofRepo(t)
	stageFile(t, repo, "candidate.txt", "candidate\n")
	snapshot, err := CaptureStagedClean(repo)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	proof, err := StoreSuccess(snapshot, "go test ./...", "direct", now.Add(-time.Second), now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proofDir(snapshot.RepoRoot), proofIdentity(proof)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &proof); err != nil {
		t.Fatal(err)
	}
	proof.Command = "false"
	tampered, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	proofs, err := LoadCurrentSuccesses(repo, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 0 {
		t.Fatalf("tampered proof became evidence: %+v", proofs)
	}

	if _, err := StoreSuccess(snapshot, "go vet ./...", "direct", now.Add(-26*time.Hour), now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	proofs, err = LoadCurrentSuccesses(repo, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 0 {
		t.Fatalf("expired proof became evidence: %+v", proofs)
	}
}

func TestCaptureStagedCleanRejectsUnstagedAndUntrackedPaths(t *testing.T) {
	t.Run("unstaged", func(t *testing.T) {
		repo := newProofRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := CaptureStagedClean(repo); err == nil {
			t.Fatal("expected unstaged path rejection")
		} else if !strings.Contains(err.Error(), "reconc exec --staged") {
			t.Fatalf("unstaged remediation does not name the proof runner: %v", err)
		}
	})
	t.Run("untracked", func(t *testing.T) {
		repo := newProofRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := CaptureStagedClean(repo); err == nil {
			t.Fatal("expected untracked path rejection")
		} else if !strings.Contains(err.Error(), "reconc exec --staged") {
			t.Fatalf("untracked remediation does not name the proof runner: %v", err)
		}
	})
}

func TestCaptureCurrentIgnoresAmbientForeignGitState(t *testing.T) {
	target := newProofRepo(t)
	stageFile(t, target, "target.txt", "target\n")
	want, err := CaptureCurrent(target)
	if err != nil {
		t.Fatal(err)
	}
	foreign := newProofRepo(t)
	stageFile(t, foreign, "foreign.txt", "foreign\n")
	for key, value := range map[string]string{
		"GIT_DIR":                          filepath.Join(foreign, ".git"),
		"GIT_WORK_TREE":                    foreign,
		"GIT_INDEX_FILE":                   filepath.Join(foreign, ".git", "index"),
		"GIT_COMMON_DIR":                   filepath.Join(foreign, ".git"),
		"GIT_OBJECT_DIRECTORY":             filepath.Join(foreign, ".git", "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": filepath.Join(foreign, ".git", "objects"),
		"GIT_CONFIG_COUNT":                 "1",
		"GIT_CONFIG_KEY_0":                 "alias.rev-parse",
		"GIT_CONFIG_VALUE_0":               "status",
	} {
		t.Setenv(key, value)
	}
	got, err := CaptureCurrent(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ambient Git state changed proof snapshot: got %+v want %+v", got, want)
	}
}

func TestStoreSuccessRejectsSnapshotChangedBeforePublication(t *testing.T) {
	repo := newProofRepo(t)
	stageFile(t, repo, "candidate.txt", "first\n")
	snapshot, err := CaptureStagedClean(repo)
	if err != nil {
		t.Fatal(err)
	}
	stageFile(t, repo, "candidate.txt", "second\n")
	now := time.Now().UTC()
	if _, err := StoreSuccess(snapshot, "go test ./...", "direct", now.Add(-time.Second), now); err == nil {
		t.Fatal("expected changed staged index to reject proof publication")
	} else if !strings.HasPrefix(err.Error(), "git HEAD") {
		t.Fatalf("unexpected snapshot-drift error: %v", err)
	}
}

func TestCaptureStagedCleanSerializesConcurrentSnapshots(t *testing.T) {
	repo := newProofRepo(t)
	stageFile(t, repo, "candidate.txt", "candidate\n")
	const workers = 12
	results := make(chan Snapshot, workers)
	errors := make(chan error, workers)
	var waitGroup sync.WaitGroup
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			snapshot, err := CaptureStagedClean(repo)
			if err != nil {
				errors <- err
				return
			}
			results <- snapshot
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Errorf("concurrent snapshot: %v", err)
	}
	var expected Snapshot
	count := 0
	for snapshot := range results {
		if count == 0 {
			expected = snapshot
		} else if snapshot != expected {
			t.Errorf("snapshot %d differs: got %+v want %+v", count, snapshot, expected)
		}
		count++
	}
	if count != workers {
		t.Fatalf("captured %d snapshots, want %d", count, workers)
	}
}

func TestCaptureStagedCleanWaitsForTransientGitIndexLock(t *testing.T) {
	repo := newProofRepo(t)
	stageFile(t, repo, "candidate.txt", "candidate\n")
	lockPath := filepath.Join(repo, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte("busy"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		removed <- os.Remove(lockPath)
	}()
	if _, err := CaptureStagedClean(repo); err != nil {
		t.Fatalf("transient index lock was not retried: %v", err)
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
}

func newProofRepo(t *testing.T) string {
	t.Helper()
	t.Setenv(retentionStateRootEnvForTest, t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init")
	git(t, repo, "config", "user.name", "reconc-test")
	git(t, repo, "config", "user.email", "reconc-test@example.com")
	git(t, repo, "add", "tracked.txt")
	git(t, repo, "commit", "-m", "initial")
	return repo
}

const retentionStateRootEnvForTest = "RECONC_CLAUDE_STATE_DIR"

func stageFile(t *testing.T, repo, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", name)
}

func git(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
