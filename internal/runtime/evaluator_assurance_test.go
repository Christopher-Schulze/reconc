package runtime

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"reconc.dev/reconc/internal/policy"
)

func TestRequireAssuranceUsesCurrentSuccessAndChangedFileGate(t *testing.T) {
	withRECONCHome(t)
	policyYAML := `rules:
  - id: native-assurance
    kind: require_assurance
    mode: block
    when_paths: ["**/*.go"]
    message: native assurance required
    assurance:
      - id: live
        type: live_verification
        commands: ["go test ./..."]
      - id: network
        type: network_boundary
        scan_paths: ["**/*.go"]
        site_patterns: ["http.Get("]
        guard_markers: ["GuardedClient"]
        marker_window_lines: 2
`
	repo := makeRepo(t, "# project\n", "", policyYAML)
	writeFile(t, repo, "src/main.go", "package main\nfunc run() { http.Get(\"https://example.test\") }\n")
	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}

	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 1 {
		t.Fatalf("expected one native assurance block, got %+v", report)
	}
	for _, expected := range []string{"[live]", "[network]"} {
		if !strings.Contains(report.Violations[0].Explanation, expected) {
			t.Errorf("expected %s finding in %q", expected, report.Violations[0].Explanation)
		}
	}

	writeFile(t, repo, "src/main.go", "package main\nfunc run() { GuardedClient(); http.Get(\"https://example.test\") }\n")
	inputs.Commands = []string{"go test ./..."}
	inputs.CommandResults = []CommandResult{{Command: "go test ./...", Outcome: CommandOutcomeSuccess}}
	report, err = CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("expected current success plus guard to pass, got %+v", report.Violations)
	}
}

func TestRequireAssuranceReportsRawSuccessfulCommandOnce(t *testing.T) {
	withRECONCHome(t)
	policyYAML := `rules:
  - id: native-assurance
    kind: require_assurance
    mode: block
    when_paths: ["**/*.go"]
    message: native assurance required
    assurance:
      - id: live
        type: live_verification
        commands: ["go test ./..."]
      - id: network
        type: network_boundary
        scan_paths: ["**/*.go"]
        site_patterns: ["http.Get("]
        guard_markers: ["GuardedClient"]
        marker_window_lines: 2
`
	repo := makeRepo(t, "# project\n", "", policyYAML)
	writeFile(t, repo, "src/main.go", "package main\nfunc run() { http.Get(\"https://example.test\") }\n")
	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}
	inputs.CommandResults = []CommandResult{{Command: "rtk go test ./...", Outcome: CommandOutcomeSuccess}}

	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 1 {
		t.Fatalf("expected only network gate to block, got %+v", report)
	}
	commands := report.Violations[0].MatchedCommands
	if len(commands) != 1 || commands[0] != "rtk go test ./..." {
		t.Fatalf("report must keep one raw command instead of raw+normalized duplicates: %v", commands)
	}
}

func TestRequireAssuranceBoundsMaximumFindingDetails(t *testing.T) {
	withRECONCHome(t)
	const gateCount = 50
	command := "verify-" + strings.Repeat("界", 2048)
	var policyYAML strings.Builder
	policyYAML.WriteString("rules:\n  - id: native-assurance-bounds\n    kind: require_assurance\n    mode: block\n    when_paths: [\"**/*.go\"]\n    message: native assurance required\n    assurance:\n")
	for index := range gateCount {
		policyYAML.WriteString(fmt.Sprintf("      - id: gate-%03d\n        type: live_verification\n        commands: [%q]\n", index, command+fmt.Sprintf("-%03d", index)))
	}
	repo := makeRepo(t, "# project\n", "", policyYAML.String())
	inputs := Empty()
	inputs.WritePaths = []string{"src/main.go"}

	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 1 {
		t.Fatalf("assurance decision = %s, violations=%d", report.Decision, len(report.Violations))
	}
	violation := report.Violations[0]
	if len(violation.Explanation) > MaxViolationTextBytes || !utf8.ValidString(violation.Explanation) ||
		!strings.Contains(violation.Explanation, "[gate-000]") ||
		!strings.Contains(violation.Explanation, "...[49 additional failures omitted]") {
		t.Fatalf("bounded assurance explanation = %d bytes: %q", len(violation.Explanation), violation.Explanation)
	}
	if len(violation.RecommendedAction) > MaxViolationTextBytes || !utf8.ValidString(violation.RecommendedAction) ||
		!strings.HasSuffix(violation.RecommendedAction, "Then resolve the remaining 49 assurance finding(s).") {
		t.Fatalf("bounded assurance remediation = %d bytes: %q", len(violation.RecommendedAction), violation.RecommendedAction)
	}
}

func TestPreparedAssuranceGatesReuseNormalizedCommandStorage(t *testing.T) {
	rules := []policy.Rule{{
		ID: "native-assurance",
		Assurance: []policy.AssuranceGate{{
			ID: "live", Type: policy.AssuranceLiveVerification,
			Commands: []string{"cd /repo && go test ./..."},
		}},
	}}
	cache := newCommandInvocationCache(compileCommandExpectationPlan(rules, "/repo"))
	first, err := cache.assuranceGatesFor(&rules[0], "/repo")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.assuranceGatesFor(&rules[0], "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(first[0].Commands) != 1 || first[0].Commands[0] != "go test ./..." {
		t.Fatalf("prepared assurance gates = %#v", first)
	}
	if &first[0] != &second[0] || &first[0].Commands[0] != &second[0].Commands[0] {
		t.Fatal("prepared assurance gate storage was cloned between evaluations")
	}
	if rules[0].Assurance[0].Commands[0] != "cd /repo && go test ./..." {
		t.Fatalf("preparation mutated runtime rule storage: %#v", rules[0].Assurance[0])
	}
	var got []policy.AssuranceGate
	allocations := testing.AllocsPerRun(1000, func() {
		got, err = cache.assuranceGatesFor(&rules[0], "/repo")
	})
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("prepared assurance lookup = %#v, %v", got, err)
	}
	if allocations != 0 {
		t.Fatalf("prepared assurance lookup allocations = %.1f, want 0", allocations)
	}
}
