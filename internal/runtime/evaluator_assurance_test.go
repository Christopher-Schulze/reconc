package runtime

import (
	"strings"
	"testing"
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
