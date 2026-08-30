package agentsession

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/privatefs"
	"reconc.dev/reconc/internal/repositorycontrol"
)

func TestRepositoryRunMutationSyncsMaterialStateAndSkipsNoOp(t *testing.T) {
	repo := t.TempDir()
	if err := saveRepositoryRunState(repo, repositoryRunState{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	original := syncRepositoryRunFile
	syncs := 0
	syncRepositoryRunFile = func(file *os.File) error {
		syncs++
		return original(file)
	}
	t.Cleanup(func() { syncRepositoryRunFile = original })

	_, after, err := mutateRepositoryRunState(repo, func(state repositoryRunState) repositoryRunState {
		state.NoProgressNudges = 1
		return state
	})
	if err != nil {
		t.Fatal(err)
	}
	if syncs != 1 {
		t.Fatalf("material repository-run mutation syncs = %d, want 1", syncs)
	}
	persisted, err := loadRepositoryRunState(repo)
	if err != nil || persisted != after {
		t.Fatalf("acknowledged repository-run state = %+v, %v; want %+v", persisted, err, after)
	}
	if _, _, err := mutateRepositoryRunState(repo, func(state repositoryRunState) repositoryRunState {
		return state
	}); err != nil {
		t.Fatal(err)
	}
	if syncs != 1 {
		t.Fatalf("no-op repository-run mutation added a sync: %d", syncs)
	}
}

func TestRepositoryRunMutationJoinsSyncUnlockAndCloseErrors(t *testing.T) {
	repo := t.TempDir()
	if err := saveRepositoryRunState(repo, repositoryRunState{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	original := syncRepositoryRunFile
	syncFailure := errors.New("injected repository-run sync failure")
	syncRepositoryRunFile = func(file *os.File) error {
		if err := file.Close(); err != nil {
			return errors.Join(syncFailure, err)
		}
		return syncFailure
	}
	t.Cleanup(func() { syncRepositoryRunFile = original })

	_, _, err := mutateRepositoryRunState(repo, func(state repositoryRunState) repositoryRunState {
		state.NoProgressNudges = 1
		return state
	})
	if !errors.Is(err, syncFailure) {
		t.Fatalf("repository-run sync failure = %v", err)
	}
	for _, operation := range []string{
		"sync repository run state",
		"unlock repository run state",
		"close repository run state",
	} {
		if !strings.Contains(err.Error(), operation) {
			t.Fatalf("joined repository-run failure lacks %q: %v", operation, err)
		}
	}
}

func TestRepositoryRunStoreFallsBackFromTornNewestSlot(t *testing.T) {
	repo := t.TempDir()
	first := repositoryRunState{Enabled: true, EnabledAt: 1_752_524_800_000_000_000}
	if err := saveRepositoryRunState(repo, first); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	first.RootIdentity = repositoryRunRootIdentity(root)
	if err := saveRepositoryRunState(repo, repositoryRunState{DisabledReason: repositoryRunDisabledCommandOff}); err != nil {
		t.Fatal(err)
	}
	path, err := repositoryRunStatePath(repo)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("TORN"), repositoryRunSlotSize); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := loadRepositoryRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("torn newest slot did not fall back: got %+v want %+v", got, first)
	}
}

func TestRepositoryRunStoreFallsBackFromCorruptNewestHeader(t *testing.T) {
	repo := t.TempDir()
	first := repositoryRunState{Enabled: true, EnabledAt: 1_752_524_800_000_000_000}
	if err := saveRepositoryRunState(repo, first); err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	first.RootIdentity = repositoryRunRootIdentity(root)
	if err := saveRepositoryRunState(repo, repositoryRunState{DisabledReason: repositoryRunDisabledCommandOff}); err != nil {
		t.Fatal(err)
	}
	path, err := repositoryRunStatePath(repo)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{repositoryRunEnabledBit}, repositoryRunSlotSize+5); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := loadRepositoryRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("corrupt newest header did not fall back: got %+v want %+v", got, first)
	}
}

