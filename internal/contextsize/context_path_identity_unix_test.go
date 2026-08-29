//go:build !windows

package contextsize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanResolvesRepositoryAliasAndRejectsFileEscape(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "policy")
	alias := filepath.Join(t.TempDir(), "repo-alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	report, err := Scan(alias, []string{"AGENTS.md"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalBytes != int64(len("policy")) {
		t.Fatalf("aliased repository scan bytes=%d, want %d", report.TotalBytes, len("policy"))
	}

	outside := filepath.Join(t.TempDir(), "outside.md")
	mustWrite(t, outside, "external")
	if err := os.Symlink(outside, filepath.Join(repo, "linked.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(repo, []string{"linked.md"}, 100); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("external file identity was not rejected: %v", err)
	}
}

func TestScanAllowsInRootIntermediateSymlink(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "real", "AGENTS.md"), "inside")
	if err := os.Symlink("real", filepath.Join(repo, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	report, err := Scan(repo, []string{"linked/AGENTS.md"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalBytes != int64(len("inside")) || len(report.Files) != 1 || !report.Files[0].Exists {
		t.Fatalf("in-root intermediate symlink report = %+v", report)
	}
}

func TestScanRejectsFinalInRootSymlink(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "real.md"), "inside")
	if err := os.Symlink("real.md", filepath.Join(repo, "linked.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Scan(repo, []string{"linked.md"}, 100); err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
		t.Fatalf("final in-root symlink was accepted: %v", err)
	}
}

func TestScanRejectsEscapingIntermediateSymlink(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.md"), "secret")
	if err := os.Symlink(outside, filepath.Join(repo, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Scan(repo, []string{"linked/secret.md"}, 100); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("escaping intermediate symlink was not rejected: %v", err)
	}
}

func TestContextRootRejectsParentSymlinkReplacement(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(repo, "real", "context.md"), "inside")
	mustWrite(t, filepath.Join(outside, "context.md"), "outside")
	link := filepath.Join(repo, "linked")
	if err := os.Symlink("real", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}()
	replaced := false
	open := func(name string) (*os.File, error) {
		if !replaced {
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			replaced = true
		}
		return root.Open(name)
	}
	if _, _, err := contextFileInfoRoot(root, "linked/context.md", open); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("parent symlink replacement was accepted: %v", err)
	}
}
