package runtime

import (
	"strings"
	"testing"
)

func TestHasTemplateVars(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{
		{"docs/todo/{task_id}.md", true},
		{"src/{module}/main.go", true},
		{"src/main.go", false},
		{"docs/**", false},
		{"{a}/{b}", true},
		{"literal", false},
	}
	for _, c := range cases {
		got := HasTemplateVars(c.pattern)
		if got != c.want {
			t.Errorf("HasTemplateVars(%q) = %v, want %v", c.pattern, got, c.want)
		}
	}
}

func TestPatternHasAnyTemplateVar(t *testing.T) {
	if !PatternHasAnyTemplateVar([]string{"a", "b/{c}"}) {
		t.Error("expected true when any pattern has a var")
	}
	if PatternHasAnyTemplateVar([]string{"a", "b/c"}) {
		t.Error("expected false when no pattern has vars")
	}
}

func TestMatchTemplateNoVarsFallsBackToGlob(t *testing.T) {
	caps, ok, err := MatchTemplate("src/**", "src/main.go")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Error("expected match")
	}
	if len(caps) != 0 {
		t.Errorf("expected empty captures, got %v", caps)
	}
}

func TestMatchTemplateSingleVar(t *testing.T) {
	caps, ok, err := MatchTemplate("docs/todo/{task_id}.md", "docs/todo/TODO-001.md")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if caps["task_id"] != "TODO-001" {
		t.Errorf("expected task_id=TODO-001, got %s", caps["task_id"])
	}
}

func TestMatchTemplateMultipleVars(t *testing.T) {
	caps, ok, err := MatchTemplate("docs/{category}/{task_id}.md", "docs/todo/TODO-001.md")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if caps["category"] != "todo" {
		t.Errorf("category wrong: %s", caps["category"])
	}
	if caps["task_id"] != "TODO-001" {
		t.Errorf("task_id wrong: %s", caps["task_id"])
	}
}

func TestMatchTemplateRejectsCrossSlash(t *testing.T) {
	// {var} is single-segment; should NOT match across slashes.
	_, ok, err := MatchTemplate("docs/{task_id}.md", "docs/sub/TODO-001.md")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("{var} should not cross slashes")
	}
}

func TestMatchTemplateMixedWithGlobstar(t *testing.T) {
	caps, ok, err := MatchTemplate("src/{module}/**", "src/auth/handler/login.go")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if caps["module"] != "auth" {
		t.Errorf("module wrong: %s", caps["module"])
	}
}

func TestMatchTemplateNoMatch(t *testing.T) {
	caps, ok, err := MatchTemplate("docs/todo/{task_id}.md", "src/main.go")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("expected no match")
	}
	if caps != nil {
		t.Errorf("expected nil captures on miss, got %v", caps)
	}
}

func TestMatchTemplateRejectsDuplicateVarName(t *testing.T) {
	_, _, err := MatchTemplate("{x}/{x}", "a/b")
	if err == nil {
		t.Error("expected error for duplicate var name")
	}
}

func TestMatchTemplateAnyReturnsFirstHit(t *testing.T) {
	pat, caps, ok, err := MatchTemplateAny(
		[]string{"src/**", "docs/{task_id}.md"},
		"docs/TODO-001.md",
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if pat != "docs/{task_id}.md" {
		t.Errorf("expected matched pattern, got %s", pat)
	}
	if caps["task_id"] != "TODO-001" {
		t.Errorf("captures wrong: %v", caps)
	}
}

func TestSubstituteTemplateBasic(t *testing.T) {
	got, err := SubstituteTemplate("docs/fidelity/{task_id}.json", map[string]string{"task_id": "TODO-001"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "docs/fidelity/TODO-001.json" {
		t.Errorf("got %s", got)
	}
}

func TestSubstituteTemplateMultipleVars(t *testing.T) {
	got, err := SubstituteTemplate("a/{x}/b/{y}", map[string]string{"x": "1", "y": "2"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "a/1/b/2" {
		t.Errorf("got %s", got)
	}
}

func TestSubstituteTemplateNoVarsNoOp(t *testing.T) {
	got, err := SubstituteTemplate("docs/static.md", map[string]string{"x": "1"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "docs/static.md" {
		t.Errorf("got %s", got)
	}
}

func TestSubstituteTemplateMissingVarReturnsError(t *testing.T) {
	got, err := SubstituteTemplate("docs/{missing}.md", map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing var")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected 'missing' in error, got: %v", err)
	}
	// The placeholder should remain in the partial output.
	if got != "docs/{missing}.md" {
		t.Errorf("placeholder should remain on miss, got: %s", got)
	}
}

func TestSubstituteTemplateInList(t *testing.T) {
	got, err := SubstituteTemplateInList(
		[]string{"a/{x}", "b/{x}.txt", "c"},
		map[string]string{"x": "1"},
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0] != "a/1" || got[1] != "b/1.txt" || got[2] != "c" {
		t.Errorf("got %v", got)
	}
}

func TestSubstituteTemplateInListPreservesOriginal(t *testing.T) {
	original := []string{"{x}"}
	_, _ = SubstituteTemplateInList(original, map[string]string{"x": "y"})
	if original[0] != "{x}" {
		t.Error("original slice should not be mutated")
	}
}
