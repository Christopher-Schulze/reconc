package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIdentityRequiresExplicitExistingTagAtCleanHEAD(t *testing.T) {
	root := publicSurfaceRoot(t)
	repository := t.TempDir()
	runGit(t, repository, "init", "--quiet")
	runGit(t, repository, "config", "user.name", "Reconc Test")
	runGit(t, repository, "config", "user.email", "reconc-test@example.invalid")
	writeAuditFixture(t, repository, "source.txt", "source\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "--quiet", "-m", "fixture source")
	// Synthetic versions exercise tag parsing only in this isolated repository.
	const tag = "reconc-v12.34.56"
	runGit(t, repository, "tag", "-a", tag, "-m", "fixture release")
	head, err := exec.Command("git", "-C", repository, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	development := "dev+" + strings.TrimSpace(string(head))[:12]
	resolve := func(tag string, want string, failure string) {
		t.Helper()
		command := exec.Command("bash", filepath.Join(root, "scripts/build/resolve-version.sh"), repository)
		command.Env = append(os.Environ(), "RELEASE_TAG="+tag)
		output, err := command.CombinedOutput()
		if failure != "" {
			if err == nil || !strings.Contains(string(output), failure) {
				t.Fatalf("resolve %q = %q (%v), want failure %q", tag, output, err, failure)
			}
			return
		}
		if err != nil || strings.TrimSpace(string(output)) != want {
			t.Fatalf("resolve %q = %q (%v), want %q", tag, output, err, want)
		}
	}
	resolve("", development, "")
	resolve(tag, "12.34.56", "")
	for _, invalid := range []string{"main", "reconc-v01.2.3", "reconc-v1.2.3-preview.1", "reconc-v1.2.3;false"} {
		resolve(invalid, "", "exact stable")
	}
	resolve("reconc-v98.76.54", "", "does not exist")
	writeAuditFixture(t, repository, "untracked.txt", "untracked\n")
	resolve("", development+"-dirty", "")
	resolve(tag, "", "tracked or untracked changes")
	runGit(t, repository, "add", "untracked.txt")
	resolve(tag, "", "tracked or untracked changes")
	runGit(t, repository, "commit", "--quiet", "-m", "later source")
	resolve(tag, "", "does not identify HEAD")
	runGit(t, repository, "tag", "reconc-v98.76.54")
	resolve("reconc-v98.76.54", "98.76.54", "")
}
