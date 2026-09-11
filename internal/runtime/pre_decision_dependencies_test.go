package runtime

import (
	"reflect"
	"strconv"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestPreDecisionDependenciesFollowCommandCompositeReachability(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", `rules:
  - id: command-gate
    kind: any_of
    when_paths: ['src/{name}.go']
    checks:
      - kind: forbid_command
        commands: ['danger']
      - kind: require_fresh_file
        path: 'status/{name}.txt'
        max_age_hours: 2
      - kind: require_evidence
        file: 'proof/{name}.txt'
        must_exist: true
      - kind: require_script
        script: 'scripts/check.sh'
        cache_inputs: ['inputs/data.json']
    mode: block
    message: gate
`)
	compiled, _, err := NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatal(err)
	}
	inputs := ExecutionInputs{WritePaths: []string{"src/main.go"}, Commands: []string{"danger"}}
	plan, err := compiled.PreDecisionDependencies(repo, inputs, PreDecisionRouteCommand)
	if err != nil {
		t.Fatal(err)
	}
	want := []PreDecisionDependency{
		{Path: "inputs/data.json", FreshnessHours: []int{}, ContentBound: true},
		{Path: "proof/main.txt", FreshnessHours: []int{}, ContentBound: true},
		{Path: "scripts/check.sh", FreshnessHours: []int{}, ContentBound: true},
		{Path: "status/main.txt", FreshnessHours: []int{2}, ContentBound: true},
	}
	if !plan.Cacheable || plan.ScriptEnvironmentIdentity == "" || !reflect.DeepEqual(plan.Dependencies, want) {
		t.Fatalf("dependency plan = %+v, want dependencies %+v", plan, want)
	}

	unreached, err := compiled.PreDecisionDependencies(repo,
		ExecutionInputs{WritePaths: []string{"src/main.go"}, Commands: []string{"safe"}}, PreDecisionRouteCommand)
	if err != nil {
		t.Fatal(err)
	}
	if !unreached.Cacheable || len(unreached.Dependencies) != 0 || unreached.ScriptEnvironmentIdentity != "" {
		t.Fatalf("unreached command retained dependencies: %+v", unreached)
	}
}

func TestPreDecisionDependenciesMirrorPreWriteCompositeSemantics(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", `rules:
  - id: write-gate
    kind: all_of
    when_paths: ['src/**']
    checks:
      - kind: deny_write
        paths: ['generated/**']
      - kind: require_evidence
        file: 'proof.txt'
        must_exist: true
    mode: block
    message: gate
`)
	compiled, _, err := NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compiled.PreDecisionDependencies(repo,
		ExecutionInputs{WritePaths: []string{"src/main.go"}}, PreDecisionRouteWrite)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Cacheable || len(plan.Dependencies) != 0 {
		t.Fatalf("pre-write all_of retained completion-only dependencies: %+v", plan)
	}
}

func TestPreDecisionDependenciesDisableOpaqueScripts(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", `rules:
  - id: opaque
    kind: any_of
    when_paths: ['src/**']
    checks:
      - kind: forbid_command
        commands: ['danger']
      - kind: require_script
        script: 'scripts/check.sh'
    mode: block
    message: gate
`)
	compiled, _, err := NewEvaluator().CurrentCompiledPolicyEvaluator(repo)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compiled.PreDecisionDependencies(repo,
		ExecutionInputs{WritePaths: []string{"src/main.go"}, Commands: []string{"danger"}}, PreDecisionRouteCommand)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cacheable || len(plan.Dependencies) != 1 || plan.Dependencies[0].Path != "scripts/check.sh" || plan.ScriptEnvironmentIdentity == "" {
		t.Fatalf("opaque script dependency plan = %+v", plan)
	}
}

func TestPreDecisionDependencyCollectorFailsClosedAtCapacity(t *testing.T) {
	collector := newPreDecisionDependencyCollector()
	for index := 0; index <= MaxPreDecisionDependencyPaths; index++ {
		if err := collector.add("proof/"+strconv.Itoa(index), 0, true, nil); err != nil {
			t.Fatal(err)
		}
	}
	if plan := collector.plan(); plan.Cacheable || len(plan.Dependencies) != MaxPreDecisionDependencyPaths {
		t.Fatalf("over-capacity dependency plan = %+v", plan)
	}
}

func TestPreDecisionDependencyCollectorDisablesReachedAssurance(t *testing.T) {
	collector := newPreDecisionDependencyCollector()
	err := collector.addRule(policy.Rule{
		Kind: policy.KindRequireAssurance,
		Assurance: []policy.AssuranceGate{{
			Type: policy.AssuranceSubstantiveProof, ProofFile: "proof.json", MaxAgeHours: 24,
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := collector.plan()
	want := []PreDecisionDependency{{Path: "proof.json", FreshnessHours: []int{24}, ContentBound: true}}
	if plan.Cacheable || !reflect.DeepEqual(plan.Dependencies, want) {
		t.Fatalf("assurance dependency plan = %+v, want uncacheable dependencies %+v", plan, want)
	}
}
