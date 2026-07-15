package agentsession

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRepositoryRunStoreFallsBackFromTornNewestSlot(t *testing.T) {
	repo := t.TempDir()
	first := repositoryRunState{Enabled: true, EnabledAt: 1_752_524_800_000_000_000}
	if err := saveRepositoryRunState(repo, first); err != nil {
		t.Fatal(err)
	}
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, repositoryRunSlotSize*2+1), 0o600); err != nil {
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
