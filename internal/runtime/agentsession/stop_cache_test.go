package agentsession

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestDirtyPathsFromStatusIncludesRenamePairAndSkipsRuntimeState(t *testing.T) {
	status := " M src/a.go\x00R  src/new.go\x00src/old.go\x00?? docs/new.md\x00 M .reconc/cache/report.json\x00?? .reconc/reports/s1.json\x00?? .reconc/locks/s1.stop-policy.lock\x00"
	got := dirtyPathsFromStatus(status)
	want := []string{"docs/new.md", "src/a.go", "src/new.go", "src/old.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty paths mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestStopScopeWritePathsToUncommittedUsesFingerprintSnapshot(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "--quiet")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	write("clean.go", "package x\n")
	write("dirty.go", "package x\n")
	git("add", "clean.go", "dirty.go")
	git("commit", "-m", "base", "--quiet")
	write("dirty.go", "package x // dirty\n")
	write("newpkg/sub.go", "package newpkg\n")
	write("other-session.go", "package other\n")

	snapshot := stopPolicyGitSnapshotFor(repo)
	if !snapshot.StatusOK {
		t.Fatalf("status snapshot failed: %s", snapshot.Status)
	}
	abs := func(rel string) string { return filepath.Join(repo, rel) }
	writes := []string{abs("clean.go"), abs("dirty.go"), abs("newpkg/sub.go")}
	want := []string{abs("dirty.go"), abs("newpkg/sub.go")}
	if got := stopScopeWritePathsToUncommitted(repo, writes, snapshot); !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped writes mismatch\ngot:  %#v\nwant: %#v", got, want)
	}

	git("add", "-A")
	git("commit", "-m", "finish", "--quiet")
	cleanSnapshot := stopPolicyGitSnapshotFor(repo)
	if got := stopScopeWritePathsToUncommitted(repo, writes, cleanSnapshot); len(got) != 0 {
		t.Fatalf("committed writes must leave no stop triggers, got %#v", got)
	}
}

func TestStopScopeWritePathsFailsClosed(t *testing.T) {
	repo := t.TempDir()
	writes := []string{filepath.Join(repo, "src", "a.go"), "../outside.md"}
	unknown := stopPolicyGitSnapshot{Status: "git failed", StatusMode: "normal", StatusOK: false}
	if got := stopScopeWritePathsToUncommitted(repo, writes, unknown); !reflect.DeepEqual(got, writes) {
		t.Fatalf("untrusted status must retain all writes\ngot:  %#v\nwant: %#v", got, writes)
	}
	dirty := stopPolicyGitSnapshot{Status: "?? src/\x00", StatusMode: "normal", StatusOK: true}
	if got := stopScopeWritePathsToUncommitted(repo, writes, dirty); !reflect.DeepEqual(got, writes) {
		t.Fatalf("unresolvable paths must be retained\ngot:  %#v\nwant: %#v", got, writes)
	}
}

