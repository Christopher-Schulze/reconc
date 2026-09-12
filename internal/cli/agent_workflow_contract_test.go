package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentReadOnlyEntryPreservesUninitializedAndStaleRepositories(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "uninitialized"
		if stale {
			name = "stale-policy"
		}
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			if stale {
				repo = makeCheckRepo(t, "rules: []\n")
			}
			if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Review only\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			stateRoot := t.TempDir()
			t.Setenv("RECONC_HOME", stateRoot)
			before := briefingFilesystemInventory(t, repo, stateRoot)
			for _, arguments := range [][]string{
				{"agent-intro", "--json"},
				{"agent-intro", "--section", "integration-surfaces", "--json"},
				{"session-briefing", repo, "--json"},
			} {
				var stdout, stderr bytes.Buffer
				if err := Run(arguments, "test", &stdout, &stderr); err != nil || !json.Valid(stdout.Bytes()) {
					t.Fatalf("read-only entry %v: %v; %s; %s", arguments, err, stdout.String(), stderr.String())
				}
				if arguments[0] == "session-briefing" {
					var report struct {
						PolicyDelta string `json:"policy_delta"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
						t.Fatal(err)
					}
					want := "compiled lockfile not found"
					if stale {
						want = "source_digest does not match"
					}
					if !strings.Contains(report.PolicyDelta, want) {
						t.Fatalf("expected %s policy, got %s", want, report.PolicyDelta)
					}
				}
			}
			after := briefingFilesystemInventory(t, repo, stateRoot)
			if len(before) != len(after) {
				t.Fatalf("read-only entry changed file membership: %d -> %d", len(before), len(after))
			}
			for path, want := range before {
				if got, ok := after[path]; !ok || got != want {
					t.Fatalf("read-only entry changed %s: %+v -> %+v", path, want, got)
				}
			}
		})
	}
}

func TestAgentProofExportDestinationControlsCandidateBinding(t *testing.T) {
	for _, test := range []struct {
		name       string
		insideRepo bool
		wantStatus string
		wantExit   int
	}{
		{"outside", false, "valid", 0},
		{"inside", true, "candidate-mismatch", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := makeCheckRepo(t, "rules: []\n")
			initGitRepo(t, repo)
			commitProofVerifyFixture(t, repo)
			outputRoot := t.TempDir()
			if test.insideRepo {
				outputRoot = repo
			}
			output := filepath.Join(outputRoot, "review proof.json")
			var stdout, stderr bytes.Buffer
			if err := Run([]string{"proof", repo, "--output", output}, "test", &stdout, &stderr); err != nil {
				t.Fatalf("export: %v; %s", err, stdout.String())
			}
			stdout.Reset()
			err := Run([]string{"proof", "verify", output, "--repo", repo, "--json"}, "test", &stdout, &stderr)
			if ExitCode(err) != test.wantExit {
				t.Fatalf("verify exit %d, want %d: %v; %s", ExitCode(err), test.wantExit, err, stdout.String())
			}
			var report proofVerificationReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Status != test.wantStatus || !report.IntegrityValid || report.LocalCandidateMatch == nil || *report.LocalCandidateMatch == test.insideRepo {
				t.Fatalf("candidate binding: %+v", report)
			}
		})
	}
}
