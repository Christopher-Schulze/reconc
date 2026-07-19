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
		{
			name:       "Shell",
			preset:     "shell-assurance",
			markerPath: ".shellcheckrc",
			marker:     "shell=bash\n",
			sourcePath: "scripts/check.sh",
			source:     "#!/bin/sh\nexit 0\n",
			commands:   []string{"find . -type f -name '*.sh' -not -path './.git/*' -not -path './vendor/*' -exec shellcheck {} +"},
		},
		{
			name:       "C++",
			preset:     "cpp-assurance",
			markerPath: "CMakeLists.txt",
			marker:     "cmake_minimum_required(VERSION 3.20)\n",
			sourcePath: "src/main.cpp",
			source:     "int main() { return 0; }\n",
			commands:   []string{"ctest --test-dir build --output-on-failure"},
		},
		{
			name:       "Java",
			preset:     "java-assurance",
			markerPath: "pom.xml",
			marker:     "<project/>\n",
			sourcePath: "src/main/java/Main.java",
			source:     "final class Main {}\n",
			commands:   []string{"mvn test"},
		},
		{
			name:       "PHP",
			preset:     "php-assurance",
			markerPath: "composer.json",
			marker:     "{}\n",
			sourcePath: "src/index.php",
			source:     "<?php\nfunction main(): int { return 1; }\n",
			commands:   []string{"composer test"},
		},
		{
			name:       "C#",
			preset:     "csharp-assurance",
			markerPath: "Example.csproj",
			marker:     "<Project/>\n",
			sourcePath: "src/Program.cs",
			source:     "internal static class Program {}\n",
			commands:   []string{"dotnet test"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			t.Setenv("PATH", "")
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
