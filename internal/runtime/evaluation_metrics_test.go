package runtime

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestCompiledPolicyEvaluatorMatchesFreshRepositoryEvaluator(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: deny-src\n    kind: deny_write\n    paths: [src/**]\n    mode: block\n    message: blocked\n")
	_, body, err := compiler.RenderRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := NewCompiledPolicyEvaluator(body)
	if err != nil {
		t.Fatal(err)
	}
	inputs := ExecutionInputs{
		ReadPaths: []string{}, WritePaths: []string{"src/main.go"}, WriteEpochs: map[string]uint64{},
		Commands: []string{}, Claims: []string{}, CommandResults: []CommandResult{},
	}
	currentReport, currentMetrics, err := NewEvaluator().CheckRepoPolicyWithMetrics(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	candidateReport, candidateMetrics, err := candidate.Check(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(currentReport, candidateReport) || currentMetrics != candidateMetrics {
		t.Fatalf("compiled evaluator drift:\ncurrent=%+v %+v\ncandidate=%+v %+v",
			currentReport, currentMetrics, candidateReport, candidateMetrics)
	}
	if !reflect.DeepEqual(candidate.RuleIDs(), []string{"deny-src"}) {
		t.Fatalf("candidate rule ids = %v", candidate.RuleIDs())
	}
}

func TestNormalizeReplayInputsUsesProductionPathAndCommandSemantics(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	inputs := Empty()
	inputs.WritePaths = []string{repo + "/src/main.go"}
	inputs.Commands = []string{"go   test ./..."}
	normalized, err := NormalizeReplayInputs(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized.WritePaths, []string{"src/main.go"}) ||
		!reflect.DeepEqual(normalized.Commands, []string{"go test ./..."}) {
		t.Fatalf("normalized replay = %+v", normalized)
	}
}

func TestNormalizeReplayInputsDeduplicatesWriteIdentityAndPreservesLatestEpoch(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	absolute := repo + "/src/main.go"
	normalized, err := NormalizeReplayInputs(repo, ExecutionInputs{
		WritePaths:  []string{absolute, "src/main.go", absolute},
		WriteEpochs: map[string]uint64{absolute: 2, "src/main.go": 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized.WritePaths, []string{"src/main.go"}) {
		t.Fatalf("normalized writes = %#v", normalized.WritePaths)
	}
	if normalized.WriteEpochs["src/main.go"] != 3 {
		t.Fatalf("normalized write epoch = %d, want 3", normalized.WriteEpochs["src/main.go"])
	}
}

func TestCompiledPolicyEvaluatorTracesSatisfiedRuleTriggers(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: read-first\n    kind: require_read\n    paths: [src/**]\n    before_paths: [README.md]\n    mode: block\n    message: read first\n")
	_, body, err := compiler.RenderRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewCompiledPolicyEvaluator(body)
	if err != nil {
		t.Fatal(err)
	}
	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.ReadPaths = []string{"README.md"}
	report, _, trace, err := evaluator.CheckWithTrace(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass || !reflect.DeepEqual(trace.MatchedRuleIDs, []string{"read-first"}) {
		t.Fatalf("satisfied trace = %+v, report=%+v", trace, report)
	}
}

func TestCompiledPolicyEvaluatorTraceUsesCompleteCommandTriggers(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", `rules:
  - id: path-only
    kind: require_claim
    when_paths: ['src/**']
    claims: [approved]
    mode: warn
    message: path
  - id: command-only
    kind: forbid_command
    command_match: prefix
    commands: ['pip install']
    mode: warn
    message: command
  - id: composite
    kind: all_of
    when_paths: ['src/**']
    checks:
      - kind: forbid_command
        command_match: prefix
        commands: ['pip install']
      - kind: require_claim
        claims: [approved]
    mode: warn
    message: composite
`)
	_, body, err := compiler.RenderRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewCompiledPolicyEvaluator(body)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name               string
		inputs             ExecutionInputs
		want               []string
		wantPreCommandMode Decision
	}{
		{
			name:               "path only",
			inputs:             ExecutionInputs{WritePaths: []string{"src/main.go"}, Claims: []string{"approved"}},
			want:               []string{"path-only"},
			wantPreCommandMode: DecisionPass,
		},
		{
			name:               "command only",
			inputs:             ExecutionInputs{Commands: []string{"pip install requests"}},
			want:               []string{"command-only"},
			wantPreCommandMode: DecisionWarn,
		},
		{
			name: "composite hit",
			inputs: ExecutionInputs{
				WritePaths: []string{"src/main.go"}, Commands: []string{"rtk pip install requests"}, Claims: []string{"approved"},
			},
			want:               []string{"command-only", "composite", "path-only"},
			wantPreCommandMode: DecisionWarn,
		},
		{
			name: "composite miss",
			inputs: ExecutionInputs{
				WritePaths: []string{"src/main.go"}, Commands: []string{"echo safe"}, Claims: []string{"approved"},
			},
			want:               []string{"path-only"},
			wantPreCommandMode: DecisionPass,
		},
		{
			name: "historical command result is not current",
			inputs: ExecutionInputs{
				WritePaths: []string{"src/main.go"}, Commands: []string{"echo safe"}, Claims: []string{"approved"},
				CommandResults: []CommandResult{{Command: "pip install old", Outcome: CommandOutcomeSuccess}},
			},
			want:               []string{"command-only", "path-only"},
			wantPreCommandMode: DecisionPass,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, trace, err := evaluator.CheckWithTrace(repo, test.inputs)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(trace.MatchedRuleIDs, test.want) {
				t.Fatalf("matched rules = %#v, want %#v", trace.MatchedRuleIDs, test.want)
			}
			preCommand, err := CheckRepoPolicyForPreCommand(repo, test.inputs)
			if err != nil {
				t.Fatal(err)
			}
			if preCommand.Decision != test.wantPreCommandMode {
				t.Fatalf("pre-command decision = %s, want %s", preCommand.Decision, test.wantPreCommandMode)
			}
		})
	}
}

func TestCompiledPolicyEvaluatorTraceDoesNotRepeatScriptSideEffects(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	counter := filepath.Join(repo, "counter")
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, "scripts/audit.sh", "#!/bin/sh\nprintf x >> \""+counter+"\"\n")
	if err := os.Chmod(filepath.Join(repo, "scripts/audit.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "policies/rules.yml", `rules:
  - id: script
    kind: require_script
    when_paths: ['src/**']
    script: scripts/audit.sh
    mode: block
    message: script
`)
	_, body, err := compiler.RenderRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewCompiledPolicyEvaluator(body)
	if err != nil {
		t.Fatal(err)
	}
	report, _, trace, err := evaluator.CheckWithTrace(repo, ExecutionInputs{WritePaths: []string{"src/main.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass || !reflect.DeepEqual(trace.MatchedRuleIDs, []string{"script"}) {
		t.Fatalf("script trace = %#v, report=%+v", trace.MatchedRuleIDs, report)
	}
	body, err = os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "x" {
		t.Fatalf("script executions = %q, want one", body)
	}
}

func TestCompiledPolicyEvaluatorReusesRootBoundCommandExpectations(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, "policies/rules.yml", "rules:\n  - id: tests\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['cd "+repo+" && go test ./...']\n    mode: block\n    message: tests\n")
	_, body, err := compiler.RenderRepoPolicy(repo, "0.1.0-test")
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewCompiledPolicyEvaluator(body)
	if err != nil {
		t.Fatal(err)
	}
	first := evaluator.planForRoot(repo)
	second := evaluator.planForRoot(repo)
	if first != second || first.commandExpectations == nil || first.commandExpectationRoot != repo {
		t.Fatal("compiled evaluator rebuilt or failed to root command expectations")
	}
	report, _, err := evaluator.Check(repo, ExecutionInputs{
		WritePaths: []string{"src/main.go"},
		CommandResults: []CommandResult{{
			Command: "go test ./...", Outcome: CommandOutcomeSuccess,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass || evaluator.planForRoot(repo) != first {
		t.Fatalf("root-bound compiled evaluation = %+v", report)
	}
}
