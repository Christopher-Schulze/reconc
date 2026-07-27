package commandproof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureCurrentAllowsDirtyWorkingTree(t *testing.T) {
	repo := newProofRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("work\n"), 0o600); err != nil {
		t.Fatalf("write untracked fixture: %v", err)
	}
	got, err := CaptureCurrent(repo)
	if err != nil {
		t.Fatalf("CaptureCurrent rejected dirty working tree: %v", err)
	}
	if got.RepoRoot == "" || got.Head == "" || got.IndexTree == "" {
		t.Fatalf("incomplete snapshot: %+v", got)
	}
}

func TestVerifyStagedCleanBindsCommandToCandidate(t *testing.T) {
	repo := newProofRepo(t)
	stageFile(t, repo, "candidate.txt", "first\n")
	before, err := CaptureStagedClean(repo)
	if err != nil {
		t.Fatalf("CaptureStagedClean: %v", err)
	}
	if err := VerifyStagedClean(before); err != nil {
		t.Fatalf("unchanged candidate rejected: %v", err)
	}

	stageFile(t, repo, "candidate.txt", "second\n")
	err = VerifyStagedClean(before)
	if err == nil || !strings.Contains(err.Error(), "changed while the command ran") {
		t.Fatalf("expected candidate-drift rejection, got %v", err)
	}
}

func TestValidateProofRejectsEveryInvalidContractField(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	snapshot := Snapshot{RepoRoot: "/repo", Head: "head", IndexTree: "tree"}
	valid := Proof{
		Schema:        proofSchema,
		Scope:         proofScope,
		RepoRoot:      snapshot.RepoRoot,
		Head:          snapshot.Head,
		IndexTree:     snapshot.IndexTree,
		Command:       "go test ./...",
		ExecutionMode: "direct",
		Outcome:       "success",
		ExitCode:      0,
		StartedAt:     now.Add(-time.Minute),
		CompletedAt:   now,
	}
	if err := validateProof(valid, snapshot, now, time.Hour); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Proof)
		err    string
		maxAge time.Duration
	}{
		{name: "schema", mutate: func(p *Proof) { p.Schema = "other" }, err: "schema"},
		{name: "scope", mutate: func(p *Proof) { p.Scope = "other" }, err: "scope"},
		{name: "repository", mutate: func(p *Proof) { p.RepoRoot = "/other" }, err: "staged state"},
		{name: "head", mutate: func(p *Proof) { p.Head = "other" }, err: "staged state"},
		{name: "index tree", mutate: func(p *Proof) { p.IndexTree = "other" }, err: "staged state"},
		{name: "empty command", mutate: func(p *Proof) { p.Command = " \t" }, err: "command is empty"},
		{name: "execution mode", mutate: func(p *Proof) { p.ExecutionMode = "indirect" }, err: "mode is invalid"},
		{name: "outcome", mutate: func(p *Proof) { p.Outcome = "failure" }, err: "not a successful"},
		{name: "exit code", mutate: func(p *Proof) { p.ExitCode = 1 }, err: "not a successful"},
		{name: "zero start", mutate: func(p *Proof) { p.StartedAt = time.Time{} }, err: "timestamps"},
		{name: "zero completion", mutate: func(p *Proof) { p.CompletedAt = time.Time{} }, err: "timestamps"},
		{name: "reverse timestamps", mutate: func(p *Proof) { p.CompletedAt = p.StartedAt.Add(-time.Second) }, err: "timestamps"},
		{name: "future completion", mutate: func(p *Proof) { p.CompletedAt = now.Add(61 * time.Second) }, err: "in the future"},
		{name: "expired", mutate: func(p *Proof) {
			p.StartedAt = now.Add(-3 * time.Hour)
			p.CompletedAt = now.Add(-2 * time.Hour)
		}, err: "expired", maxAge: time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			proof := valid
			test.mutate(&proof)
			maxAge := test.maxAge
			if maxAge == 0 {
				maxAge = time.Hour
			}
			err := validateProof(proof, snapshot, now, maxAge)
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("expected %q error, got %v", test.err, err)
			}
		})
	}
}

func TestLoadCurrentSuccessesIgnoresNonProofEntries(t *testing.T) {
	repo := newProofRepo(t)
	snapshot, err := CaptureCurrent(repo)
	if err != nil {
		t.Fatalf("CaptureCurrent: %v", err)
	}
	dir := proofDir(snapshot.RepoRoot)
	if err := os.MkdirAll(filepath.Join(dir, "directory.json"), 0o700); err != nil {
		t.Fatalf("create directory fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("not a proof"), 0o600); err != nil {
		t.Fatalf("create extension fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("create malformed fixture: %v", err)
	}

	proofs, err := LoadCurrentSuccesses(repo, time.Now())
	if err != nil {
		t.Fatalf("LoadCurrentSuccesses: %v", err)
	}
	if len(proofs) != 0 {
		t.Fatalf("non-proof entries became evidence: %+v", proofs)
	}
}
