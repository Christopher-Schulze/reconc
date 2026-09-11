//go:build !windows

package runtime

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestAPIRecipeComparesRealGitCandidates(t *testing.T) {
	for _, test := range []struct {
		name, candidate, dirty string
		want                   Decision
	}{
		{"additive API", "Open()\nClose()\n", "", DecisionPass},
		{"removed API", "Close()\n", "", DecisionBlock},
		{"stale candidate", "Open()\nClose()\n", "Close()\n", DecisionBlock},
	} {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := initGitRepo(t)
			writeFile(t, repo, "AGENTS.md", "# API fixture\n")
			writeFile(t, repo, "api/exports.txt", "Open()\n")
			gitRun(t, repo, "add", ".")
			gitRun(t, repo, "commit", "-qm", "base API")
			base := recipeGitHead(t, repo)
			writeFile(t, repo, "api/exports.txt", test.candidate)
			gitRun(t, repo, "add", ".")
			gitRun(t, repo, "commit", "-qm", "candidate API")
			current := recipeGitHead(t, repo)
			writeScript(t, repo, "scripts/check-api.sh", apiRecipeComparisonScript)
			writeFile(t, repo, "policies/rules.yml", fmt.Sprintf(`rules:
  - id: api
    template: public-api-compatibility
    script: scripts/check-api.sh
    when_paths: ['api/**']
    args: ['--base', '%s', '--current', '%s']
    cache_inputs: ['api/exports.txt', 'scripts/check-api.sh']
`, base, current))
			if test.dirty != "" {
				writeFile(t, repo, "api/exports.txt", test.dirty)
			}
			assertSourceRecipeDecision(t, repo, "api/exports.txt", test.want)
		})
	}
}

func TestGeneratedRecipeComparesOutputsAndPreservesUnrelatedChanges(t *testing.T) {
	for _, test := range []struct {
		name, output       string
		missing, unrelated bool
		want               Decision
	}{
		{"matching output", "HELLO\n", false, false, DecisionPass},
		{"stale output", "OLD\n", false, false, DecisionBlock},
		{"missing output", "", true, false, DecisionBlock},
		{"unrelated candidate change", "HELLO\n", false, true, DecisionBlock},
	} {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := initGitRepo(t)
			writeFile(t, repo, "AGENTS.md", "# generator fixture\n")
			writeFile(t, repo, "schema/message.txt", "hello\n")
			writeFile(t, repo, "docs/owned.txt", "before\n")
			if !test.missing {
				writeFile(t, repo, "generated/message.txt", test.output)
			}
			gitRun(t, repo, "add", ".")
			gitRun(t, repo, "commit", "-qm", "source candidate")
			source := recipeGitHead(t, repo)
			writeScript(t, repo, "scripts/check-generated.sh", generatedRecipeComparisonScript)
			writeFile(t, repo, "policies/rules.yml", fmt.Sprintf(`rules:
  - id: generated
    template: generated-artifact-consistency
    script: scripts/check-generated.sh
    when_paths: ['schema/**']
    args: ['--source', '%s', '--outputs', 'generated/message.txt']
    cache_inputs: ['schema/message.txt', 'generated/message.txt', 'docs/owned.txt', 'scripts/check-generated.sh']
`, source))
			if test.unrelated {
				writeFile(t, repo, "docs/owned.txt", "user change\n")
			}
			assertSourceRecipeDecision(t, repo, "schema/message.txt", test.want)
			if test.unrelated {
				command := exec.Command("git", "diff", "--", "docs/owned.txt")
				command.Dir = repo
				output, err := command.CombinedOutput()
				if err != nil || !strings.Contains(string(output), "+user change") {
					t.Fatalf("unrelated change was lost: %s, %v", output, err)
				}
			}
		})
	}
}

func recipeGitHead(t *testing.T, repo string) string {
	t.Helper()
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = repo
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve fixture commit: %s: %v", output, err)
	}
	return strings.TrimSpace(string(output))
}

func assertSourceRecipeDecision(t *testing.T, repo, path string, want Decision) {
	t.Helper()
	if _, err := compileTestHelper(repo); err != nil {
		t.Fatal(err)
	}
	report, err := CheckRepoPolicy(repo, ExecutionInputs{WritePaths: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != want {
		t.Fatalf("recipe decision = %s, want %s: %+v", report.Decision, want, report)
	}
	for _, violation := range report.Violations {
		if strings.Contains(violation.Explanation, " error:") {
			t.Fatalf("domain outcome became script error: %s", violation.Explanation)
		}
	}
}

const apiRecipeComparisonScript = `#!/bin/sh
set -eu
[ "$#" -eq 4 ] && [ "$1" = --base ] && [ "$3" = --current ] || exit 1
base=$2
current=$4
mkdir scratch
git show "$base:api/exports.txt" > scratch/base
git show "$current:api/exports.txt" > scratch/current
result=pass
code=0
while IFS= read -r symbol; do
  if ! grep -F -x -- "$symbol" scratch/current >/dev/null; then result=block; code=2; fi
done < scratch/base
if ! cmp -s scratch/current api/exports.txt; then result=block; code=2; fi
printf '{"contract":"public-api-compatibility","result":"%s","base":"%s","current":"%s","evidence":"exported-symbol-set-comparison"}\n' "$result" "$base" "$current"
exit "$code"
`

const generatedRecipeComparisonScript = `#!/bin/sh
set -eu
[ "$#" -eq 4 ] && [ "$1" = --source ] && [ "$3" = --outputs ] && [ "$4" = generated/message.txt ] || exit 1
source=$2
[ "$(git rev-parse HEAD)" = "$source" ] || exit 1
mkdir scratch
tr '[:lower:]' '[:upper:]' < schema/message.txt > scratch/message.txt
result=pass
code=0
if ! cmp -s scratch/message.txt "$4"; then result=block; code=2; fi
if ! git diff --quiet HEAD -- docs/owned.txt; then result=block; code=2; fi
printf '{"contract":"generated-artifact-consistency","result":"%s","source":"%s","outputs":["generated/message.txt"],"evidence":"generated-output-and-candidate-comparison"}\n' "$result" "$source"
exit "$code"
`
