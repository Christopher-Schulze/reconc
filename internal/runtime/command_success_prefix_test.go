package runtime

import "testing"

func TestPrefixSuccessPreservesRequiredCommandExitStatus(t *testing.T) {
	tests := []struct {
		command string
		pass    bool
	}{
		{"go test ./... -run TestFoo", true},
		{"go test ./... -run TestFoo 2>&1", true},
		{`go test ./... '|| true'`, true},
		{`go test ./... \|`, true},
		{"go test ./... || true", false},
		{"go test ./... | tail", false},
		{"go test ./... ; true", false},
		{"go test ./... &", false},
		{"go test ./... && true", false},
		{"go test ./... -run TestFoo || true", false},
		{"go test ./... $FLAGS", false},
		{"go test ./... $(true)", false},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			repo := makeRepoWithFiles(t, "rules:\n  - id: tests-first\n    kind: require_command_success\n    command_match: prefix\n    when_paths: ['src/**']\n    commands: ['go test ./...']\n    mode: block\n    message: tests must succeed\n", nil)
			inputs := Empty()
			inputs.WritePaths = []string{"src/main.go"}
			inputs.CommandResults = []CommandResult{{Command: test.command, Outcome: CommandOutcomeSuccess}}
			report, err := CheckRepoPolicy(repo, inputs)
			if err != nil {
				t.Fatal(err)
			}
			if (report.Decision == DecisionPass) != test.pass {
				t.Fatalf("decision = %s, want pass=%t; violations=%+v", report.Decision, test.pass, report.Violations)
			}
		})
	}
}