func TestRepositoryRunStoreFailsClosedWhenBothSlotsAreInvalid(t *testing.T) {
	repo := t.TempDir()
	if err := saveRepositoryRunState(repo, repositoryRunState{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	path, err := repositoryRunStatePath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRepositoryRunState(repo); err == nil {
		t.Fatal("invalid repository run slots must fail closed")
	}
}

func TestRepositoryRunStoreIgnoresRemovedRunloopPath(t *testing.T) {
	repo := t.TempDir()
	legacy := filepath.Join(repo, ".reconc", "runloop", "state.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"enabled":true,"mode":"repo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := loadRepositoryRunState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state != (repositoryRunState{}) {
		t.Fatalf("removed runloop path affected repository run state: %+v", state)
	}
}

func TestRepositoryRunStoreRejectsOversizedState(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".reconc", "run", "state.bin")
	if err := repositorycontrol.EnsureRunDirectory(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := privatefs.WritePrivateIfChanged(path, make([]byte, repositoryRunSlotSize*2+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRepositoryRunState(repo); err == nil {
		t.Fatal("oversized repository run state must fail closed")
	}
}

func TestRepositoryRunStoreIsBoundedAndPrivate(t *testing.T) {
	repo := t.TempDir()
	for index := 0; index < 100; index++ {
		if _, _, err := mutateRepositoryRunState(repo, func(state repositoryRunState) repositoryRunState {
			state.Enabled = true
			state.NoProgressNudges = index
			return state
		}); err != nil {
			t.Fatal(err)
		}
	}
	path, err := repositoryRunStatePath(repo)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > repositoryRunSlotSize*2 {
		t.Fatalf("repository run store grew to %d bytes", info.Size())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("repository run store mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestRepositoryRunStateRejectsForeignRootAndResetPreservesLog(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	if _, err := SetRepositoryRun(source, true); err != nil {
		t.Fatal(err)
	}
	sourcePath, _ := repositoryRunStatePath(source)
	targetPath, _ := repositoryRunStatePath(target)
	if err := repositorycontrol.EnsureRunDirectory(target); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := privatefs.WritePrivateIfChanged(targetPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendRunDecision(target, RunDecision{Event: "sentinel", Branch: "preserve_me"}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRepositoryRunState(target); err == nil || !strings.Contains(err.Error(), "different repository root") || !strings.Contains(err.Error(), "run reset") {
		t.Fatalf("foreign state did not fail closed with remediation: %v", err)
	}
	status, err := ResetRepositoryRun(target)
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.DisabledReason != "command_off" {
		t.Fatalf("reset did not restore clean disabled state: %+v", status)
	}
	decisions, err := ReadRunDecisions(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 || decisions[0].Branch != "preserve_me" || decisions[1].Branch != "run_state_reset" {
		t.Fatalf("reset deleted unrelated run evidence: %#v", decisions)
	}
}

func TestRepositoryRunResetRecoversFullyCorruptState(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".reconc", "run", "state.bin")
	if err := repositorycontrol.EnsureRunDirectory(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := privatefs.WritePrivateIfChanged(path, []byte("both slots are corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRepositoryRunStatus(repo); err == nil || !strings.Contains(err.Error(), "run reset") {
		t.Fatalf("corrupt state lacks exact remediation: %v", err)
	}
	if _, err := ResetRepositoryRun(repo); err != nil {
		t.Fatal(err)
	}
	state, err := loadRepositoryRunState(repo)
	if err != nil || state.Enabled || state.RootIdentity == ([32]byte{}) {
		t.Fatalf("reset state invalid: %+v err=%v", state, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("reset state mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestRepositoryRunResetRejectsSymlinkedStateWithoutTouchingTarget(t *testing.T) {
	repo := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.bin")
	const original = "must survive"
	if err := os.WriteFile(victim, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".reconc", "run", "state.bin")
	if err := repositorycontrol.EnsureRunDirectory(repo); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := ResetRepositoryRun(repo); err == nil || !strings.Contains(err.Error(), "symlink component") {
		t.Fatalf("reset followed a symlinked state path: %v", err)
	}
	body, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != original {
		t.Fatalf("reset modified symlink target: %q", body)
	}
}
