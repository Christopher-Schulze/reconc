package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/adopt"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policyauthor"
)

const policyAuthorCandidate = `rules:
  - id: author-protect-generated
    kind: deny_write
    paths: ["dist/**"]
    mode: warn
    message: generated files are immutable
`

func TestPolicyAuthorNonTerminalPreviewAndJSONNeverPrompt(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	candidate := writePolicyAuthorCandidate(t)
	for _, test := range []struct {
		name     string
		terminal bool
		json     bool
	}{
		{name: "non-terminal text"},
		{name: "terminal JSON", terminal: true, json: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []string{repo, "--candidate", candidate}
			if test.json {
				args = append(args, "--json")
			}
			var output bytes.Buffer
			if err := runPolicyAuthor(args, "test", unreadablePolicyAuthorInput{}, test.terminal, &output); err != nil {
				t.Fatal(err)
			}
			if test.json {
				var report policyAuthorReport
				if err := json.Unmarshal(output.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if !report.Preview.Validation.Ready || report.Adoption.Requested {
					t.Fatalf("JSON report = %+v", report)
				}
			} else if !strings.Contains(output.String(), "Preview only; repository unchanged") {
				t.Fatalf("text output = %q", output.String())
			}
			assertPolicyAuthorTargetAbsent(t, repo)
		})
	}
}

func TestPolicyAuthorTerminalDetectionRejectsCharacterDevices(t *testing.T) {
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	if inputIsTerminal(input) {
		t.Fatal("non-terminal character device was treated as an interactive terminal")
	}
}

func TestPolicyAuthorDeclineAndEOFCancelPreserveTargetAndLockIdentity(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
	}{
		{name: "decline", input: "no\n"},
		{name: "EOF cancellation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := makeCheckRepo(t, "rules: []\n")
			target := filepath.Join(repo, filepath.FromSlash(policyauthor.DefaultTarget))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("rules: []\n"), 0o640); err != nil {
				t.Fatal(err)
			}
			if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(repo, filepath.FromSlash(ingest.LockfilePath))
			targetBefore, targetBody := policyAuthorSnapshot(t, target)
			lockBefore, lockBody := policyAuthorSnapshot(t, lockPath)
			var output bytes.Buffer
			err := runPolicyAuthor([]string{repo, "--candidate", writePolicyAuthorCandidate(t)}, "test", strings.NewReader(test.input), true, &output)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "declined; repository unchanged") {
				t.Fatalf("decline output = %q", output.String())
			}
			targetAfter, targetAfterBody := policyAuthorSnapshot(t, target)
			lockAfter, lockAfterBody := policyAuthorSnapshot(t, lockPath)
			if !os.SameFile(targetBefore, targetAfter) || !os.SameFile(lockBefore, lockAfter) ||
				!bytes.Equal(targetBody, targetAfterBody) || !bytes.Equal(lockBody, lockAfterBody) {
				t.Fatal("declined adoption changed target or lock bytes/identity")
			}
		})
	}
}

func TestPolicyAuthorExplicitAndInteractiveApply(t *testing.T) {
	for _, test := range []struct {
		name  string
		args  []string
		input string
	}{
		{name: "explicit JSON", args: []string{"--apply", "--json"}},
		{name: "interactive yes", input: "yes\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := makeCheckRepo(t, "rules: []\n")
			args := append([]string{repo, "--candidate", writePolicyAuthorCandidate(t)}, test.args...)
			var output bytes.Buffer
			if err := runPolicyAuthor(args, "test", strings.NewReader(test.input), test.input != "", &output); err != nil {
				t.Fatal(err)
			}
			targetBody, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(policyauthor.DefaultTarget)))
			if err != nil || string(targetBody) != policyAuthorCandidate {
				t.Fatalf("adopted target = %q, %v", targetBody, err)
			}
			if _, err := runtimePolicyAuthorLock(repo); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), `"applied": true`) && !strings.Contains(output.String(), "Adopted atomically") {
				t.Fatalf("apply output = %q", output.String())
			}
		})
	}
}

func TestPolicyAuthorDetectedAndImpactReportsAreBounded(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.test/author\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var detected bytes.Buffer
	if err := runPolicyAuthor([]string{repo, "--detected", "--json"}, "test", unreadablePolicyAuthorInput{}, true, &detected); err != nil {
		t.Fatal(err)
	}
	var detectedReport policyAuthorReport
	if err := json.Unmarshal(detected.Bytes(), &detectedReport); err != nil {
		t.Fatal(err)
	}
	if detectedReport.Detection == nil || detectedReport.Detection.RepoRoot != "." ||
		detectedReport.Preview.CandidateKind != "detected" || !detectedReport.Preview.Validation.Ready {
		t.Fatalf("detected report = %+v", detectedReport)
	}

	var impact bytes.Buffer
	if err := runPolicyAuthor([]string{
		repo, "--candidate", writePolicyAuthorCandidate(t), "--write", "dist/output.js", "--json",
	}, "test", unreadablePolicyAuthorInput{}, true, &impact); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(impact.Bytes(), []byte(policyAuthorCandidate)) {
		t.Fatalf("author JSON leaked candidate body: %s", impact.Bytes())
	}
	var impactReport policyAuthorReport
	if err := json.Unmarshal(impact.Bytes(), &impactReport); err != nil {
		t.Fatal(err)
	}
	if impactReport.Impact == nil || impactReport.Impact.Summary.NewlyWarningCases != 1 ||
		impactReport.Preview.CandidateSHA256 == "" {
		t.Fatalf("impact report = %+v", impactReport)
	}
}

func TestDetectedPolicyYAMLUsesStructuredSuggestions(t *testing.T) {
	report := adopt.Report{
		RepoRoot: "repository  - id: injected",
		Suggestions: []adopt.Suggestion{{
			ID: "real-rule", Kind: "deny_write", Mode: "warn", Message: "generated files are immutable",
			Paths: []string{"dist/**"}, Evidence: []string{"dist/"}, Reason: "dist exists",
		}},
	}
	body, err := detectedPolicyYAML(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "injected") || !strings.HasPrefix(string(body), "rules:\n  - id: real-rule\n") {
		t.Fatalf("structured detected policy = %q", body)
	}
}

type unreadablePolicyAuthorInput struct{}

func (unreadablePolicyAuthorInput) Read([]byte) (int, error) {
	return 0, errors.New("policy author unexpectedly read non-interactive input")
}

func writePolicyAuthorCandidate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "candidate.yml")
	if err := os.WriteFile(path, []byte(policyAuthorCandidate), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func policyAuthorSnapshot(t *testing.T, path string) (os.FileInfo, []byte) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return info, body
}

func assertPolicyAuthorTargetAbsent(t *testing.T, repo string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(policyauthor.DefaultTarget))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("policy author preview created target: %v", err)
	}
}

func runtimePolicyAuthorLock(repo string) ([]byte, error) {
	path := filepath.Join(repo, filepath.FromSlash(ingest.LockfilePath))
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return body, nil
}
