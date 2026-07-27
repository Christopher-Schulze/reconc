package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/policy"
)

func TestCompositeCheckEvaluatorCoversEverySupportedBehavior(t *testing.T) {
	repo := t.TempDir()
	ctx := &evalContext{repoRoot: repo}
	fresh := filepath.Join(repo, "fresh.txt")
	stale := filepath.Join(repo, "stale.txt")
	evidence := filepath.Join(repo, "evidence.txt")
	if err := os.WriteFile(fresh, []byte("fresh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidence, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		check      policy.Check
		inputs     ExecutionInputs
		minimum    uint64
		wantOK     bool
		wantReason string
		wantError  string
	}{
		{name: "fresh", check: policy.Check{Kind: policy.KindRequireFreshFile, Path: "fresh.txt", MaxAgeHours: 1}, wantOK: true},
		{name: "fresh optional missing", check: policy.Check{Kind: policy.KindRequireFreshFile, Path: "missing", Optional: true}, wantOK: true},
		{name: "fresh missing", check: policy.Check{Kind: policy.KindRequireFreshFile, Path: "missing"}, wantReason: "missing file"},
		{name: "fresh stale", check: policy.Check{Kind: policy.KindRequireFreshFile, Path: "stale.txt", MaxAgeHours: 1}, wantReason: "stale"},
		{name: "fresh directory", check: policy.Check{Kind: policy.KindRequireFreshFile, Path: "directory"}, wantReason: "not a regular file"},
		{name: "fresh bad template", check: policy.Check{Kind: policy.KindRequireFreshFile, Path: "{missing}"}, wantError: "check path"},
		{name: "evidence absent unconstrained", check: policy.Check{Kind: policy.KindRequireEvidence, File: "missing"}, wantOK: true},
		{name: "evidence optional", check: policy.Check{Kind: policy.KindRequireEvidence, File: "missing", MustExist: true, Optional: true}, wantOK: true},
		{name: "evidence required", check: policy.Check{Kind: policy.KindRequireEvidence, File: "missing", MustExist: true}, wantReason: "missing file"},
		{name: "evidence absent content", check: policy.Check{Kind: policy.KindRequireEvidence, File: "missing", MustContain: []string{"x"}}, wantReason: "cannot check content"},
		{name: "evidence directory", check: policy.Check{Kind: policy.KindRequireEvidence, File: "directory", MustExist: true}, wantReason: "not a regular file"},
		{name: "evidence existence only", check: policy.Check{Kind: policy.KindRequireEvidence, File: "evidence.txt", MustExist: true}, wantOK: true},
		{name: "evidence contains", check: policy.Check{Kind: policy.KindRequireEvidence, File: "evidence.txt", MustContain: []string{"alpha"}}, wantOK: true},
		{name: "evidence lacks", check: policy.Check{Kind: policy.KindRequireEvidence, File: "evidence.txt", MustContain: []string{"gamma"}}, wantReason: "missing required substring"},
		{name: "evidence forbidden", check: policy.Check{Kind: policy.KindRequireEvidence, File: "evidence.txt", MustNotContain: "beta"}, wantReason: "forbidden substring"},
		{name: "evidence lines", check: policy.Check{Kind: policy.KindRequireEvidence, File: "evidence.txt", MaxLineCount: 1}, wantReason: "lines > max"},
		{name: "evidence bad template", check: policy.Check{Kind: policy.KindRequireEvidence, File: "{missing}"}, wantError: "check file"},
		{name: "claim pass", check: policy.Check{Kind: policy.KindRequireClaim, Claims: []string{"approved"}}, inputs: ExecutionInputs{Claims: []string{"approved"}}, wantOK: true},
		{name: "claim fail", check: policy.Check{Kind: policy.KindRequireClaim, Claims: []string{"approved"}}, wantReason: "no required claim"},
		{name: "command pass", check: policy.Check{Kind: policy.KindRequireCommand, Commands: []string{"go test ./..."}}, inputs: ExecutionInputs{Commands: []string{"go test ./..."}}, wantOK: true},
		{name: "command fail", check: policy.Check{Kind: policy.KindRequireCommand, Commands: []string{"go test ./..."}}, wantReason: "no required command ran"},
		{name: "command success pass", check: policy.Check{Kind: policy.KindRequireCommandSuccess, Commands: []string{"go test ./..."}}, inputs: ExecutionInputs{CommandResults: []CommandResult{{Command: "go test ./...", Outcome: CommandOutcomeSuccess, EvidenceEpoch: 2}}}, minimum: 1, wantOK: true},
		{name: "command success stale", check: policy.Check{Kind: policy.KindRequireCommandSuccess, Commands: []string{"go test ./..."}}, inputs: ExecutionInputs{CommandResults: []CommandResult{{Command: "go test ./...", Outcome: CommandOutcomeSuccess, EvidenceEpoch: 1}}}, minimum: 2, wantReason: "after the triggering write"},
		{name: "forbid pass", check: policy.Check{Kind: policy.KindForbidCommand, Commands: []string{"rm -rf"}}, inputs: ExecutionInputs{Commands: []string{"go test ./..."}}, wantOK: true},
		{name: "forbid fail", check: policy.Check{Kind: policy.KindForbidCommand, Commands: []string{"rm -rf"}, CommandMatch: policy.CommandMatchPrefix}, inputs: ExecutionInputs{Commands: []string{"rm -rf build"}}, wantReason: "forbidden command"},
		{name: "deny pass", check: policy.Check{Kind: policy.KindDenyWrite, Paths: []string{"generated/**"}}, inputs: ExecutionInputs{WritePaths: []string{"docs/readme.md"}}, wantOK: true},
		{name: "deny fail", check: policy.Check{Kind: policy.KindDenyWrite, Paths: []string{"generated/**"}}, inputs: ExecutionInputs{WritePaths: []string{"generated/out.go"}}, wantReason: "forbidden paths"},
		{name: "script missing", check: policy.Check{Kind: policy.KindRequireScript}, wantError: "missing script"},
		{name: "unsupported", check: policy.Check{Kind: "future"}, wantReason: "unsupported check kind"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ok, reason, err := evalCheck(ctx, test.check, map[string]string{}, test.inputs, test.minimum)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want substring %q", err, test.wantError)
				}
				return
			}
			if err != nil || ok != test.wantOK || !strings.Contains(reason, test.wantReason) {
				t.Fatalf("evalCheck() = %t, %q, %v", ok, reason, err)
			}
		})
	}
}

