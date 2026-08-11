package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/impactlab"
)

func TestImpactComparesCandidateWithoutMutatingRepository(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	candidate := filepath.Join(t.TempDir(), "candidate.yml")
	writeCLIFile(t, filepath.Dir(candidate), filepath.Base(candidate),
		"rules:\n  - id: candidate-deny\n    kind: deny_write\n    paths: [src/**]\n    mode: block\n    message: blocked\n")
	lockPath := filepath.Join(repo, compiler.LockfileRelativePath)
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "nested", "impact.json")
	var stdout, stderr bytes.Buffer
	err = Run([]string{
		"impact", repo, "--candidate", candidate, "--write", "src/main.go",
		"--json", "--output", outputPath,
	}, "test", &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := os.ReadFile(outputPath)
	if readErr != nil || !bytes.Equal(body, stdout.Bytes()) || stderr.Len() != 0 {
		t.Fatalf("impact output = %v, equal=%t, stderr=%q", readErr, bytes.Equal(body, stdout.Bytes()), stderr.String())
	}
	var report impactlab.Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.DecisionChanges != 1 || report.Summary.NewlyBlockingCases != 1 ||
		len(report.Cases) != 1 || report.Cases[0].Repository == nil || !report.Cases[0].Repository.DecisionChanged {
		t.Fatalf("impact report = %+v", report)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("impact mutated current lock: %v", err)
	}
	for _, absent := range []string{".reconc/audit.jsonl", ".reconc/run/state.bin"} {
		if _, err := os.Stat(filepath.Join(repo, absent)); !os.IsNotExist(err) {
			t.Fatalf("impact created %s: %v", absent, err)
		}
	}
}

