package proofbundle_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/commandproof"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/policyproof"
	"reconc.dev/reconc/internal/proofbundle"
	"reconc.dev/reconc/internal/retention"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestPassBundleIsDeterministicPortableAndVerifiable(t *testing.T) {
	repo := proofRepo(t, "rules: []\n", nil)
	t.Setenv("RECONC_SCHEMA_BASE_URL", "https://private.example.invalid/schema")
	first, err := proofbundle.Generate(repo, "0.8.6-test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := proofbundle.Generate(repo, "0.8.6-test")
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := proofbundle.MarshalJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := proofbundle.MarshalJSON(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("unchanged repository emitted non-deterministic proof bytes")
	}
	if !first.OK || first.Decision != "pass" || first.RepoRoot != "." || first.Task.Configured {
		t.Fatalf("unexpected pass bundle: %#v", first)
	}
	if len(first.Evidence.SatisfiedChecks) == 0 {
		t.Fatal("passing completion checks were not bound as satisfied evidence")
	}
	if strings.Contains(first.Schema, "private.example.invalid") {
		t.Fatalf("environment-specific schema URL leaked into portable proof: %s", first.Schema)
	}
	assertPrivateTextAbsent(t, firstJSON, repo)
	var markdown bytes.Buffer
	if err := proofbundle.RenderMarkdown(&markdown, first); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"# Reconc Proof Bundle", "Decision: **PASS**", first.Digest, first.Candidate.Fingerprint, "No current command proof receipts"} {
		if !strings.Contains(markdown.String(), expected) {
			t.Errorf("Markdown omitted %q", expected)
		}
	}
	first.Decision = "block"
	if err := proofbundle.Verify(first); err == nil {
		t.Fatal("tampered proof bundle passed verification")
	}
}

func TestUnbornGitCandidateIsPortableAndVerifiable(t *testing.T) {
	repo := proofRepo(t, "rules: []\n", nil)
	git(t, repo, "init", "-q")
	git(t, repo, "config", "user.name", "reconc-test")
	git(t, repo, "config", "user.email", "reconc-test@example.com")
	bundle, err := proofbundle.Generate(repo, "0.8.6-test")
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.Candidate.GitAvailable || bundle.Candidate.GitHead != "UNBORN" {
		t.Fatalf("unexpected unborn candidate: %#v", bundle.Candidate)
	}
	if err := proofbundle.Verify(bundle); err != nil {
		t.Fatalf("Verify() = %v", err)
	}
}

func TestBlockedTypedTaskAndSecretsAreSafeToShare(t *testing.T) {
	repo := proofRepo(t, "rules: []\n", map[string]string{
		".reconc.yml":             "task_lifecycle:\n  profile: sections-v1\n  completion:\n    required_evidence_fields: [Tests]\n",
		"docs/tasks.md":           "# TASK Control Plane\n\n## Active\n\n- [~] 001 Proof -> tasks/001-proof.md\n\n## Queue\n\n## Blocked\n\n## Done\n",
		"docs/tasks/001-proof.md": "# TASK 001: Proof\n\n## Why\n\nProve.\n\n## Acceptance\n\n- Complete.\n\n## Sub-Tasks\n\n- [~] Finish token=supersecret.\n\n## Evidence\n\n- Tests:\n\n## Notes\n\nNone.\n\n## Deviations\n\nNone.\n",
	})
	bundle, err := proofbundle.Generate(repo, "0.8.6-test")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.OK || bundle.Decision != "block" || bundle.Task.ID != "001" || bundle.Task.State != "active" || bundle.NextAction == "" {
		t.Fatalf("typed TASK block was not preserved: %#v", bundle)
	}
	body, err := proofbundle.MarshalJSON(bundle)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivateTextAbsent(t, body, repo)
	if strings.Contains(string(body), "supersecret") {
		t.Fatalf("secret leaked into bundle: %s", body)
	}
}

func TestCommandProofIsCurrentBoundAndArgumentRedacted(t *testing.T) {
	policy := "rules:\n  - id: tests\n    kind: require_command_success\n    when_paths: [src/**]\n    commands: [go test ./... --token=supersecret]\n    mode: block\n    message: Tests required.\n"
	repo := proofRepo(t, policy, map[string]string{"src/main.go": "package main\n"})
	initGit(t, repo)
	writeFile(t, repo, "src/main.go", "package main\n\nconst version = 1\n")
	git(t, repo, "add", "src/main.go")
	snapshot, err := commandproof.CaptureStagedClean(repo)
	if err != nil {
		t.Fatal(err)
	}
	completed := time.Now().UTC()
	if _, err := commandproof.StoreSuccess(snapshot, "go test ./... --token=supersecret", "direct", completed.Add(-time.Second), completed); err != nil {
		t.Fatal(err)
	}
	bundle, err := proofbundle.Generate(repo, "0.8.6-test")
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.OK || len(bundle.Evidence.CommandProofs) != 1 {
		t.Fatalf("current command proof not accepted: %#v", bundle)
	}
	proof := bundle.Evidence.CommandProofs[0]
	if proof.Command != "go [arguments redacted]" || !proof.CandidateBound || !proof.Fresh || proof.Outcome != "success" {
		t.Fatalf("unsafe or incomplete command proof summary: %#v", proof)
	}
	body, _ := proofbundle.MarshalJSON(bundle)
	if strings.Contains(string(body), "supersecret") || strings.Contains(string(body), "--token") {
		t.Fatalf("command arguments leaked: %s", body)
	}
}

