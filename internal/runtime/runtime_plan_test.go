package runtime

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	contractschema "reconc.dev/reconc/internal/schema"
)

func rewriteLockfileWithDigest(t *testing.T, repo string, mutate func(map[string]interface{})) {
	t.Helper()
	lockPath := filepath.Join(repo, compiler.LockfileRelativePath)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	mutate(payload)
	digest, err := compiler.ComputeLockDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload["lock_digest"] = digest
	updated, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimePlanRejectsMalformedCurrentRulesAfterEnvelopeValidation(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{name: "unknown field", mutate: func(rule map[string]interface{}) { rule["unexpected"] = true }},
		{name: "wrong type", mutate: func(rule map[string]interface{}) { rule["paths"] = "generated/**" }},
		{name: "unsupported kind", mutate: func(rule map[string]interface{}) { rule["kind"] = "future_rule" }},
		{name: "invalid shape", mutate: func(rule map[string]interface{}) { delete(rule, "paths") }},
		{name: "unsupported claims", mutate: func(rule map[string]interface{}) {
			rule["claims"] = []interface{}{"reviewed"}
		}},
		{name: "malformed template", mutate: func(rule map[string]interface{}) {
			rule["kind"] = "require_claim"
			rule["when_paths"] = []interface{}{"docs/{task-id}.md"}
		}},
		{name: "cross-kind assurance", mutate: func(rule map[string]interface{}) {
			rule["assurance"] = []interface{}{map[string]interface{}{
				"id": "layout", "type": "repository_layout", "required_root_entries": []interface{}{"go.mod"},
			}}
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := makeRepo(t, "# project\n", "", "rules:\n  - id: generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: block\n    message: generated\n")
			rewriteLockfileWithDigest(t, repo, func(payload map[string]interface{}) {
				rules := payload["rules"].([]interface{})
				test.mutate(rules[0].(map[string]interface{}))
			})
			if _, err := CheckRepoPolicy(repo, Empty()); err == nil || !strings.Contains(err.Error(), "refresh required") {
				t.Fatalf("malformed current rule was accepted: %v", err)
			}
		})
	}
}

func TestRuntimePlanRejectsInvalidAssuranceCommandPolicyAcrossAllLockFormats(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: assurance\n    kind: require_assurance\n    when_paths: ['**']\n    assurance:\n      - id: live\n        type: live_verification\n        commands: ['go test ./...']\n    mode: block\n    message: assurance\n")
	body, err := os.ReadFile(filepath.Join(repo, compiler.LockfileRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	var current map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&current); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1", "2", "3", "4", "5", "6"} {
		for _, commandPolicy := range []interface{}{nil, "unknown"} {
			malformed := cloneRuntimeLockPayload(t, current)
			gate := malformed["rules"].([]interface{})[0].(map[string]interface{})["assurance"].([]interface{})[0].(map[string]interface{})
			if commandPolicy == nil {
				delete(gate, "command_policy")
			} else {
				gate["command_policy"] = commandPolicy
			}
			prepareRuntimeLockVersion(t, malformed, version)
			lock := decodeRuntimeLockPayload(t, malformed)
			if _, err := compileRuntimePlanWithParts(lock.payload, lock.rulesJSON, lock.actionsJSON, lock.actions); err == nil || !strings.Contains(err.Error(), "command_policy must be all or any") {
				t.Fatalf("format-%s command_policy=%v was accepted: %v", version, commandPolicy, err)
			}
		}
	}
}

