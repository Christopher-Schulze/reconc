package policyauthor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
)

const validCandidate = `rules:
  - id: protect-generated
    kind: deny_write
    paths: ["dist/**"]
    mode: warn
    message: generated files are immutable
`

func TestPrepareValidatesAndExplainsWithoutMutation(t *testing.T) {
	repo := authorRepo(t)
	target := filepath.Join(repo, filepath.FromSlash(DefaultTarget))
	preview, err := Prepare(Request{
		Repo: repo, Version: "test", CandidateKind: "file",
		CandidateName: "candidate.yml", Body: []byte(validCandidate),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Validation.SchemaValid || !preview.Validation.CompileValid || !preview.Validation.Ready ||
		preview.RepoRoot != "." || preview.Target != DefaultTarget ||
		len(preview.Explanation.Rules) != 1 || len(preview.Explanation.RuleKinds) != 1 ||
		preview.Explanation.RuleKinds[0].Kind != "deny_write" || len(preview.LockfileBytes()) == 0 {
		t.Fatalf("preview = %+v", preview)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview mutated target: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(ingest.LockfilePath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview mutated lock: %v", err)
	}

	badSchema := Request{Repo: repo, Version: "test", CandidateKind: "file", CandidateName: "bad", Body: []byte("unknown: true\n")}
	if _, err := Prepare(badSchema); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("schema error = %v", err)
	}
	badCompile := Request{Repo: repo, Version: "test", CandidateKind: "file", CandidateName: "bad", Body: []byte("extends: [definitely-missing]\nrules: []\n")}
	if _, err := Prepare(badCompile); err == nil || !strings.Contains(err.Error(), "compile candidate") {
		t.Fatalf("compile error = %v", err)
	}
}

func TestPrepareReportsConflictsAndApplyRefusesThem(t *testing.T) {
	repo := authorRepo(t)
	body := []byte(`rules:
  - id: first
    kind: deny_write
    paths: ["dist/**"]
    mode: warn
    message: first
  - id: second
    kind: deny_write
    paths: ["dist/**"]
    mode: warn
    message: second
`)
	request := Request{Repo: repo, Version: "test", CandidateKind: "file", CandidateName: "conflict", Body: body}
	preview, err := Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Validation.Ready || len(preview.Explanation.Conflicts) != 1 {
		t.Fatalf("conflict preview = %+v", preview)
	}
	if _, err := Apply(request, preview); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("apply conflict error = %v", err)
	}
}

func TestPrepareUsesRealPresetAndTemplateExpansion(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("extends: [default]\nrules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := []byte(`rules:
  - id: tests-track-source
    template: tests-follow-source
    paths: ["src/**"]
    when_paths: ["tests/**"]
`)
	preview, err := Prepare(Request{
		Repo: repo, Version: "test", CandidateKind: "file",
		CandidateName: "template", Body: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(preview.Explanation.EffectivePacks, []string{"default"}) || len(preview.Explanation.Rules) != 2 {
		t.Fatalf("preset/template explanation = %+v", preview.Explanation)
	}
	found := false
	for _, rule := range preview.Explanation.Rules {
		if rule.ID == "tests-track-source" && rule.Kind == "couple_change" && rule.Mode == "warn" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expanded template rule missing: %+v", preview.Explanation.Rules)
	}
}

func TestApplyPublishesExactPreviewAndValidatesRuntime(t *testing.T) {
	repo := authorRepo(t)
	request := Request{Repo: repo, Version: "test", CandidateKind: "file", CandidateName: "candidate", Body: []byte(validCandidate)}
	preview, err := Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := Apply(request, preview)
	if err != nil {
		t.Fatal(err)
	}
	if !adoption.Requested || !adoption.Confirmed || !adoption.Applied || adoption.RolledBack || adoption.LockSHA256 == "" {
		t.Fatalf("adoption = %+v", adoption)
	}
	targetBody, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(DefaultTarget)))
	if err != nil || !bytes.Equal(targetBody, request.Body) {
		t.Fatalf("target body differs: %v", err)
	}
	lockBody, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(ingest.LockfilePath)))
	if err != nil || !bytes.Equal(lockBody, preview.LockfileBytes()) {
		t.Fatalf("lock body differs: %v", err)
	}
}

func TestApplyRejectsCandidateDriftBeforeMutation(t *testing.T) {
	repo := authorRepo(t)
	request := Request{Repo: repo, Version: "test", CandidateKind: "file", CandidateName: "candidate", Body: []byte(validCandidate)}
	preview, err := Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Body = []byte("rules: []\n")
	if _, err := Apply(request, preview); err == nil || !strings.Contains(err.Error(), "changed after preview") {
		t.Fatalf("candidate drift error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(DefaultTarget))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate drift mutated target: %v", err)
	}
}

func TestApplyRollsBackTargetAndLockAfterVerificationFailure(t *testing.T) {
	repo := authorRepo(t)
	target := filepath.Join(repo, filepath.FromSlash(DefaultTarget))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	originalTarget := []byte("rules: []\n")
	if err := os.WriteFile(target, originalTarget, 0o640); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(repo, filepath.FromSlash(ingest.LockfilePath))
	originalLock, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Repo: repo, Version: "test", CandidateKind: "file", CandidateName: "candidate", Body: []byte(validCandidate)}
	preview, err := Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := apply(request, preview, func(string) error { return errors.New("injected verification failure") })
	if err == nil || !strings.Contains(err.Error(), "injected verification failure") || !adoption.RolledBack || adoption.Applied {
		t.Fatalf("adoption=%+v error=%v", adoption, err)
	}
	gotTarget, targetErr := os.ReadFile(target)
	gotLock, lockErr := os.ReadFile(lockPath)
	info, infoErr := os.Stat(target)
	if targetErr != nil || lockErr != nil || infoErr != nil {
		t.Fatalf("read rollback target/lock: %v/%v/%v", targetErr, lockErr, infoErr)
	}
	if !bytes.Equal(gotTarget, originalTarget) || !bytes.Equal(gotLock, originalLock) ||
		info.Mode().Perm() != originalInfo.Mode().Perm() {
		t.Fatalf("rollback target=%q lock_equal=%t mode=%v want_mode=%v", gotTarget, bytes.Equal(gotLock, originalLock), info.Mode(), originalInfo.Mode())
	}
}

func TestApplyRollbackRemovesItsNewPolicyDirectory(t *testing.T) {
	repo := authorRepo(t)
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	request := Request{Repo: repo, Version: "test", CandidateKind: "file", CandidateName: "candidate", Body: []byte(validCandidate)}
	preview, err := Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := apply(request, preview, func(string) error { return errors.New("injected verification failure") })
	if err == nil || !adoption.RolledBack {
		t.Fatalf("adoption=%+v error=%v", adoption, err)
	}
	if _, err := os.Lstat(filepath.Join(repo, "policies")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback retained owned policy directory: %v", err)
	}
}

func TestPrepareRejectsUnsafeTargets(t *testing.T) {
	repo := authorRepo(t)
	for _, target := range []string{"../policy.yml", "/tmp/policy.yml", "nested/policies/x.yml", "policies/x.json", "policies/a/b.yml"} {
		request := Request{Repo: repo, Version: "test", Target: target, CandidateKind: "file", CandidateName: "candidate", Body: []byte(validCandidate)}
		if _, err := Prepare(request); err == nil {
			t.Fatalf("unsafe target %q accepted", target)
		}
	}
	if runtime.GOOS != "windows" {
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(repo, "policies")); err != nil {
			t.Fatal(err)
		}
		request := Request{Repo: repo, Version: "test", CandidateKind: "file", CandidateName: "candidate", Body: []byte(validCandidate)}
		if _, err := Prepare(request); err == nil || !strings.Contains(err.Error(), "escapes") {
			t.Fatalf("symlink target error = %v", err)
		}
	}
}

func authorRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("default_mode: warn\nrules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}
