package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreDecisionEmptyRoutesRetainPathValidation(t *testing.T) {
	withRECONCHome(t)
	policies := []struct{ name, body string }{
		{"empty", "rules: []\n"},
		{"completion-only", "rules:\n  - id: read-first\n    kind: require_read\n    paths: ['src/**']\n    before_paths: ['README.md']\n    mode: block\n    message: read first\n"},
	}
	for _, policy := range policies {
		t.Run(policy.name, func(t *testing.T) {
			repo := makeRepo(t, "# project\n", "", policy.body)
			outside := t.TempDir()
			if err := os.Symlink(outside, filepath.Join(repo, "outside")); err != nil {
				t.Fatal(err)
			}
			compiled, _, err := NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
			if err != nil {
				t.Fatal(err)
			}
			cases := []struct {
				name, root, path, errorText string
			}{
				{"prospective", repo, "src/new.go", ""},
				{"root", repo, ".", ""},
				{"empty", repo, "", ""},
				{"absolute-inside", repo, filepath.Join(repo, "src/new.go"), ""},
				{"lexical-escape", repo, "../escaped", "escapes"},
				{"absolute-escape", repo, filepath.Join(outside, "escaped"), "escapes"},
				{"symlink-escape", repo, "outside/escaped", "escapes"},
				{"invalid-path", repo, "invalid\x00path", "resolve evidence path"},
				{"missing-root", filepath.Join(repo, "missing-root"), "file", "resolve repo filesystem identity"},
			}
			for _, route := range []PreDecisionRoute{PreDecisionRouteCommand, PreDecisionRouteWrite} {
				for _, read := range []bool{false, true} {
					for _, test := range cases {
						t.Run(fmt.Sprintf("%s/route-%d/read-%t", test.name, route, read), func(t *testing.T) {
							inputs := ExecutionInputs{
								WritePaths: []string{test.path}, WriteEpochs: map[string]uint64{test.path: 7},
								Commands: []string{"echo  safe"}, Claims: []string{"claim"},
								CommandResults: []CommandResult{{Command: "go test", Outcome: CommandOutcomeSuccess}},
							}
							if read {
								inputs.ReadPaths, inputs.WritePaths = inputs.WritePaths, nil
							}
							plan, err := compiled.PreDecisionDependencies(test.root, inputs, route)
							if test.errorText != "" {
								if err == nil || !strings.Contains(err.Error(), test.errorText) || plan.Cacheable {
									t.Fatalf("route=%d read=%t plan=%+v error=%v, want %q", route, read, plan, err, test.errorText)
								}
								return
							}
							if err != nil || !plan.Cacheable || len(plan.Dependencies) != 0 || plan.ScriptEnvironmentIdentity != "" {
								t.Fatalf("route=%d read=%t plan=%+v error=%v", route, read, plan, err)
							}
						})
					}
				}
			}
		})
	}
}

func TestPreDecisionEmptyRoutesDoNotAllocateForUnusedEvidence(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	compiled, _, err := NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatal(err)
	}
	minimal := ExecutionInputs{WritePaths: []string{"src/new.go"}}
	large := minimal
	for index := range 128 {
		command := fmt.Sprintf("echo  value-%d", index)
		large.Commands = append(large.Commands, command)
		large.Claims = append(large.Claims, command)
		large.CommandResults = append(large.CommandResults, CommandResult{Command: command, Outcome: CommandOutcomeSuccess})
	}
	for _, route := range []PreDecisionRoute{PreDecisionRouteCommand, PreDecisionRouteWrite} {
		measure := func(inputs ExecutionInputs) float64 {
			return testing.AllocsPerRun(10, func() {
				plan, err := compiled.PreDecisionDependencies(repo, inputs, route)
				if err != nil || !plan.Cacheable || len(plan.Dependencies) != 0 {
					t.Fatalf("route=%d plan=%+v error=%v", route, plan, err)
				}
			})
		}
		smallAllocs, largeAllocs := measure(minimal), measure(large)
		if largeAllocs > smallAllocs {
			t.Fatalf("route=%d unused evidence adds allocations: minimal=%g large=%g", route, smallAllocs, largeAllocs)
		}
	}
}