func TestRuntimePlanPreservesExplicitZeroProofAgeFromCompiledPolicy(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: assurance\n    kind: require_assurance\n    when_paths: ['**']\n    assurance:\n      - id: proof\n        type: substantive_proof\n        proof_file: proof.json\n        max_age_hours: 0\n    mode: block\n    message: assurance\n")
	plan, err := NewEvaluator().loadFreshRuntimePlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.rules) != 1 || len(plan.rules[0].Assurance) != 1 || plan.rules[0].Assurance[0].MaxAgeHours != 0 {
		t.Fatalf("compiled zero-age proof contract = %+v", plan.rules)
	}
	body, err := os.ReadFile(filepath.Join(repo, compiler.LockfileRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"max_age_hours": 0`) {
		t.Fatal("zero-age proof emitted an unnecessary lockfile field instead of the canonical omitted-zero form")
	}
}

func TestRuntimePlanRejectsDeadCheckPathFieldsAcrossAllLockFormats(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: require_claim\n        claims: [approved]\n    mode: block\n    message: gate\n")
	data, err := os.ReadFile(filepath.Join(repo, compiler.LockfileRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	var current map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&current); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1", "2", "3", "4", "5", "6"} {
		version := version
		t.Run("format-"+version, func(t *testing.T) {
			valid := cloneRuntimeLockPayload(t, current)
			prepareRuntimeLockVersion(t, valid, version)
			lock := decodeRuntimeLockPayload(t, valid)
			if _, err := compileRuntimePlanWithParts(lock.payload, lock.rulesJSON, lock.actionsJSON, lock.actions); err != nil {
				t.Fatalf("valid format-%s lock failed: %v", version, err)
			}
			for _, field := range []string{"before_paths", "when_paths"} {
				malformed := cloneRuntimeLockPayload(t, current)
				check := malformed["rules"].([]interface{})[0].(map[string]interface{})["checks"].([]interface{})[0].(map[string]interface{})
				check[field] = []interface{}{"dead/**"}
				prepareRuntimeLockVersion(t, malformed, version)
				lock := decodeRuntimeLockPayload(t, malformed)
				if _, err := compileRuntimePlanWithParts(lock.payload, lock.rulesJSON, lock.actionsJSON, lock.actions); err == nil ||
					!strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), field) {
					t.Fatalf("format-%s check.%s was accepted: %v", version, field, err)
				}
			}
		})
	}
}

func cloneRuntimeLockPayload(t testing.TB, payload map[string]interface{}) map[string]interface{} {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func prepareRuntimeLockVersion(t testing.TB, payload map[string]interface{}, version string) {
	t.Helper()
	payload["$schema"] = contractschema.ResolveVersion(contractschema.PolicyLock, version)
	payload["format_version"] = version
	payload["sources"] = []interface{}{}
	payload["source_count"] = json.Number("0")
	payload["source_digest"] = strings.Repeat("0", 64)
	delete(payload, "lock_digest")
	if version == "1" {
		payload["repo_root"] = "/tmp/reconc-legacy"
		discovery := payload["discovery"].(map[string]interface{})
		discovery["repo_root"] = "/tmp/reconc-legacy"
		discovery["start_path"] = "/tmp/reconc-legacy"
		delete(payload, "actions")
		setLegacyV1SourceDigest(t, payload)
		return
	}
	if version == "2" || version == "3" || version == "4" {
		delete(payload, "actions")
	}
	if version == "5" {
		delete(payload["actions"].(map[string]interface{}), "ledger")
	}
	digest, err := compiler.ComputeLockDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload["lock_digest"] = digest
}

func decodeRuntimeLockPayload(t testing.TB, payload map[string]interface{}) *decodedLockfile {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := decodeLockfile(body)
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestRuntimePlanRejectsMalformedEvidenceContracts(t *testing.T) {
	mutations := []struct {
		name       string
		policyText string
		mutate     func(map[string]interface{})
	}{
		{
			name:       "escaping required file",
			policyText: "rules:\n  - id: proof\n    kind: require_fresh_file\n    when_paths: ['src/**']\n    required_files:\n      - path: proof.md\n    mode: block\n    message: proof\n",
			mutate: func(rule map[string]interface{}) {
				rule["required_files"].([]interface{})[0].(map[string]interface{})["path"] = "../proof.md"
			},
		},
		{
			name:       "assertion-free evidence",
			policyText: "rules:\n  - id: proof\n    kind: require_evidence\n    when_paths: ['src/**']\n    evidence:\n      - file: proof.md\n        must_exist: true\n    mode: block\n    message: proof\n",
			mutate: func(rule map[string]interface{}) {
				delete(rule["evidence"].([]interface{})[0].(map[string]interface{}), "must_exist")
			},
		},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := makeRepo(t, "# project\n", "", test.policyText)
			rewriteLockfileWithDigest(t, repo, func(payload map[string]interface{}) {
				test.mutate(payload["rules"].([]interface{})[0].(map[string]interface{}))
			})
			if _, err := CheckRepoPolicy(repo, Empty()); err == nil || !strings.Contains(err.Error(), "refresh required") {
				t.Fatalf("malformed evidence contract was accepted: %v", err)
			}
		})
	}
}

func TestEvaluatorZeroValueIsUsable(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	var evaluator Evaluator
	if _, err := evaluator.CheckRepoPolicy(repo, Empty()); err != nil {
		t.Fatalf("zero-value evaluator failed: %v", err)
	}
}

func TestRuntimePlanRejectsUnknownEnvelopeAndSourceFields(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{name: "envelope", mutate: func(payload map[string]interface{}) { payload["unexpected"] = true }},
		{name: "discovery", mutate: func(payload map[string]interface{}) {
			payload["discovery"].(map[string]interface{})["unexpected"] = true
		}},
		{name: "source", mutate: func(payload map[string]interface{}) {
			payload["sources"].([]interface{})[0].(map[string]interface{})["unexpected"] = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := makeRepo(t, "# project\n", "", "rules: []\n")
			rewriteLockfileWithDigest(t, repo, test.mutate)
			if _, err := CheckRepoPolicy(repo, Empty()); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("unknown %s field was accepted: %v", test.name, err)
			}
		})
	}
}

func TestRuntimePlanCacheInvalidatesOnSourceAndLockChanges(t *testing.T) {
	withRECONCHome(t)
	policyText := "rules:\n  - id: generated\n    kind: deny_write\n    paths: ['generated/**']\n    mode: block\n    message: generated\n"
	repo := makeRepo(t, "# project\n", "", policyText)
	evaluator := NewEvaluator()
	first, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	second, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil || first != second {
		t.Fatalf("unchanged plan was not reused: first=%p second=%p err=%v", first, second, err)
	}

	policyPath := filepath.Join(repo, "policies", "rules.yml")
	if err := os.WriteFile(policyPath, []byte(strings.Replace(policyText, "generated/**", "dist/**", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.loadFreshRuntimePlan(repo); err == nil || !strings.Contains(err.Error(), "source_digest") {
		t.Fatalf("source drift did not invalidate the plan: %v", err)
	}
	if err := os.WriteFile(policyPath, []byte(policyText), 0o644); err != nil {
		t.Fatal(err)
	}
	restored, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil || restored == first {
		t.Fatalf("restored source did not rebuild the invalidated plan: plan=%p err=%v", restored, err)
	}

	lockPath := filepath.Join(repo, compiler.LockfileRelativePath)
	originalLock, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	rewriteLockfileWithDigest(t, repo, func(payload map[string]interface{}) {
		payload["rules"].([]interface{})[0].(map[string]interface{})["unexpected"] = true
	})
	if _, err := evaluator.loadFreshRuntimePlan(repo); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("lock drift did not invalidate and reject the plan: %v", err)
	}
	if err := os.WriteFile(lockPath, originalLock, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.loadFreshRuntimePlan(repo); err != nil {
		t.Fatalf("restored lockfile did not rebuild: %v", err)
	}
}

func TestRuntimePlanConcurrentSameRootPublishesOnePlan(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules: []\n")
	evaluator := NewEvaluator()
	const workers = 16
	plans := make(chan *runtimePlan, workers)
	errors := make(chan error, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			plan, err := evaluator.loadRuntimePlan(repo)
			plans <- plan
			errors <- err
		}()
	}
	group.Wait()
	close(plans)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *runtimePlan
	for plan := range plans {
		if first == nil {
			first = plan
			continue
		}
		if plan != first {
			t.Fatalf("same-root concurrent load published multiple plans: first=%p other=%p", first, plan)
		}
	}
	if len(evaluator.loads) != 0 || len(evaluator.plans) != 1 {
		t.Fatalf("load state leaked: active=%d cached=%d", len(evaluator.loads), len(evaluator.plans))
	}
}

func TestTypedEvaluatorMatchesOneShotReports(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "default_mode: warn\n", `rules:
  - id: generated
    kind: deny_write
    paths: ['generated/**']
    mode: block
    message: generated
  - id: tests
    kind: require_command_success
    when_paths: ['src/**']
    commands: ['go test ./...']
    mode: warn
    message: tests
  - id: shell
    kind: all_of
    when_paths: ['src/**']
    checks:
      - kind: require_claim
        claims: ['reviewed']
      - kind: forbid_command
        commands: ['rm -rf']
    mode: warn
    message: shell
`)
	inputs := []ExecutionInputs{
		Empty(),
		{WritePaths: []string{"generated/out.go"}},
		{WritePaths: []string{"src/main.go"}, Claims: []string{"reviewed"}, CommandResults: []CommandResult{{Command: "go test ./...", Outcome: CommandOutcomeSuccess}}},
	}
	evaluator := NewEvaluator()
	for index, input := range inputs {
		oneShot, err := CheckRepoPolicy(repo, input)
		if err != nil {
			t.Fatalf("one-shot case %d: %v", index, err)
		}
		reused, err := evaluator.CheckRepoPolicy(repo, input)
		if err != nil {
			t.Fatalf("reused case %d: %v", index, err)
		}
		if !reflect.DeepEqual(oneShot, reused) {
			t.Fatalf("case %d report drift:\none-shot=%+v\nreused=%+v", index, oneShot, reused)
		}
	}
}

func TestMigratedRuntimePlanRetainsEmbeddedRuleParityCheck(t *testing.T) {
	withRECONCHome(t)
	policyText := "rules:\n  - id: r\n    kind: deny_write\n    paths: ['x']\n    mode: block\n    message: x\n"
	repo := makeRepo(t, "# project\n", "", policyText)
	rewriteLockfile(t, repo, func(payload map[string]interface{}) {
		payload["$schema"] = compiler.LegacyLockfileSchemaV1
		payload["format_version"] = "1"
		delete(payload, "actions")
		payload["repo_root"] = repo
		payload["sources"] = []interface{}{
			map[string]interface{}{"kind": "agents_md", "path": "AGENTS.md", "content": "# project\n"},
			map[string]interface{}{"kind": "policy_file", "path": "policies/rules.yml", "content": policyText},
		}
		setLegacyV1SourceDigest(t, payload)
		discovery := payload["discovery"].(map[string]interface{})
		discovery["repo_root"] = repo
		discovery["start_path"] = repo
		payload["rules"].([]interface{})[0].(map[string]interface{})["mode"] = "observe"
	})
	if _, err := CheckRepoPolicy(repo, Empty()); err == nil || !strings.Contains(err.Error(), "rules do not match") {
		t.Fatalf("migrated embedded-rule drift was accepted: %v", err)
	}
}

// TestRuntimePlanRejectsUnbindableCacheInputs re-checks the declared script
// inputs against the shape the Stop fingerprint can bind, so a hand-edited
// lockfile cannot smuggle a glob or an escaping path past the compiler.
func TestRuntimePlanRejectsUnbindableCacheInputs(t *testing.T) {
	const policyText = "rules:\n  - id: gate\n    kind: require_script\n    when_paths: ['src/**']\n    script: 'scripts/check.sh'\n    cache_inputs: ['build/report.json']\n    mode: block\n    message: gate\n"
	for _, test := range []struct {
		name  string
		value []interface{}
	}{
		{name: "glob", value: []interface{}{"build/**/*.json"}},
		{name: "escaping path", value: []interface{}{"../outside.json"}},
		// Both rooting conventions, because the same policy file must be
		// refused on Unix and on Windows.
		{name: "absolute path", value: []interface{}{"/etc/passwd"}},
		{name: "windows drive path", value: []interface{}{`C:\Windows\hosts`}},
		{name: "windows unc path", value: []interface{}{`\\server\share\report.json`}},
		{name: "backslash rooted path", value: []interface{}{`\etc\passwd`}},
		{name: "backslash escaping path", value: []interface{}{`..\outside.json`}},
		{name: "duplicate", value: []interface{}{"build/report.json", "build/report.json"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := makeRepo(t, "# project\n", "", policyText)
			rewriteLockfileWithDigest(t, repo, func(payload map[string]interface{}) {
				rule := payload["rules"].([]interface{})[0].(map[string]interface{})
				rule["cache_inputs"] = test.value
			})
			if _, err := CheckRepoPolicy(repo, Empty()); err == nil || !strings.Contains(err.Error(), "refresh required") {
				t.Fatalf("unbindable cache_inputs was accepted: %v", err)
			}
		})
	}
}

// TestRuntimePlanCarriesDeclaredCacheInputs guards the accepted side so the
// rejection cases cannot pass by refusing everything.
func TestRuntimePlanCarriesDeclaredCacheInputs(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "",
		"rules:\n  - id: gate\n    kind: require_script\n    when_paths: ['src/**']\n    script: 'scripts/check.sh'\n    cache_inputs: ['build/report.json', 'STATUS.md']\n    mode: block\n    message: gate\n")
	if _, err := CheckRepoPolicy(repo, Empty()); err != nil {
		t.Fatalf("declared cache_inputs was rejected: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".reconc", "policy.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Rules []struct {
			CacheInputs []string `json:"cache_inputs"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(body, &lock); err != nil {
		t.Fatal(err)
	}
	if len(lock.Rules) != 1 || len(lock.Rules[0].CacheInputs) != 2 ||
		lock.Rules[0].CacheInputs[0] != "build/report.json" || lock.Rules[0].CacheInputs[1] != "STATUS.md" {
		t.Fatalf("compiled lockfile did not carry the declaration: %s", body)
	}
}

// TestRuntimePlanRejectsUnbindableCacheInputsInComposites is the sub-check half
// of the same contract: a composite carries its own script entries, and a
// hand-edited lockfile must not smuggle an unbindable declaration through them.
func TestRuntimePlanRejectsUnbindableCacheInputsInComposites(t *testing.T) {
	const policyText = "rules:\n  - id: gate\n    kind: all_of\n    when_paths: ['src/**']\n    checks:\n      - kind: require_script\n        script: 'scripts/check.sh'\n        cache_inputs: ['build/report.json']\n    mode: block\n    message: gate\n"
	for _, test := range []struct {
		name  string
		value []interface{}
	}{
		{name: "glob", value: []interface{}{"build/**/*.json"}},
		{name: "escaping path", value: []interface{}{"../outside.json"}},
		{name: "duplicate", value: []interface{}{"build/report.json", "build/report.json"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := makeRepo(t, "# project\n", "", policyText)
			rewriteLockfileWithDigest(t, repo, func(payload map[string]interface{}) {
				rule := payload["rules"].([]interface{})[0].(map[string]interface{})
				check := rule["checks"].([]interface{})[0].(map[string]interface{})
				check["cache_inputs"] = test.value
			})
			if _, err := CheckRepoPolicy(repo, Empty()); err == nil || !strings.Contains(err.Error(), "refresh required") {
				t.Fatalf("unbindable composite cache_inputs was accepted: %v", err)
			}
		})
	}
}

func TestRuntimePlanReusesImmutableCommandExpectationsAcrossEvaluations(t *testing.T) {
	withRECONCHome(t)
	repo := makeRepo(t, "# project\n", "", "rules:\n  - id: tests\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['go test ./...']\n    mode: block\n    message: tests\n  - id: shell\n    kind: forbid_command\n    commands: ['rm -rf']\n    mode: block\n    message: shell\n")
	evaluator := NewEvaluator()
	plan, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	expectations := plan.commandExpectations
	if expectations == nil || expectations.repoRoot != repo || len(expectations.expected) != 2 {
		t.Fatalf("prepared command expectations = %#v", expectations)
	}

	firstCache := newCommandInvocationCache(expectations)
	secondCache := newCommandInvocationCache(expectations)
	firstCache.observedInvocations("go test ./...")
	if firstCache.commandExpectationPlan != secondCache.commandExpectationPlan || len(secondCache.observed) != 0 {
		t.Fatal("immutable expectations were not shared independently of evaluation-local observed state")
	}

	inputs := ExecutionInputs{
		WritePaths: []string{"src/main.go"},
		CommandResults: []CommandResult{{
			Command: "go test ./...", Outcome: CommandOutcomeSuccess,
		}},
	}
	firstReport, err := evaluateRuntimePlan(repo, plan, inputs, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	secondReport, err := evaluateRuntimePlan(repo, plan, inputs, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := evaluator.loadFreshRuntimePlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstReport, secondReport) || reloaded != plan || reloaded.commandExpectations != expectations {
		t.Fatal("repeated evaluation rebuilt command expectations or changed decisions")
	}
}

func TestRuntimePlanCommandExpectationsPreserveRootAnchoredPolicySemantics(t *testing.T) {
	withRECONCHome(t)
	repo := t.TempDir()
	writeFile(t, repo, "AGENTS.md", "# project\n")
	writeFile(t, repo, "policies/rules.yml", "rules:\n  - id: tests\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['cd "+repo+" && go test ./...']\n    mode: block\n    message: tests\n")
	if _, err := compiler.CompileRepoPolicy(repo, "0.1.0-test"); err != nil {
		t.Fatal(err)
	}
	report, err := NewEvaluator().CheckRepoPolicy(repo, ExecutionInputs{
		WritePaths: []string{"src/main.go"},
		CommandResults: []CommandResult{{
			Command: "go test ./...", Outcome: CommandOutcomeSuccess,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionPass {
		t.Fatalf("root-anchored prepared expectation decision = %s, violations=%+v", report.Decision, report.Violations)
	}
}
