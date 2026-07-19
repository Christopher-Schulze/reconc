//go:build !windows

package runtime

import "testing"

func TestBuiltinGuardrailTemplatesEnforceBehavior(t *testing.T) {
	t.Run("authority approval", func(t *testing.T) {
		withRECONCHome(t)
		repo := makeRepo(t, "# project\n", "", "rules:\n  - id: authority\n    template: authority-change-approval\n    when_paths: ['AGENTS.md']\n")
		inputs := Empty()
		inputs.WritePaths = []string{"AGENTS.md"}
		report, err := CheckRepoPolicy(repo, inputs)
		if err != nil || report.Decision != DecisionBlock {
			t.Fatalf("missing approval must block: decision=%v err=%v", report.Decision, err)
		}
		inputs.Claims = []string{"authority-change-approved"}
		report, err = CheckRepoPolicy(repo, inputs)
		if err != nil || report.Decision != DecisionPass {
			t.Fatalf("approval claim must pass: decision=%v err=%v", report.Decision, err)
		}
	})

	t.Run("local secret state", func(t *testing.T) {
		withRECONCHome(t)
		repo := makeRepo(t, "# project\n", "", "rules:\n  - id: local-state\n    template: local-secret-state-read-only\n")
		inputs := Empty()
		inputs.WritePaths = []string{"services/api/.env.local", "state/runtime.db-wal", "state/runtime.sqlite3"}
		report, err := CheckRepoPolicy(repo, inputs)
		if err != nil || report.Decision != DecisionBlock {
			t.Fatalf("secret or database-state write must block: decision=%v err=%v", report.Decision, err)
		}
		inputs.WritePaths = []string{"src/main.go"}
		report, err = CheckRepoPolicy(repo, inputs)
		if err != nil || report.Decision != DecisionPass {
			t.Fatalf("ordinary source write must pass: decision=%v err=%v", report.Decision, err)
		}
	})

	t.Run("verified change", func(t *testing.T) {
		withRECONCHome(t)
		repo := makeRepo(t, "# project\n", "", "rules:\n  - id: verify\n    template: verified-change\n    commands: ['go test ./...']\n    when_paths: ['**/*.go']\n")
		inputs := Empty()
		inputs.WritePaths = []string{"internal/policy/policy.go"}
		report, err := CheckRepoPolicy(repo, inputs)
		if err != nil || report.Decision != DecisionBlock {
			t.Fatalf("missing command evidence must block: decision=%v err=%v", report.Decision, err)
		}
		inputs.CommandResults = []CommandResult{{Command: "go test ./...", Outcome: CommandOutcomeSuccess, EvidenceEpoch: ExplicitEvidenceEpoch}}
		report, err = CheckRepoPolicy(repo, inputs)
		if err != nil || report.Decision != DecisionPass {
			t.Fatalf("successful verification must pass: decision=%v err=%v", report.Decision, err)
		}
	})

	t.Run("custom gate", func(t *testing.T) {
		withRECONCHome(t)
		repo := t.TempDir()
		writeFile(t, repo, "AGENTS.md", "# project\n")
		writeScript(t, repo, "scripts/gate.sh", "#!/bin/sh\nexit 2\n")
		writeFile(t, repo, "policies/rules.yml", "rules:\n  - id: gate\n    template: custom-gate-on-change\n    script: scripts/gate.sh\n    when_paths: ['src/**']\n")
		if _, err := compileTestHelper(repo); err != nil {
			t.Fatal(err)
		}
		inputs := Empty()
		inputs.WritePaths = []string{"src/main.go"}
		report, err := CheckRepoPolicy(repo, inputs)
		if err != nil || report.Decision != DecisionBlock {
			t.Fatalf("blocking gate must block: decision=%v err=%v", report.Decision, err)
		}
		writeScript(t, repo, "scripts/gate.sh", "#!/bin/sh\nexit 0\n")
		report, err = CheckRepoPolicy(repo, inputs)
		if err != nil || report.Decision != DecisionPass {
			t.Fatalf("successful gate must pass: decision=%v err=%v", report.Decision, err)
		}
	})
}