func TestCompositeRuleFoldsRemainFailClosed(t *testing.T) {
	ctx := &evalContext{repoRoot: t.TempDir()}
	base := map[string]interface{}{
		"id":         "composite",
		"kind":       "all_of",
		"mode":       "block",
		"message":    "requirements",
		"when_paths": []interface{}{"src/**"},
	}
	claim := map[string]interface{}{"kind": "require_claim", "claims": []interface{}{"approved"}}
	command := map[string]interface{}{"kind": "require_command", "commands": []interface{}{"go test ./..."}}
	inputs := ExecutionInputs{WritePaths: []string{"src/app.go"}, WriteEpochs: map[string]uint64{"src/app.go": 1}}

	all := cloneCompositeRule(base)
	all["checks"] = []interface{}{claim, command}
	if violation, err := evalAllOf(ctx, all, policy.ModeBlock, inputs); err != nil || violation == nil ||
		!strings.Contains(violation.Explanation, "check #1 require_claim") ||
		!strings.Contains(violation.Explanation, "check #2 require_command") {
		t.Fatalf("all_of result = %+v, %v", violation, err)
	}
	inputs.Claims = []string{"approved"}
	inputs.Commands = []string{"go test ./..."}
	if violation, err := evalAllOf(ctx, all, policy.ModeBlock, inputs); err != nil || violation != nil {
		t.Fatalf("passing all_of = %+v, %v", violation, err)
	}

	any := cloneCompositeRule(base)
	any["kind"] = "any_of"
	any["checks"] = []interface{}{claim, command}
	inputs.Claims = nil
	inputs.Commands = nil
	if violation, err := evalAnyOf(ctx, any, policy.ModeBlock, inputs); err != nil || violation == nil {
		t.Fatalf("failing any_of = %+v, %v", violation, err)
	}
	inputs.Claims = []string{"approved"}
	if violation, err := evalAnyOf(ctx, any, policy.ModeBlock, inputs); err != nil || violation != nil {
		t.Fatalf("passing any_of = %+v, %v", violation, err)
	}

	not := cloneCompositeRule(base)
	not["kind"] = "not"
	not["checks"] = []interface{}{claim}
	if violation, err := evalNot(ctx, not, policy.ModeBlock, inputs); err != nil || violation == nil {
		t.Fatalf("failing not = %+v, %v", violation, err)
	}
	inputs.Claims = nil
	if violation, err := evalNot(ctx, not, policy.ModeBlock, inputs); err != nil || violation != nil {
		t.Fatalf("passing not = %+v, %v", violation, err)
	}
	not["checks"] = []interface{}{claim, command}
	if _, err := evalNot(ctx, not, policy.ModeBlock, inputs); err == nil || !strings.Contains(err.Error(), "exactly one check") {
		t.Fatalf("invalid not error = %v", err)
	}
	noMatch := cloneCompositeRule(base)
	noMatch["checks"] = []interface{}{claim}
	if violation, err := evalAllOf(ctx, noMatch, policy.ModeBlock, ExecutionInputs{WritePaths: []string{"docs/readme.md"}}); err != nil || violation != nil {
		t.Fatalf("non-matching composite = %+v, %v", violation, err)
	}
}