func TestCommandProofUsesEffectiveShellExecutableIdentity(t *testing.T) {
	repo := proofRepo(t, "rules: []\n", map[string]string{"src/main.go": "package main\n"})
	initGit(t, repo)
	writeFile(t, repo, "src/main.go", "package main\n\nconst version = 1\n")
	git(t, repo, "add", "src/main.go")
	snapshot, err := commandproof.CaptureStagedClean(repo)
	if err != nil {
		t.Fatal(err)
	}
	completed := time.Now().UTC()
	command := `PRIVATE_SECRET="raw secret" env TOKEN="hidden" sudo -u root /usr/local/bin/go test ./...`
	if _, err := commandproof.StoreSuccess(snapshot, command, "shell", completed.Add(-time.Second), completed); err != nil {
		t.Fatal(err)
	}
	bundle, err := proofbundle.Generate(repo, "0.8.6-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Evidence.CommandProofs) != 1 {
		t.Fatalf("command proofs = %#v", bundle.Evidence.CommandProofs)
	}
	proof := bundle.Evidence.CommandProofs[0]
	if proof.Command != "go [arguments redacted]" || proof.CommandHash != commandHash("go") {
		t.Fatalf("shell-derived command identity = %#v", proof)
	}
	body, err := proofbundle.MarshalJSON(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"raw secret", "hidden", "PRIVATE_SECRET", "TOKEN="} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("command proof leaked %q: %s", secret, body)
		}
	}
}

func TestStaleAndTamperedCommandProofsNeverBecomeEvidence(t *testing.T) {
	policy := "rules:\n  - id: tests\n    kind: require_command_success\n    when_paths: [src/**]\n    commands: [go test]\n    mode: block\n    message: Tests required.\n"
	for _, test := range []struct {
		name      string
		completed time.Time
		tamper    bool
	}{
		{name: "stale", completed: time.Now().UTC().Add(-365 * 24 * time.Hour)},
		{name: "tampered", completed: time.Now().UTC(), tamper: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := proofRepo(t, policy, map[string]string{"src/main.go": "package main\n"})
			initGit(t, repo)
			writeFile(t, repo, "src/main.go", "package main\n\nconst version = 1\n")
			git(t, repo, "add", "src/main.go")
			snapshot, err := commandproof.CaptureStagedClean(repo)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := commandproof.StoreSuccess(snapshot, "go test", "direct", test.completed.Add(-time.Second), test.completed); err != nil {
				t.Fatal(err)
			}
			if test.tamper {
				dir := filepath.Join(retention.ProjectDir(retention.ResolveStateRoot(), snapshot.RepoRoot), "command-proofs")
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("proof files: %v, %v", entries, err)
				}
				path := filepath.Join(dir, entries[0].Name())
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				body = bytes.Replace(body, []byte(`"outcome": "success"`), []byte(`"outcome": "failure"`), 1)
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			bundle, err := proofbundle.Generate(repo, "0.8.6-test")
			if err != nil {
				t.Fatal(err)
			}
			if bundle.OK || len(bundle.Evidence.CommandProofs) != 0 {
				t.Fatalf("invalid receipt became evidence: %#v", bundle)
			}
		})
	}
}

func TestSupersededBlockIsBoundToOlderCandidate(t *testing.T) {
	policy := "rules:\n  - id: deny-generated\n    kind: deny_write\n    paths: [gen/**]\n    mode: block\n    message: Generated output is blocked.\n"
	repo := proofRepo(t, policy, map[string]string{"src/main.go": "package main\n"})
	initGit(t, repo)
	state, err := agentsession.CaptureCompletionState(repo)
	if err != nil {
		t.Fatal(err)
	}
	inputs := runtime.Empty()
	inputs.WritePaths = []string{"gen/output.go"}
	blocked, err := runtime.CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := policyproof.Store(repo, "check", state.Fingerprint, blocked); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "src/main.go", "package main\n\nconst version = 2\n")
	bundle, err := proofbundle.Generate(repo, "0.8.6-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.SupersededBlocks) != 1 || bundle.SupersededBlocks[0].CandidateFingerprint != state.Fingerprint {
		t.Fatalf("older block was not classified as superseded: %#v", bundle.SupersededBlocks)
	}
}

func proofRepo(t *testing.T, policy string, files map[string]string) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# fixture\n")
	writeFile(t, repo, "policies/rules.yml", policy)
	for name, body := range files {
		writeFile(t, repo, name, body)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "proof-test"); err != nil {
		t.Fatalf("compile policy: %v", err)
	}
	return repo
}

func writeFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initGit(t *testing.T, repo string) {
	t.Helper()
	git(t, repo, "init", "-q")
	git(t, repo, "config", "user.name", "reconc-test")
	git(t, repo, "config", "user.email", "reconc-test@example.com")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "fixture")
}

func git(t *testing.T, repo string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}

func assertPrivateTextAbsent(t *testing.T, body []byte, repo string) {
	t.Helper()
	for _, private := range []string{repo, os.Getenv("HOME"), os.Getenv("USER"), "session_id", "prompt", "transcript"} {
		if private != "" && strings.Contains(string(body), private) {
			t.Errorf("private value %q leaked into proof", private)
		}
	}
}

func commandHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
