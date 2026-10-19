package agentsession

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLinkedWorktreeRootMatchesOnlyThisRepositoryWorktrees(t *testing.T) {
	repo := t.TempDir()
	gitInitHelper(t, repo)
	worktree := filepath.Join(repo, ".claude", "worktrees", "w1")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", worktree, "-b", "worktree-w1").CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	canonicalWorktree, err := ResolveRepoRoot(worktree)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		dir  string
		want string
	}{
		{name: "main checkout", dir: repo},
		{name: "main subdirectory", dir: filepath.Join(repo, "src")},
		{name: "worktree root", dir: worktree, want: canonicalWorktree},
		{name: "missing worktree subdirectory", dir: filepath.Join(worktree, "new", "dir"), want: canonicalWorktree},
		{name: "relative worktree path", dir: filepath.Join(".claude", "worktrees", "w1"), want: canonicalWorktree},
		{name: "empty", dir: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := LinkedWorktreeRoot(repo, test.dir)
			if err != nil || got != test.want {
				t.Fatalf("LinkedWorktreeRoot = %q, %v; want %q", got, err, test.want)
			}
		})
	}
	// From inside the worktree, the main checkout is not a linked worktree of it.
	if got, err := LinkedWorktreeRoot(worktree, repo); err != nil || got != "" {
		t.Fatalf("main checkout seen from the worktree = %q, %v", got, err)
	}

	// A submodule-style gitlink has no commondir and never matches.
	submodule := filepath.Join(repo, "vendor", "lib")
	modules := filepath.Join(repo, ".git", "modules", "lib")
	if err := os.MkdirAll(submodule, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(modules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(submodule, ".git"), []byte("gitdir: ../../.git/modules/lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := LinkedWorktreeRoot(repo, submodule); err != nil || got != "" {
		t.Fatalf("submodule matched as a linked worktree: %q, %v", got, err)
	}

	// A worktree of another repository never matches.
	other := t.TempDir()
	gitInitHelper(t, other)
	foreign := filepath.Join(repo, "foreign-wt")
	if out, err := exec.Command("git", "-C", other, "worktree", "add", "-q", foreign, "-b", "foreign").CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if got, err := LinkedWorktreeRoot(repo, foreign); err != nil || got != "" {
		t.Fatalf("another repository's worktree matched: %q, %v", got, err)
	}
	if reason, err := linkedWorktreeWriteBlock(repo, []string{filepath.Join(worktree, "a.go"), filepath.Join(repo, "b.go")}); err != nil || reason == "" {
		t.Fatalf("cross-checkout write was not blocked: %q, %v", reason, err)
	}
	if reason, err := linkedWorktreeWriteBlock(repo, []string{filepath.Join(repo, "b.go"), filepath.Join(foreign, "c.go")}); err != nil || reason != "" {
		t.Fatalf("main-checkout writes were blocked: %q, %v", reason, err)
	}
}