func TestCompositeCheckDecodingPreservesEveryTypedField(t *testing.T) {
	raw := map[string]interface{}{
		"kind":             "require_evidence",
		"path":             "fresh.txt",
		"max_age_hours":    float64(2),
		"file":             "evidence.txt",
		"must_exist":       true,
		"must_contain":     []interface{}{"one", 2, "two"},
		"must_not_contain": "secret",
		"max_line_count":   float64(3),
		"script":           "check.sh",
		"args":             []interface{}{"--strict", 2},
		"timeout_sec":      float64(4),
		"paths":            []interface{}{"a/**"},
		"before_paths":     []interface{}{"b/**"},
		"when_paths":       []interface{}{"c/**"},
		"commands":         []interface{}{"go test"},
		"claims":           []interface{}{"approved"},
		"command_match":    "prefix",
		"optional":         true,
	}
	check, ok := checkFromMap(raw)
	if !ok || check.Kind != policy.KindRequireEvidence || check.MaxAgeHours != 2 || check.MaxLineCount != 3 ||
		check.TimeoutSec != 4 || strings.Join(check.MustContain, ",") != "one,two" ||
		strings.Join(check.Args, ",") != "--strict" || !check.MustExist || !check.Optional ||
		check.CommandMatch != policy.CommandMatchPrefix {
		t.Fatalf("decoded check = %+v, %t", check, ok)
	}
	for _, field := range [][]string{check.Paths, check.BeforePaths, check.WhenPaths, check.Commands, check.Claims} {
		if len(field) != 1 {
			t.Fatalf("decoded list field = %v", field)
		}
	}
	if _, ok := checkFromMap(map[string]interface{}{}); ok {
		t.Fatal("check without kind was accepted")
	}
	checks := checksFromRule(map[string]interface{}{"checks": []interface{}{raw, "invalid", map[string]interface{}{}}})
	if len(checks) != 1 {
		t.Fatalf("decoded checks = %+v", checks)
	}
	if checks := checksFromRule(map[string]interface{}{"checks": "invalid"}); checks != nil {
		t.Fatalf("non-list checks = %+v", checks)
	}
}

func cloneCompositeRule(source map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
