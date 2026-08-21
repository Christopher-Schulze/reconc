package runtime

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/policy"
)

func TestWorkflowAuditBatchCandidateRejectsEveryProtocolMismatch(t *testing.T) {
	base := policy.Rule{
		Kind:           policy.KindRequireScript,
		Script:         "tools/reconc/audits/run-workflow-audit",
		Args:           []string{"task-state"},
		TimeoutSec:     12,
		KillTimeoutSec: 3,
	}
	script, mode, timeout, killTimeout, ok := workflowAuditBatchCandidate(&base)
	if !ok || script != "tools/reconc/audits/run-workflow-audit" || mode != "task-state" || timeout != 12 || killTimeout != 3 {
		t.Fatalf("valid candidate = (%q, %q, %d, %d, %t)", script, mode, timeout, killTimeout, ok)
	}

	tests := []struct {
		name   string
		change func(*policy.Rule)
	}{
		{name: "wrong kind", change: func(rule *policy.Rule) { rule.Kind = policy.KindDenyWrite }},
		{name: "missing script", change: func(rule *policy.Rule) { rule.Script = "" }},
		{name: "template script", change: func(rule *policy.Rule) { rule.Script = "{root}/audits/run-workflow-audit" }},
		{name: "wrong basename", change: func(rule *policy.Rule) { rule.Script = "audits/run-other" }},
		{name: "no args", change: func(rule *policy.Rule) { rule.Args = nil }},
		{name: "multiple args", change: func(rule *policy.Rule) { rule.Args = []string{"a", "b"} }},
		{name: "empty mode", change: func(rule *policy.Rule) { rule.Args = []string{""} }},
		{name: "template mode", change: func(rule *policy.Rule) { rule.Args = []string{"{mode}"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := base
			rule.Args = append([]string(nil), base.Args...)
			test.change(&rule)
			if _, _, _, _, ok := workflowAuditBatchCandidate(&rule); ok {
				t.Fatalf("workflowAuditBatchCandidate(%#v) accepted protocol mismatch", rule)
			}
		})
	}
}

func TestWorkflowAuditBatchHelpersPreserveModeAndFailureTruth(t *testing.T) {
	items := []workflowAuditBatchItem{{mode: "task-state"}, {mode: "repo-layout"}, {mode: "task-state"}}
	if got, want := uniqueBatchModes(items), []string{"task-state", "repo-layout"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueBatchModes() = %#v, want %#v", got, want)
	}

	valid := `{"results":[{"mode":"task-state","failures":[]},{"mode":"repo-layout","failures":["bad root"]}]}`
	parsed, ok := parseWorkflowAuditBatchOutput(valid, []string{"task-state", "repo-layout"})
	if !ok || len(parsed["task-state"]) != 0 || !reflect.DeepEqual(parsed["repo-layout"], []string{"bad root"}) {
		t.Fatalf("parseWorkflowAuditBatchOutput() = (%#v, %t)", parsed, ok)
	}
	for _, invalid := range []string{"not-json", `{"results":[{"mode":"task-state","failures":[]}]}`} {
		if parsed, ok := parseWorkflowAuditBatchOutput(invalid, []string{"task-state", "repo-layout"}); ok || parsed != nil {
			t.Fatalf("invalid batch output accepted: %#v", invalid)
		}
	}

	contexts := []matchContext{{path: "a.go"}, {path: "b.go"}, {path: "a.go"}}
	if got, want := triggeredPathsForContexts(contexts), []string{"a.go", "b.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("triggeredPathsForContexts() = %#v, want %#v", got, want)
	}
	if got := batchScriptFailureDetails("audits/run-workflow-audit", []string{" ", "failure"}); !reflect.DeepEqual(got, []string{"script audits/run-workflow-audit blocked: failure"}) {
		t.Fatalf("batchScriptFailureDetails() = %#v", got)
	}
	if got := batchScriptFailureDetails("audit", nil); !reflect.DeepEqual(got, []string{"script audit blocked: no output"}) {
		t.Fatalf("empty batchScriptFailureDetails() = %#v", got)
	}
	for _, action := range []string{
		batchScriptRecommendedAction([]string{strings.Repeat("ü", 700)}),
		scriptRecommendedAction([]string{strings.Repeat("ü", 700)}),
	} {
		if len([]rune(action)) > 640 || !strings.HasSuffix(action, "...") {
			t.Fatalf("recommended action is not rune-bounded: %d runes", len([]rune(action)))
		}
	}
}

func TestPathAndEpochNormalizationRejectsEscapesAndKeepsStrongestEpoch(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested", "file.go")
	got, err := normalizePaths([]string{"", ".", "nested\\file.go", nested}, root)
	if err != nil {
		t.Fatalf("normalizePaths: %v", err)
	}
	want := []string{"nested\\file.go", "nested/file.go"}
	if filepath.Separator == '\\' {
		want = []string{"nested/file.go", "nested/file.go"}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizePaths() = %#v, want %#v", got, want)
	}
	if _, err := normalizePaths([]string{"../outside"}, root); err == nil {
		t.Fatal("repo escape must fail")
	} else {
		var boundary *rerrors.RepoBoundaryError
		if !errors.As(err, &boundary) {
			t.Fatalf("repo escape error = %T %v, want RepoBoundaryError", err, err)
		}
	}

	epochs, err := normalizeWriteEpochs(
		[]string{"nested\\file.go", "nested/file.go", "."},
		map[string]uint64{"nested\\file.go": 3, "nested/file.go": 7, ".": 11},
		root,
	)
	if err != nil {
		t.Fatalf("normalizeWriteEpochs: %v", err)
	}
	wantEpochs := map[string]uint64{"nested\\file.go": 3, "nested/file.go": 7}
	if filepath.Separator == '\\' {
		wantEpochs = map[string]uint64{"nested/file.go": 7}
	}
	if !reflect.DeepEqual(epochs, wantEpochs) {
		t.Fatalf("normalizeWriteEpochs() = %#v", epochs)
	}

	absolute := filepath.Join(root, "nested", "file.go")
	relative := RelativizeEpochKeys(root, map[string]uint64{
		absolute:                    5,
		"nested/file.go":            9,
		filepath.Dir(root) + "/out": 12,
	})
	if relative[absolute] != 5 || relative["nested/file.go"] != 9 {
		t.Fatalf("RelativizeEpochKeys() = %#v", relative)
	}
}

func TestCommandEvidenceHelpersDropNoiseWithoutWeakeningOutcomes(t *testing.T) {
	if got := normalizeCommands([]string{" ", "go  test", "echo x\nprintf y"}); !reflect.DeepEqual(got, []string{"go test", "echo x ; printf y"}) {
		t.Fatalf("normalizeCommands() = %#v", got)
	}
	results := normalizeCommandResults([]CommandResult{
		{Command: " ", Outcome: CommandOutcomeSuccess, EvidenceEpoch: 1},
		{Command: "go  test", Outcome: CommandOutcomeFailure, EvidenceEpoch: 2},
	})
	if !reflect.DeepEqual(results, []CommandResult{{Command: "go test", Outcome: CommandOutcomeFailure, EvidenceEpoch: 2}}) {
		t.Fatalf("normalizeCommandResults() = %#v", results)
	}
	if got := dedupePreservingOrder([]string{"a", "b", "a", "c", "b"}); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("dedupePreservingOrder() = %#v", got)
	}
	if got := rawCommandsPreservingSyntax(
		[]string{"", "go test", "go test"},
		[]CommandResult{{Command: "go test"}, {Command: "go test 2>&1"}},
	); !reflect.DeepEqual(got, []string{"go test", "go test 2>&1"}) {
		t.Fatalf("rawCommandsPreservingSyntax() = %#v", got)
	}

	ctx := &evalContext{repoRoot: "/repo", rawCommands: []string{"raw"}, currentCommands: []string{"current"}}
	if got := commandsForShellAnalysis(nil, []string{"fallback"}); !reflect.DeepEqual(got, []string{"fallback"}) {
		t.Fatalf("nil context commands = %#v", got)
	}
	if got := commandsForShellAnalysis(ctx, nil); !reflect.DeepEqual(got, []string{"raw"}) {
		t.Fatalf("raw context commands = %#v", got)
	}
	ctx.preCommand = true
	if got := commandsForShellAnalysis(ctx, nil); !reflect.DeepEqual(got, []string{"current"}) {
		t.Fatalf("pre-command context commands = %#v", got)
	}
	if ctxRepoRoot(nil) != "" || ctxRepoRoot(ctx) != "/repo" {
		t.Fatalf("ctxRepoRoot(nil/context) = %q/%q", ctxRepoRoot(nil), ctxRepoRoot(ctx))
	}
}

