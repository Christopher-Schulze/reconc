package commandproof

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestValidateAndStoreRejectOversizedCommandProof(t *testing.T) {
	repo := newProofRepo(t)
	snapshot, err := CaptureStagedClean(repo)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := StoreSuccess(snapshot, strings.Repeat("x", maxProofSize), "direct", now, now); err == nil {
		t.Fatal("oversized command proof must not be published")
	}
}

func TestGitOutputBytesContextReturnsExactBytes(t *testing.T) {
	repo := newProofRepo(t)
	command := exec.Command("git", "rev-parse", "--verify", "HEAD")
	command.Dir = repo
	want, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	output, err := gitOutputBytesContext(t.Context(), repo, "rev-parse", "--verify", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, want) {
		t.Fatalf("git output = %q, want %q", output, want)
	}
}