func TestCachedCleanStopReportRequiresCurrentFingerprint(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	state, err := InitializeSessionState(repo, "s-exact-cache")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runStopPolicyCheck(repo, state); err != nil {
		t.Fatalf("populate cache: %v", err)
	}
	state, err = LoadSessionState(repo, "s-exact-cache")
	if err != nil {
		t.Fatal(err)
	}
	evidenceHash := stopPolicyEvidenceHash(state)
	if _, ok := cachedCleanStopPolicyReportForEvidence(state.RepoRoot, state, evidenceHash); !ok {
		t.Fatal("unchanged exact report should be reusable")
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cachedCleanStopPolicyReportForEvidence(state.RepoRoot, state, evidenceHash); ok {
		t.Fatal("dirty-state drift must invalidate the clean reentrant report")
	}
}

func TestStopPolicyFingerprintTracksDirtyContentWithoutFullDiff(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	state, err := InitializeSessionState(repo, "s-dirty-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(repo, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(srcDir, "a.go")

	if err := os.WriteFile(target, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "add", "src/a.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", repo, "commit", "-m", "track a", "--quiet")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	if err := os.WriteFile(target, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := stopPolicyFingerprint(repo, state)

	if err := os.WriteFile(target, []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := stopPolicyFingerprint(repo, state)
	if first == second {
		t.Fatal("fingerprint must change when tracked worktree content changes")
	}

	cmd = exec.Command("git", "-C", repo, "add", "src/a.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	staged := stopPolicyFingerprint(repo, state)
	if staged == second {
		t.Fatal("fingerprint must change when dirty content moves into the index")
	}

	if err := os.WriteFile(target, []byte("four\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unstagedAfterStaged := stopPolicyFingerprint(repo, state)
	if unstagedAfterStaged == staged {
		t.Fatal("fingerprint must change when worktree content diverges from the staged blob")
	}
}

func TestStopPolicyFingerprintUntrackedModeAllIsOptIn(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	state, err := InitializeSessionState(repo, "s-untracked-mode")
	if err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(repo, "scratch")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(srcDir, "a.txt")
	if err := os.WriteFile(target, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(stopPolicyUntrackedModeEnv, "")
	normalA := stopPolicyFingerprint(repo, state)
	if err := os.WriteFile(target, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	normalB := stopPolicyFingerprint(repo, state)
	if normalA != normalB {
		t.Fatal("default normal untracked mode should not hash every file inside an untracked directory")
	}

	t.Setenv(stopPolicyUntrackedModeEnv, "all")
	allA := stopPolicyFingerprint(repo, state)
	if err := os.WriteFile(target, []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	allB := stopPolicyFingerprint(repo, state)
	if allA == allB {
		t.Fatal("all untracked mode must fingerprint file content inside untracked directories")
	}
}

func TestStopPolicyGitStatusNormalTracksUntrackedDirectorySentinel(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	repo := setupStopScriptPolicyRepo(t, counterPath, 0, "")
	if err := os.MkdirAll(filepath.Join(repo, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scratch", "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(stopPolicyUntrackedModeEnv, "")
	status, mode := stopPolicyGitStatus(repo)
	if mode != "normal" {
		t.Fatalf("expected normal mode, got %q", mode)
	}
	if !strings.Contains(status, "?? scratch/\x00") {
		t.Fatalf("normal status should include untracked directory sentinel, got %q", status)
	}

	tracked := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked.txt"}, {"commit", "-m", "track", "--quiet"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(tracked, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, _ = stopPolicyGitStatus(repo)
	if !strings.Contains(status, "tracked.txt") || !strings.Contains(status, "?? scratch/\x00") {
		t.Fatalf("one status snapshot must contain tracked and untracked changes, got %q", status)
	}
}

func TestGitHeadFingerprintTracksCommitAndLinkedWorktree(t *testing.T) {
	repo := setupStopBenchmarkRepo(t)
	first := gitHeadFingerprint(repo)
	if strings.HasPrefix(first, "error:") || strings.HasSuffix(first, "missing") {
		t.Fatalf("initial HEAD fingerprint is invalid: %q", first)
	}
	if err := os.WriteFile(filepath.Join(repo, "src/a.go"), []byte("package src // next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "src/a.go"}, {"commit", "-m", "next", "--quiet"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	second := gitHeadFingerprint(repo)
	if second == first {
		t.Fatalf("HEAD fingerprint did not change after commit: %q", second)
	}
	worktree := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "--quiet", "-b", "linked-test", worktree, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	linked := gitHeadFingerprint(worktree)
	if strings.HasPrefix(linked, "error:") || strings.HasSuffix(linked, "missing") {
		t.Fatalf("linked-worktree HEAD fingerprint is invalid: %q", linked)
	}
}

func BenchmarkStopPolicyFingerprint(b *testing.B) {
	benchmarks := []struct {
		name  string
		dirty func(testing.TB, string)
	}{
		{name: "clean"},
		{
			name: "tracked-dirty",
			dirty: func(tb testing.TB, repo string) {
				tb.Helper()
				if err := os.WriteFile(filepath.Join(repo, "src/a.go"), []byte("package src // dirty\n"), 0o644); err != nil {
					tb.Fatal(err)
				}
			},
		},
		{
			name: "untracked-directory",
			dirty: func(tb testing.TB, repo string) {
				tb.Helper()
				path := filepath.Join(repo, "scratch/a.txt")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					tb.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("scratch\n"), 0o644); err != nil {
					tb.Fatal(err)
				}
			},
		},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			repo := setupStopBenchmarkRepo(b)
			if benchmark.dirty != nil {
				benchmark.dirty(b, repo)
			}
			state, err := InitializeSessionState(repo, "fingerprint")
			if err != nil {
				b.Fatal(err)
			}
			state = AppendWritePath(state, "src/a.go")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := stopPolicyFingerprint(repo, state); got == "" {
					b.Fatal("empty fingerprint")
				}
			}
		})
	}
}

func BenchmarkStopPolicyCheck(b *testing.B) {
	b.Run("cold-evidence", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		state, err := InitializeSessionState(repo, "cold")
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			iteration := state
			iteration.Commands = []string{fmt.Sprintf("bench-%d", i)}
			if _, err := runStopPolicyCheck(repo, iteration); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("warm-exact", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		state, err := InitializeSessionState(repo, "warm")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := runStopPolicyCheck(repo, state); err != nil {
			b.Fatal(err)
		}
		state, err = LoadSessionState(repo, "warm")
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := runStopPolicyCheck(repo, state); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("reentrant-clean", func(b *testing.B) {
		repo := setupStopBenchmarkRepo(b)
		state, err := InitializeSessionState(repo, "reentrant")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := runStopPolicyCheck(repo, state); err != nil {
			b.Fatal(err)
		}
		state, err = LoadSessionState(repo, "reentrant")
		if err != nil {
			b.Fatal(err)
		}
		evidenceHash := stopPolicyEvidenceHash(state)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := cachedCleanStopPolicyReportForEvidence(state.RepoRoot, state, evidenceHash); !ok {
				b.Fatal("exact reentrant report was not reusable")
			}
		}
	})
}

func setupStopBenchmarkRepo(tb testing.TB) string {
	tb.Helper()
	tb.Setenv("RECONC_HOME", tb.TempDir())
	tb.Setenv(StateRootEnv, tb.TempDir())
	repo := tb.TempDir()
	git := func(args ...string) {
		tb.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			tb.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, body string) {
		tb.Helper()
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	git("init", "--quiet")
	git("config", "user.email", "bench@reconc.dev")
	git("config", "user.name", "reconc benchmark")
	write("AGENTS.md", "# Benchmark\n")
	write("policies/rules.yml", "rules: []\n")
	write("src/a.go", "package src\n")
	if _, err := compiler.CompileRepoPolicy(repo, "benchmark"); err != nil {
		tb.Fatalf("compile policy: %v", err)
	}
	git("add", "-A")
	git("commit", "-m", "benchmark fixture", "--quiet")
	return repo
}
