package runtime

import (
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestNodeManagerAssurancePacksRequireOnlyDeclaredScripts(t *testing.T) {
	tests := []struct {
		name    string
		preset  string
		manager string
		lock    string
		command string
	}{
		{name: "npm", preset: "npm-assurance", manager: "npm@11.4.2", lock: "package-lock.json", command: "npm run test"},
		{name: "npm-metadata-only", preset: "npm-assurance", manager: "npm@11.4.2", command: "npm run test"},
		{name: "pnpm", preset: "pnpm-assurance", manager: "pnpm@10.0.0", lock: "pnpm-lock.yaml", command: "pnpm run test"},
		{name: "Yarn", preset: "yarn-assurance", manager: "yarn@4.9.2", lock: "yarn.lock", command: "yarn run test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := t.TempDir()
			writeFile(t, repo, "AGENTS.md", "# project\n")
			writeFile(t, repo, ".reconc.yml", "extends:\n  - "+test.preset+"\n")
			writeFile(t, repo, "package.json", `{"packageManager":"`+test.manager+`","scripts":{"test":"node --test"}}`+"\n")
			if test.lock != "" {
				writeFile(t, repo, test.lock, "lock\n")
			}
			writeFile(t, repo, "src/main.ts", "export const ready = true;\n")
			if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
				t.Fatalf("compile %s: %v", test.preset, err)
			}

			inputs := Empty()
			inputs.WritePaths = []string{"src/main.ts"}
			report, err := CheckRepoPolicy(repo, inputs)
			if err != nil || report.Decision != DecisionWarn {
				t.Fatalf("missing declared script evidence must warn: decision=%v err=%v violations=%+v", report.Decision, err, report.Violations)
			}
			inputs.CommandResults = []CommandResult{{Command: test.command, Outcome: CommandOutcomeSuccess, EvidenceEpoch: ExplicitEvidenceEpoch}}
			report, err = CheckRepoPolicy(repo, inputs)
			if err != nil || report.Decision != DecisionPass {
				t.Fatalf("declared script evidence must pass without invented lint/build/typecheck: decision=%v err=%v violations=%+v", report.Decision, err, report.Violations)
			}
		})
	}
}

func TestTypeScriptAssuranceUsesDeclaredTypecheckAndNativeHygiene(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, ".reconc.yml", "extends:\n  - typescript-assurance\n")
	writeFile(t, repo, "package.json", `{"packageManager":"npm@11.4.2","scripts":{"typecheck":"tsc --noEmit"}}`+"\n")
	writeFile(t, repo, "tsconfig.json", "{}\n")
	writeFile(t, repo, "src/main.ts", "export const ready = true;\n")
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}

	inputs := Empty()
	inputs.WritePaths = []string{"src/main.ts"}
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil || report.Decision != DecisionWarn {
		t.Fatalf("missing typecheck evidence must warn: decision=%v err=%v violations=%+v", report.Decision, err, report.Violations)
	}
	inputs.CommandResults = []CommandResult{{Command: "npm run typecheck", Outcome: CommandOutcomeSuccess, EvidenceEpoch: ExplicitEvidenceEpoch}}
	report, err = CheckRepoPolicy(repo, inputs)
	if err != nil || report.Decision != DecisionPass {
		t.Fatalf("declared typecheck and clean source must pass: decision=%v err=%v violations=%+v", report.Decision, err, report.Violations)
	}
}