func TestImpactExportRedactsAndImportsCorpus(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	corpusPath := filepath.Join(t.TempDir(), "corpus.json")
	var stdout, stderr bytes.Buffer
	err := Run([]string{
		"impact", "export", repo, "--write", "src/main.go",
		"--command", "curl --token sk-supersecretvalue https://example.test",
		"--complete", "all", "--output", corpusPath,
	}, "test", &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := os.ReadFile(corpusPath)
	if readErr != nil || !bytes.Equal(body, stdout.Bytes()) || bytes.Contains(body, []byte("supersecretvalue")) {
		t.Fatalf("corpus export = %v, equal=%t, body=%s", readErr, bytes.Equal(body, stdout.Bytes()), body)
	}
	corpus, err := impactlab.DecodeCorpus(body)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Completeness.CompleteReplay || corpus.Completeness.RedactionCount == 0 {
		t.Fatalf("corpus completeness = %+v", corpus.Completeness)
	}
	candidate := filepath.Join(t.TempDir(), "candidate.yml")
	if err := os.WriteFile(candidate, []byte("rules:\n  - id: docs-only\n    kind: deny_write\n    paths: [docs/**]\n    message: docs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := Run([]string{
		"impact", repo, "--candidate", candidate, "--corpus", corpusPath, "--json",
	}, "test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "unmatched in this corpus") ||
		!strings.Contains(stdout.String(), `"corpus_unmatched_rules"`) {
		t.Fatalf("imported impact output = %s", stdout.String())
	}
}

func TestImpactActionDeltaGateAndEveryReportFormat(t *testing.T) {
	repo := makeActionImpactCLIRepo(t)
	candidate := filepath.Join(t.TempDir(), "candidate.yml")
	writeCLIFile(t, filepath.Dir(candidate), filepath.Base(candidate), actionImpactCandidatePolicy)
	corpus, err := impactlab.NewCorpus(repo, []impactlab.Case{{
		ID: "database-staging", Kind: impactlab.CaseActionPre, Action: &impactlab.ActionCase{
			ToolID: "database-write",
			Request: impactlab.ActionRequestFixture{
				FormatVersion: action.RequestFormatVersion, CallID: "act_aaaaaaaaaaaaaaaaaaaaaaaaaa",
				Transport: action.TransportMCPStdio, ServerLabel: "database",
				ServerFingerprint:  actionImpactServerIdentity,
				Tool:               "execute",
				ToolContractDigest: actionImpactToolDigest,
				Phase:              action.PhasePreCall,
				RepositoryIdentity: actionImpactRepositoryIdentity,
				AuthorityMode:      action.AuthorityOperatorPinned,
				Arguments:          impactlab.ActionPayload(`{"authorization":"Bearer sk-secretvalue123","target":"staging"}`),
				Context: []action.RawContextValue{{
					Name: "environment", Value: json.RawMessage(`"test"`),
					Provenance: action.ProvenanceHostObserved, Available: true,
				}},
				Completeness: action.CompleteEvidence(), Deadline: action.DeadlineReady, StateVersion: "state-v1",
			},
			State: impactlab.ActionStateFixture{
				ContextIdentity: "context-v1", ExecutableDigest: actionImpactExecutableDigest,
				Principal: "operator", CredentialLabels: []string{"database-writer"},
				Approval:  action.ApprovalSnapshot{Status: action.ApprovalNone, Identity: "approval-none"},
				Taint:     action.TaintSnapshot{Status: action.TaintClean, Identity: "taint-clean"},
				Lifecycle: action.LifecycleActive, CachePolicyVersion: action.CacheIdentityVersion,
				Budget: action.BudgetSnapshot{
					StateVersion: "state-v1", Identity: "absent",
					ReservationIdentity: "absent", Complete: true, Candidates: []action.BudgetCandidate{},
				},
				ResampleDrift: []impactlab.ActionIdentityComponent{},
			},
			Expected: impactlab.ActionAssertion{
				Decision: action.DecisionAllow, Reason: action.ReasonDeclaredTool, ToolID: "database-write",
				MatchedRuleIDs: []string{}, Cache: impactlab.ActionCacheAssertion{Eligible: true, Reason: action.CacheEligible},
				Completeness: action.CompleteEvidence(), PhaseOutcome: action.OutcomeDispatchEligible,
			},
			SelectedValues: []impactlab.ActionValueSummary{},
		},
	}}, impactlab.AllEventClasses())
	if err != nil {
		t.Fatal(err)
	}
	corpusBody, err := impactlab.MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(corpusBody, []byte("secretvalue123")) {
		t.Fatalf("action corpus leaked credential: %s", corpusBody)
	}
	corpusPath := filepath.Join(t.TempDir(), "action-corpus.json")
	if err := os.WriteFile(corpusPath, corpusBody, 0o600); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(t.TempDir(), "unreviewed.json")
	var stdout, stderr bytes.Buffer
	err = Run([]string{
		"impact", repo, "--candidate", candidate, "--corpus", corpusPath,
		"--format", "json", "--output", outputPath,
	}, "test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("unreviewed action delta error = %v", err)
	}
	outputBody, readErr := os.ReadFile(outputPath)
	if readErr != nil || !bytes.Equal(outputBody, stdout.Bytes()) || stderr.Len() != 0 {
		t.Fatalf("unreviewed output = %v, equal=%t, stderr=%q", readErr, bytes.Equal(outputBody, stdout.Bytes()), stderr.String())
	}
	var report impactlab.Report
	if err := json.Unmarshal(outputBody, &report); err != nil {
		t.Fatal(err)
	}
	if report.DeltaGate.Passed || report.DeltaGate.RequiredCount != 1 || len(report.Cases) != 1 ||
		report.Cases[0].Action == nil || report.Cases[0].Action.Candidate.Outcome.Decision != action.DecisionBlock {
		t.Fatalf("unreviewed action report = %+v", report)
	}
	comparison := report.Cases[0]
	manifest, err := impactlab.NewDeltaManifest([]impactlab.ReviewedActionDelta{{
		CaseID: comparison.ID, CaseIdentity: comparison.CaseIdentity, Delta: impactlab.DeltaNewlyBlocked,
		CandidateLockDigest: report.Candidate.LockDigest,
		Current:             comparison.Action.Current.Outcome,
		Candidate:           comparison.Action.Candidate.Outcome,
		Rationale:           "reviewed staging block",
		Permanent:           true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	manifestBody, err := impactlab.MarshalDeltaManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(t.TempDir(), "delta-manifest.json")
	if err := os.WriteFile(manifestPath, manifestBody, 0o600); err != nil {
		t.Fatal(err)
	}

	formats := []struct {
		name string
		want string
	}{
		{name: "text", want: "database-staging"},
		{name: "json", want: `"reviewed": true`},
		{name: "junit", want: `<testsuite`},
		{name: "sarif", want: `"version": "2.1.0"`},
		{name: "github", want: "## Reconc impact"},
	}
	for _, format := range formats {
		t.Run(format.name, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			if err := Run([]string{
				"impact", repo, "--candidate", candidate, "--corpus", corpusPath,
				"--delta-manifest", manifestPath, "--format", format.name,
			}, "test", &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout.String(), format.want) || !strings.Contains(stdout.String(), "database-staging") ||
				strings.Contains(stdout.String(), "secretvalue123") || stderr.Len() != 0 {
				t.Fatalf("%s output = %q; stderr=%q", format.name, stdout.String(), stderr.String())
			}
		})
	}
}

func makeActionImpactCLIRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte(actionImpactCurrentPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"compile", repo}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("compile action impact repo: %v\nstderr: %s", err, stderr.String())
	}
	return repo
}

func TestImpactRejectsUnsafeCandidateAndInvalidOptions(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	root := t.TempDir()
	target := filepath.Join(root, "candidate.yml")
	link := filepath.Join(root, "candidate-link.yml")
	if err := os.WriteFile(target, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	tests := [][]string{
		{"impact", repo, "--candidate", link, "--write", "src/main.go"},
		{"impact", repo, "--candidate", target, "--pack", "default", "--write", "src/main.go"},
		{"impact", repo, "--candidate", target},
		{"impact", "export", repo, "--write", "src/main.go", "--complete", "unknown"},
		{"impact", repo, "--candidate", target, "--write", "src/main.go", "--format", "yaml"},
		{"impact", repo, "--candidate", target, "--write", "src/main.go", "--json", "--format", "json"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if err := Run(args, "test", &stdout, &stderr); err == nil || ExitCode(err) != 1 {
			t.Fatalf("impact accepted %v: %v", args, err)
		}
	}
}

const (
	actionImpactServerIdentity     = "hmac-sha256:v1:fixture:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	actionImpactToolDigest         = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	actionImpactRepositoryIdentity = "hmac-sha256:v1:fixture:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	actionImpactExecutableDigest   = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	actionImpactCurrentPolicy      = `actions:
  tools:
    - id: database-write
      transport: mcp_stdio
      server_label: database
      server_fingerprint: ` + actionImpactServerIdentity + `
      tool: execute
      effect:
        kind: external
  rules: []
rules: []
`
	actionImpactCandidatePolicy = `actions:
  rules:
    - id: block-staging
      selector:
        tool_ids: [database-write]
        phases: [pre_call]
      when:
        predicate:
          source: arguments
          pointer: /target
          op: eq
          value: staging
      decision: block
      message: Staging blocked.
rules: []
`
)

func TestImpactHelpAndExportHelpAreCanonical(t *testing.T) {
	for _, args := range [][]string{{"impact", "--help"}, {"help", "impact", "export"}} {
		var stdout, stderr bytes.Buffer
		if err := Run(args, "test", &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"impact", "--output"} {
			if !strings.Contains(stdout.String(), expected) {
				t.Errorf("%v help omitted %q:\n%s", args, expected, stdout.String())
			}
		}
	}
}
