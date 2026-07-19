package runtime

import (
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestPortableStackAssurancePacksEnforceLiveVerification(t *testing.T) {
	tests := []struct {
		name       string
		preset     string
		markerPath string
		marker     string
		sourcePath string
		source     string
		commands   []string
	}{
		{
			name:       "Python",
			preset:     "python-assurance",
			markerPath: "pyproject.toml",
			marker:     "[project]\nname = \"demo\"\n",
			sourcePath: "src/main.py",
			source:     "def main():\n    return 1\n",
			commands:   []string{"python -m pytest -q"},
		},
		{
			name:       "Rust",
			preset:     "rust-assurance",
			markerPath: "Cargo.toml",
			marker:     "[package]\nname = \"demo\"\nversion = \"0.1.0\"\nedition = \"2024\"\n",
			sourcePath: "src/main.rs",
			source:     "fn main() {}\n",
			commands: []string{
				"cargo test",
				"cargo fmt --all -- --check",
				"cargo clippy --all-targets --all-features -- -D warnings",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := t.TempDir()
			writeFile(t, repo, "AGENTS.md", "# project\n")
			writeFile(t, repo, ".reconc.yml", "extends:\n  - "+test.preset+"\n")
			writeFile(t, repo, test.markerPath, test.marker)
			writeFile(t, repo, test.sourcePath, test.source)
			if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
				t.Fatalf("compile %s: %v", test.preset, err)
			}

			inputs := Empty()
			inputs.WritePaths = []string{test.sourcePath}
			report, err := CheckRepoPolicy(repo, inputs)
			if err != nil || report.Decision != DecisionWarn {
				t.Fatalf("missing %s evidence must warn: decision=%v err=%v violations=%+v", test.name, report.Decision, err, report.Violations)
			}
			for _, command := range test.commands {
				inputs.CommandResults = append(inputs.CommandResults, CommandResult{
					Command: command, Outcome: CommandOutcomeSuccess, EvidenceEpoch: ExplicitEvidenceEpoch,
				})
			}
			report, err = CheckRepoPolicy(repo, inputs)
			if err != nil || report.Decision != DecisionPass {
				t.Fatalf("current %s evidence must pass: decision=%v err=%v violations=%+v", test.name, report.Decision, err, report.Violations)
			}
		})
	}
}
