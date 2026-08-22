package tasklifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoneVisiblePresenceIsValidated(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want int
		err  string
	}{
		{name: "absent", yaml: "task_lifecycle:\n  profile: sections-v1\n", want: defaultDoneVisible},
		{name: "explicit zero", yaml: "task_lifecycle:\n  profile: sections-v1\n  done_visible: 0\n", err: "between 1 and 1000"},
		{name: "negative", yaml: "task_lifecycle:\n  profile: sections-v1\n  done_visible: -1\n", err: "between 1 and 1000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(test.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(repo)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("LoadConfig error = %v, want %q", err, test.err)
				}
				return
			}
			if err != nil || cfg.DoneVisible != test.want {
				t.Fatalf("LoadConfig = %+v, %v; want done_visible=%d", cfg, err, test.want)
			}
		})
	}
}

func TestTaskSnapshotRejectsEveryReferencedFileMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{
			name: "content",
			mutate: func(t *testing.T, path, _ string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing",
			mutate: func(t *testing.T, path, _ string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "replaced with identical bytes",
			mutate: func(t *testing.T, path, body string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink replacement",
			mutate: func(t *testing.T, path, body string) {
				t.Helper()
				external := filepath.Join(t.TempDir(), "external.md")
				if err := os.WriteFile(external, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			overviewPath := filepath.Join(repo, "docs", "tasks.md")
			detailPath := filepath.Join(repo, "docs", "tasks", "001-current.md")
			detailBody := "# TASK 001: Current\n"
			writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n"))
			writeFile(t, repo, "docs/tasks/001-current.md", []byte(detailBody))
			overview, err := captureTaskFileSnapshot(overviewPath, true)
			if err != nil {
				t.Fatal(err)
			}
			detail, err := captureTaskFileSnapshot(detailPath, true)
			if err != nil {
				t.Fatal(err)
			}
			pathGuard := newTaskPathGuard(repo, 8)
			if err := pathGuard.reject(overviewPath); err != nil {
				t.Fatal(err)
			}
			if err := pathGuard.reject(detailPath); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, detailPath, detailBody)
			issues := concurrentReadIssues(repo, defaultConfig(), []taskFileSnapshot{overview, detail}, pathGuard)
			if len(issues) != 1 || issues[0].ID != "task/read/concurrent-mutation" {
				t.Fatalf("mutation issues = %+v", issues)
			}
		})
	}
}

func TestTaskSnapshotRejectsTransactionAppearingAfterRead(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks.md", []byte("# Tasks\n"))
	overview, err := captureTaskFileSnapshot(filepath.Join(repo, "docs", "tasks.md"), true)
	if err != nil {
		t.Fatal(err)
	}
	pathGuard := newTaskPathGuard(repo, 4)
	if err := pathGuard.reject(filepath.Join(repo, "docs", "tasks.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, transactionRel, []byte("{}"))
	issues := concurrentReadIssues(repo, defaultConfig(), []taskFileSnapshot{overview}, pathGuard)
	if len(issues) != 1 || issues[0].ID != "task/read/concurrent-mutation" {
		t.Fatalf("transaction mutation issues = %+v", issues)
	}
}

func TestRollbackPathResolutionFailsBeforeMutation(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "docs/tasks.md", []byte("after\n"))
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "unsafe.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	journal := transaction{
		FormatVersion: transactionVersion,
		Action:        "test",
		Files: []transactionFile{
			{Path: "docs/tasks.md", Before: []byte("before\n"), BeforeMode: 0o644, BeforeHash: hashContent([]byte("before\n")), AfterHash: hashContent([]byte("after\n"))},
			{Path: "unsafe.md", Before: []byte("outside\n"), BeforeMode: 0o644, BeforeHash: hashContent([]byte("outside\n")), AfterHash: hashContent([]byte("outside\n"))},
		},
	}
	if err := rollbackTransaction(repo, journal); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("unsafe rollback error = %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, "docs", "tasks.md"))
	if err != nil || string(body) != "after\n" {
		t.Fatalf("failed preflight mutated safe path: body=%q err=%v", body, err)
	}
	if body, err := os.ReadFile(outside); err != nil || string(body) != "outside\n" {
		t.Fatalf("failed preflight mutated symlink target: body=%q err=%v", body, err)
	}
	move := transactionMove{Source: "unsafe.md", Destination: "docs/tasks/done/unsafe.md"}
	if err := rollbackTransactionMove(repo, move, journal.Files[1]); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("rollback move ignored unsafe source path: %v", err)
	}
}