func TestNormalizeWriteEpochsPreservesMaximumAliasEpoch(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "src", "main.go")
	paths := []string{absolute, "src/main.go", "src/other.go"}
	epochs, err := normalizeWriteEpochs(paths, map[string]uint64{
		absolute:       4,
		"src/main.go":  9,
		"src/other.go": 3,
	}, root)
	if err != nil {
		t.Fatal(err)
	}
	if epochs["src/main.go"] != 9 || epochs["src/other.go"] != 3 {
		t.Fatalf("normalized epochs = %#v", epochs)
	}
}

func TestCommandEvidenceIndexPreservesOrderAndFreshness(t *testing.T) {
	inputs := ExecutionInputs{
		Commands: []string{"rtk go  test ./...", "go test ./...", "echo ready"},
		CommandResults: []CommandResult{
			{Command: "cd /repo && go test ./... > result.log", Outcome: CommandOutcomeSuccess, EvidenceEpoch: 9},
			{Command: "go test ./...", Outcome: CommandOutcomeFailure, EvidenceEpoch: 10},
		},
	}
	index := newCommandEvidenceIndex(inputs, "/repo")
	if len(index.commands) != len(inputs.Commands) || len(index.results) != len(inputs.CommandResults) {
		t.Fatalf("evidence cardinality changed: %#v", index)
	}
	if got := index.commands[0].normalized; got != "go test ./..." {
		t.Fatalf("first normalized command = %q", got)
	}
	if index.results[0].raw != inputs.CommandResults[0].Command || index.results[0].epoch != 9 {
		t.Fatalf("result provenance lost: %#v", index.results[0])
	}
	cache := newCommandInvocationCache([]policy.Rule{{Commands: []string{"go  test ./..."}}}, "/repo")
	if got := matchingCommandsWithEvidence(index, cache, inputs.Commands, []string{"go  test ./..."}, "/repo", policy.CommandMatchExact); !reflect.DeepEqual(got, []string{"rtk go  test ./...", "go test ./..."}) {
		t.Fatalf("command evidence matching = %#v", got)
	}
	if got := matchingCommandResultsSinceWithEvidence(index, cache, inputs.CommandResults, []string{"go test ./..."}, CommandOutcomeSuccess, "/repo", 9, policy.CommandMatchExact); !reflect.DeepEqual(got, []string{"cd /repo && go test ./... > result.log"}) {
		t.Fatalf("fresh result matching = %#v", got)
	}
}

