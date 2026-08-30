package agentsession

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestActiveClaimsAndDescriptionContracts(t *testing.T) {
	t.Setenv(StateRootEnv, filepath.Join(t.TempDir(), "state"))
	repo := t.TempDir()
	if _, err := InitializeSessionState(repo, "session-1"); err != nil {
		t.Fatalf("InitializeSessionState: %v", err)
	}
	if _, err := MutateSessionState(repo, "session-1", func(state SessionState) SessionState {
		return AppendClaim(state, "ci-green")
	}); err != nil {
		t.Fatalf("MutateSessionState: %v", err)
	}
	claims, err := ActiveClaims(repo)
	if err != nil {
		t.Fatalf("ActiveClaims: %v", err)
	}
	if !reflect.DeepEqual(claims, []string{"ci-green"}) {
		t.Fatalf("claims = %v", claims)
	}

	description := DescribeClaimReport(&ClaimReport{
		SessionID: "session-1", Claim: "ci-green", ClaimCount: 1,
		StatePath: "/state.json", ReportPath: "/report.json",
	})
	for _, expected := range []string{"ci-green", "session-1", "total claims: 1", "/state.json", "/report.json"} {
		if !strings.Contains(description, expected) {
			t.Fatalf("claim description omitted %q: %s", expected, description)
		}
	}
}

func TestRepositoryRunPathAndRecoveryErrorContracts(t *testing.T) {
	repo := t.TempDir()
	path, err := RunDecisionLogPath(repo)
	if err != nil {
		t.Fatalf("RunDecisionLogPath: %v", err)
	}
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatalf("ResolveRepoRoot: %v", err)
	}
	if path != filepath.Join(root, ".reconc", "run", "decisions.jsonl") {
		t.Fatalf("decision log path = %q", path)
	}

	cause := errors.New("corrupt state")
	wrapped := wrapRepositoryRunRecovery(repo, cause)
	if !errors.Is(wrapped, cause) || !strings.Contains(wrapped.Error(), "reconc run reset") {
		t.Fatalf("recovery error lost cause or remediation: %v", wrapped)
	}
	if got := wrapRepositoryRunRecovery(repo, wrapped); got != wrapped {
		t.Fatal("recovery error was wrapped twice")
	}
	if got := wrapRepositoryRunRecovery(repo, nil); got != nil {
		t.Fatalf("nil recovery error became %v", got)
	}
}

func TestStrictIntegerAcceptsOnlyExactIntegers(t *testing.T) {
	for _, test := range []struct {
		name  string
		value interface{}
		want  int
		valid bool
	}{
		{name: "json integer", value: json.Number("42"), want: 42, valid: true},
		{name: "json fraction", value: json.Number("1.5"), valid: false},
		{name: "json invalid", value: json.Number("no"), valid: false},
		{name: "float integer", value: float64(-7), want: -7, valid: true},
		{name: "float fraction", value: 1.25, valid: false},
		{name: "wrong type", value: "42", valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, valid := strictInteger(test.value)
			if got != test.want || valid != test.valid {
				t.Fatalf("strictInteger(%v) = (%d, %v), want (%d, %v)", test.value, got, valid, test.want, test.valid)
			}
		})
	}
}

func TestDiagnosticAndOutputHelpers(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "rule blocked: first failure\nsecond failure", want: "first failure"},
		{input: "script error: concise; more detail", want: "concise"},
		{input: "plain diagnostic", want: "plain diagnostic"},
		{input: " \n ", want: ""},
		{input: strings.Repeat("x", 221), want: strings.Repeat("x", 220) + "..."},
	} {
		if got := firstDiagnosticLine(test.input); got != test.want {
			t.Fatalf("firstDiagnosticLine(%q) = %q, want %q", test.input, got, test.want)
		}
	}

	for _, test := range []struct {
		left, right string
		want        string
	}{
		{right: "extra", want: "extra"},
		{left: "existing", want: "existing"},
		{left: "existing", right: "extra", want: "existing\nextra"},
	} {
		if got := joinStderr(test.left, test.right); got != test.want {
			t.Fatalf("joinStderr(%q, %q) = %q", test.left, test.right, got)
		}
	}
}

func TestTruncateUTF8PreservesValidEncodingAndBudgetContract(t *testing.T) {
	if got := truncateUTF8("short", 10); got != "short" {
		t.Fatalf("short value changed: %q", got)
	}
	if got := truncateUTF8("unchanged", 0); got != "unchanged" {
		t.Fatalf("disabled truncation changed value: %q", got)
	}
	got := truncateUTF8("äöü-"+strings.Repeat("context", 10), 30)
	if !strings.HasSuffix(got, "\n[reconc context truncated]") {
		t.Fatalf("missing truncation suffix: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
	if got := truncateUTF8("abcdef", 3); got != "\n[reconc context truncated]" {
		t.Fatalf("budget smaller than suffix = %q", got)
	}
}

func TestCursorPathInputPromotesTopLevelPath(t *testing.T) {
	raw := map[string]interface{}{
		"tool_input": map[string]interface{}{"content": "body"},
		"file_path":  "src/main.go",
	}
	got := cursorPathInput(raw)
	if got["file_path"] != "src/main.go" || got["content"] != "body" {
		t.Fatalf("cursorPathInput = %#v", got)
	}

	raw = map[string]interface{}{
		"tool_input": map[string]interface{}{"path": "existing.go"},
		"file_path":  "ignored.go",
	}
	got = cursorPathInput(raw)
	if got["path"] != "existing.go" {
		t.Fatalf("existing nested path was replaced: %#v", got)
	}
}

func TestAdapterHelpersPreserveCloneAndReasonContracts(t *testing.T) {
	nested := map[string]interface{}{"value": "before"}
	original := map[string]interface{}{"top": "original", "nested": nested}
	cloned := cloneObject(original)
	cloned["top"] = "changed"
	cloned["nested"].(map[string]interface{})["value"] = "after"
	if original["top"] != "original" || nested["value"] != "after" {
		t.Fatalf("clone aliasing contract changed: original=%#v cloned=%#v", original, cloned)
	}
	if empty := cloneObject(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil clone = %#v, want non-nil empty map", empty)
	}

	for _, test := range []struct {
		name     string
		result   Result
		fallback string
		want     string
	}{
		{name: "stderr", result: Result{Stderr: " stderr ", Stdout: "stdout"}, fallback: "fallback", want: "stderr"},
		{name: "stdout", result: Result{Stdout: " stdout "}, fallback: "fallback", want: "stdout"},
		{name: "fallback", fallback: " exact fallback ", want: "exact fallback"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := resultReason(test.result, test.fallback); got != test.want {
				t.Fatalf("resultReason() = %q, want %q", got, test.want)
			}
		})
	}
}
