//go:build !windows

package runtime

import "testing"

func TestBuiltinGuardrailTemplatesEnforceBehavior(t *testing.T) {
	t.Run("legacy CI claim", func(t *testing.T) {
		for _, test := range []struct {
			name     string
			claims   []string
			decision Decision
		}{
			{name: "missing claim warns", decision: DecisionWarn},
			{name: "self claim satisfies legacy rule", claims: []string{"ci-green"}, decision: DecisionPass},
		} {
			t.Run(test.name, func(t *testing.T) {
				withRECONCHome(t)
				repo := makeRepo(t, "# project\n", "", "rules:\n  - id: ci\n    template: ci-green-before-merge\n    when_paths: ['**']\n")
				inputs := Empty()
				inputs.WritePaths = []string{"src/main.go"}
				inputs.Claims = test.claims
				report, err := CheckRepoPolicy(repo, inputs)
				if err != nil || report.Decision != test.decision {
					t.Fatalf("legacy CI decision = %v, want %v; error = %v", report.Decision, test.decision, err)
				}
			})
		}
	})
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
		for _, test := range []struct {
			path     string
			decision Decision
		}{
			{path: ".env", decision: DecisionBlock},
			{path: "services/api/.env", decision: DecisionBlock},
			{path: ".env.local", decision: DecisionBlock},
			{path: "services/api/.env.development", decision: DecisionBlock},
			{path: ".env.example", decision: DecisionPass},
			{path: "services/api/.env.example", decision: DecisionPass},
			{path: ".env.template", decision: DecisionPass},
			{path: "services/api/.env.template", decision: DecisionPass},
			{path: "services/api/.env.example.bak", decision: DecisionBlock},
			{path: "services/api/.env.examples", decision: DecisionBlock},
			{path: "runtime.db", decision: DecisionBlock},
			{path: "state/runtime.db", decision: DecisionBlock},
			{path: "runtime.db-wal", decision: DecisionBlock},
			{path: "state/runtime.db-wal", decision: DecisionBlock},
			{path: "runtime.db-shm", decision: DecisionBlock},
			{path: "state/runtime.db-shm", decision: DecisionBlock},
			{path: "runtime.sqlite", decision: DecisionBlock},
			{path: "state/runtime.sqlite", decision: DecisionBlock},
			{path: "runtime.sqlite-wal", decision: DecisionBlock},
			{path: "state/runtime.sqlite-wal", decision: DecisionBlock},
			{path: "runtime.sqlite-shm", decision: DecisionBlock},
			{path: "state/runtime.sqlite-shm", decision: DecisionBlock},
			{path: "runtime.sqlite3", decision: DecisionBlock},
			{path: "state/runtime.sqlite3", decision: DecisionBlock},
			{path: "runtime.sqlite3-wal", decision: DecisionBlock},
			{path: "state/runtime.sqlite3-wal", decision: DecisionBlock},
			{path: "runtime.sqlite3-shm", decision: DecisionBlock},
			{path: "state/runtime.sqlite3-shm", decision: DecisionBlock},
			{path: "runtime.sqlite3-shm.bak", decision: DecisionPass},
			{path: "src/main.go", decision: DecisionPass},
		} {
			t.Run(test.path, func(t *testing.T) {
				inputs := Empty()
				inputs.WritePaths = []string{test.path}
				report, err := CheckRepoPolicy(repo, inputs)
				if err != nil || report.Decision != test.decision {
					t.Fatalf("write %q decision=%v want=%v err=%v", test.path, report.Decision, test.decision, err)
				}
			})
		}

		configuredRepo := makeRepo(t, "# project\n", "", "rules:\n  - id: project-state\n    kind: deny_write\n    paths: ['.env.*', '**/.env.*']\n    exclude_paths: ['.env.ci.example', '**/.env.ci.example']\n    mode: block\n    message: protect project secret state\n")
		for _, test := range []struct {
			path     string
			decision Decision
		}{
			{path: ".env.ci.example", decision: DecisionPass},
			{path: "services/api/.env.ci.example", decision: DecisionPass},
			{path: ".env.ci", decision: DecisionBlock},
			{path: "services/api/.env.example", decision: DecisionBlock},
		} {
			t.Run("configured "+test.path, func(t *testing.T) {
				report, err := CheckRepoPolicy(configuredRepo, ExecutionInputs{WritePaths: []string{test.path}})
				if err != nil || report.Decision != test.decision {
					t.Fatalf("configured exception write %q decision=%v want=%v err=%v", test.path, report.Decision, test.decision, err)
				}
			})
		}

		overrideRepo := makeRepo(t, "# project\n", "", "rules:\n  - id: local-state\n    template: local-secret-state-read-only\n  - id: template-rule\n    kind: deny_write\n    paths: ['.env.example', '**/.env.example']\n    mode: block\n    message: project rule still blocks this path\n")
		inputs := Empty()
		inputs.WritePaths = []string{".env.example"}
		report, err := CheckRepoPolicy(overrideRepo, inputs)
		if err != nil || report.Decision != DecisionBlock {
			t.Fatalf("independent deny rule must remain effective: decision=%v err=%v", report.Decision, err)
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