func TestCommandResultAndRedirectHelpersEnforceOutcomeEpochAndSyntax(t *testing.T) {
	results := []CommandResult{
		{Command: "go test ./...", Outcome: CommandOutcomeFailure, EvidenceEpoch: 9},
		{Command: "go test ./...", Outcome: CommandOutcomeSuccess, EvidenceEpoch: 2},
		{Command: "go test ./... > result.log", Outcome: CommandOutcomeSuccess, EvidenceEpoch: 9},
		{Command: "go test ./... | tail", Outcome: CommandOutcomeSuccess, EvidenceEpoch: 9},
	}
	got := matchingCommandResultsSince(results, []string{"go test ./..."}, CommandOutcomeSuccess, "", 5, policy.CommandMatchExact)
	if !reflect.DeepEqual(got, []string{"go test ./... > result.log"}) {
		t.Fatalf("matchingCommandResultsSince() = %#v", got)
	}
	if got := matchingCommandResultsSince(results, nil, CommandOutcomeSuccess, "", 0, policy.CommandMatchExact); got != nil {
		t.Fatalf("empty expected commands = %#v", got)
	}
	if latestWriteEpoch([]string{"a", "b", "c"}, map[string]uint64{"a": 2, "b": 9, "c": 4}) != 9 {
		t.Fatal("latestWriteEpoch did not return maximum")
	}

	for token, want := range map[string]bool{">file": true, "2>&1": true, "&>log": true, "a>b": false, "plain": false} {
		if got := isRedirectStart(token); got != want {
			t.Fatalf("isRedirectStart(%q) = %t, want %t", token, got, want)
		}
	}
	for token, want := range map[string]bool{">": true, "2>&1": true, "&>": true, "file": false, ">x": false} {
		if got := isRedirectOperatorOnly(token); got != want {
			t.Fatalf("isRedirectOperatorOnly(%q) = %t, want %t", token, got, want)
		}
	}
	for token, want := range map[string]bool{"file": true, "path/name": true, "": false, "a|b": false, "a>b": false} {
		if got := isPlainRedirectTarget(token); got != want {
			t.Fatalf("isPlainRedirectTarget(%q) = %t, want %t", token, got, want)
		}
	}
}

func TestScalarAndClaimHelpersCoverFallbacksAndWhitespace(t *testing.T) {
	for _, test := range []struct {
		value interface{}
		def   int64
		want  int64
	}{
		{value: nil, def: 8, want: 8},
		{value: json.Number("12"), def: 8, want: 12},
		{value: json.Number("bad"), def: 8, want: 8},
		{value: 4, def: 8, want: 4},
		{value: float64(5), def: 8, want: 5},
		{value: 5.5, def: 8, want: 8},
		{value: "6", def: 8, want: 8},
	} {
		if got := numAsIntDefault(test.value, test.def); got != test.want {
			t.Fatalf("numAsIntDefault(%#v, %d) = %d, want %d", test.value, test.def, got, test.want)
		}
	}
	if quote("value") != `"value"` {
		t.Fatalf("quote() = %q", quote("value"))
	}
	if got := matchingClaims([]string{" ci   green ", "other"}, []string{"ci green"}); !reflect.DeepEqual(got, []string{" ci   green "}) {
		t.Fatalf("matchingClaims() = %#v", got)
	}
	if got := matchingClaims([]string{"anything"}, nil); got != nil {
		t.Fatalf("matchingClaims(empty expected) = %#v", got)
	}
}
