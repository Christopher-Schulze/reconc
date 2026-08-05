package cli

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckEmitsSARIFAndJUnitWithoutChangingDecisionExit(t *testing.T) {
	repo := makeCheckRepo(t, "rules:\n  - id: deny-src\n    kind: deny_write\n    paths: [src/**]\n    mode: block\n    message: blocked\n")
	for _, format := range []string{"sarif", "junit"} {
		t.Run(format, func(t *testing.T) {
			outputPath := filepath.Join(t.TempDir(), "nested", "report."+format)
			var stdout, stderr bytes.Buffer
			err := Run([]string{"check", repo, "--write", "src/a b.go", "--format", format, "--output", outputPath}, "test", &stdout, &stderr)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("%s exit = %d, %v", format, ExitCode(err), err)
			}
			body, readErr := os.ReadFile(outputPath)
			if readErr != nil || !bytes.Equal(body, stdout.Bytes()) || stderr.Len() != 0 {
				t.Fatalf("%s output file = %v, equal=%t, stderr=%q", format, readErr, bytes.Equal(body, stdout.Bytes()), stderr.String())
			}
			assertNativeReport(t, format, body, "block")
			if bytes.Contains(body, []byte(repo)) || bytes.Contains(body, []byte("Decision:")) {
				t.Fatalf("%s mixed or leaked output:\n%s", format, body)
			}
		})
	}
}

func TestCheckMachineFormatMapsStalePolicyToOperationalError(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte("rules: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"sarif", "junit"} {
		var stdout, stderr bytes.Buffer
		err := Run([]string{"check", repo, "--format", format}, "test", &stdout, &stderr)
		if err == nil || ExitCode(err) != 1 || stdout.Len() == 0 {
			t.Fatalf("%s stale exit = %d, %v, output=%q", format, ExitCode(err), err, stdout.String())
		}
		assertNativeOperationalError(t, format, stdout.Bytes())
		if bytes.Contains(stdout.Bytes(), []byte(repo)) || stderr.Len() != 0 {
			t.Fatalf("%s operational output leaked path or stderr: %s / %s", format, stdout.String(), stderr.String())
		}
	}
}

func TestCIEmitsGitBoundJUnitReport(t *testing.T) {
	repo := makeCheckRepo(t, "rules:\n  - id: deny-src\n    kind: deny_write\n    paths: [src/**]\n    mode: block\n    message: blocked\n")
	initGitRepo(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "add", "src/main.go")
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, output)
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"ci", repo, "--staged", "--format", "junit"}, "test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("ci exit = %d, %v", ExitCode(err), err)
	}
	assertNativeReport(t, "junit", stdout.Bytes(), "block")
	for _, expected := range []string{"reconc.git_mode", "staged", "reconc.candidate_fingerprint"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("JUnit omitted %q:\n%s", expected, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "Git:") || stderr.Len() != 0 {
		t.Fatalf("CI mixed human output: %s / %s", stdout.String(), stderr.String())
	}
}

func TestNativeReportOutputRefusesSymlinkAndFormatConflicts(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "report.sarif")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", repo, "--format", "sarif", "--output", link}, "test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("symlink output exit = %d, %v", ExitCode(err), err)
	}
	if body, readErr := os.ReadFile(target); readErr != nil || string(body) != "keep\n" {
		t.Fatalf("symlink target = %q, %v", body, readErr)
	}
	for _, args := range [][]string{
		{"check", repo, "--json", "--format", "sarif"},
		{"check", repo, "--format", "unknown"},
		{"ci", repo, "--staged", "--format", "terse"},
	} {
		if err := Run(args, "test", &stdout, &stderr); err == nil || ExitCode(err) != 1 {
			t.Fatalf("conflicting format %v accepted: %v", args, err)
		}
	}
}

func TestCheckAndCIHelpAdvertiseNativeFormats(t *testing.T) {
	for _, command := range []string{"check", "ci"} {
		var stdout, stderr bytes.Buffer
		if err := Run([]string{command, "--help"}, "test", &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"--format", "sarif", "junit", "--output"} {
			if !strings.Contains(stdout.String(), expected) {
				t.Errorf("%s help omitted %q", command, expected)
			}
		}
	}
}

func assertNativeReport(t *testing.T, format string, body []byte, decision string) {
	t.Helper()
	if format == "sarif" {
		var document struct {
			Version string `json:"version"`
			Runs    []struct {
				Properties struct {
					Decision string `json:"decision"`
				} `json:"properties"`
				Results []json.RawMessage `json:"results"`
			} `json:"runs"`
		}
		if err := json.Unmarshal(body, &document); err != nil || document.Version != "2.1.0" || len(document.Runs) != 1 || document.Runs[0].Properties.Decision != decision || len(document.Runs[0].Results) == 0 {
			t.Fatalf("SARIF contract = %+v, %v", document, err)
		}
		return
	}
	var document struct {
		XMLName  xml.Name `xml:"testsuites"`
		Failures int      `xml:"failures,attr"`
	}
	if err := xml.Unmarshal(body, &document); err != nil || document.XMLName.Local != "testsuites" || document.Failures == 0 {
		t.Fatalf("JUnit contract = %+v, %v", document, err)
	}
}

func assertNativeOperationalError(t *testing.T, format string, body []byte) {
	t.Helper()
	if format == "sarif" {
		if !bytes.Contains(body, []byte(`"executionSuccessful": false`)) || !bytes.Contains(body, []byte("operational-error")) {
			t.Fatalf("SARIF operational contract:\n%s", body)
		}
		return
	}
	if !bytes.Contains(body, []byte("<error")) || !bytes.Contains(body, []byte("reconc.operational")) {
		t.Fatalf("JUnit operational contract:\n%s", body)
	}
}
