package runtime

import (
	"fmt"
	"strings"
	"testing"
)

func TestOwnerAwareCoupleChangeResolvesEachCompanionOnce(t *testing.T) {
	withRECONCHome(t)
	compiles := map[string]int{}
	previous := compileConcreteCouplePattern
	t.Cleanup(func() { compileConcreteCouplePattern = previous })
	compileConcreteCouplePattern = func(pattern string) (CompiledPathMatcher, error) {
		compiles[pattern]++
		return previous(pattern)
	}
	repo := makeRepo(t, "# project\n", "", `rules:
  - id: go-tests
    kind: couple_change
    paths: ['services/{module}/**/*.go']
    when_paths: ['services/{module}/**/*_test.go']
    mode: block
    message: Go tests required for the changed module
`)
	inputs := ExecutionInputs{}
	for _, module := range []string{"api", "worker"} {
		for index := 0; index < 200; index++ {
			inputs.WritePaths = append(inputs.WritePaths, fmt.Sprintf("services/%s/file%03d.go", module, index))
		}
	}
	inputs.WritePaths = append(inputs.WritePaths, "services/api/file_test.go")
	report, err := CheckRepoPolicy(repo, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionBlock || len(report.Violations) != 1 {
		t.Fatalf("worker without a test must block: decision=%s violations=%+v", report.Decision, report.Violations)
	}
	explanation := report.Violations[0].Explanation
	if !strings.Contains(explanation, "services/worker/file000.go") || strings.Contains(explanation, "no companion matched the captured owner for services/api/") {
		t.Fatalf("missing owners must name only worker sources: %s", explanation)
	}
	want := map[string]int{"services/api/**/*_test.go": 1, "services/worker/**/*_test.go": 1}
	if fmt.Sprint(compiles) != fmt.Sprint(want) {
		t.Fatalf("companion compiles = %v, want %v", compiles, want)
	}
}
