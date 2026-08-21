package runtime

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/templates"
)

func legacyMatchTemplateForTest(pattern, path string) (map[string]string, bool, error) {
	if !HasTemplateVars(pattern) {
		ok, err := MatchPath(pattern, path)
		if err != nil {
			return nil, false, err
		}
		return map[string]string{}, ok, nil
	}
	path = filepath.ToSlash(path)
	masked, err := templates.MaskVariables(strings.TrimSpace(pattern), "*")
	if err != nil {
		return nil, false, err
	}
	ok, err := MatchPath(masked, path)
	if err != nil || !ok {
		return nil, ok, err
	}
	regex, names, err := compileTemplatePattern(pattern)
	if err != nil {
		return nil, false, err
	}
	match := regex.FindStringSubmatch(path)
	if match == nil {
		return nil, false, fmt.Errorf("template matcher diverged from validated glob semantics for pattern %q", pattern)
	}
	captures := make(map[string]string, len(names))
	for index, name := range names {
		captures[name] = match[index+1]
	}
	trimmed := strings.TrimSpace(pattern)
	variables, err := templates.Variables(trimmed)
	if err != nil {
		return nil, false, err
	}
	var boundBuilder strings.Builder
	last := 0
	for _, variable := range variables {
		boundBuilder.WriteString(trimmed[last:variable.Start])
		boundBuilder.WriteString(escapeGlobLiteral(captures[variable.Name]))
		last = variable.End
	}
	boundBuilder.WriteString(trimmed[last:])
	bound := boundBuilder.String()
	boundOK, err := MatchPath(bound, path)
	if err != nil {
		return nil, false, err
	}
	if !boundOK {
		return nil, false, fmt.Errorf("template captures diverged from validated glob semantics for pattern %q", pattern)
	}
	return captures, true, nil
}

func TestCompiledTemplateMatcherMatchesLegacy(t *testing.T) {
	cases := [][2]string{
		{"src/**", "src/main.go"},
		{"src/**", "docs/main.go"},
		{"docs/todo/{task_id}.md", "docs/todo/TODO-001.md"},
		{"docs/{category}/{task_id}.md", "docs/todo/TODO-001.md"},
		{"src/{module}/**/file.go", "src/auth/http/file.go"},
		{"**/{module}.go", "auth.go"},
		{"{src,pkg}/{module}/main.go", "pkg/auth/main.go"},
		{"tmp/{module}.txt", "tmp/*.txt"},
		{"{x}/{x}", "a/b"},
		{"src/{module}/file[.go", "src/auth/filex.go"},
	}
	for _, testCase := range cases {
		compiled := compileTemplateMatcher(testCase[0])
		gotCaptures, gotOK, gotErr := compiled.match(testCase[1])
		wantCaptures, wantOK, wantErr := legacyMatchTemplateForTest(testCase[0], testCase[1])
		if gotOK != wantOK || !reflect.DeepEqual(gotCaptures, wantCaptures) || errorText(gotErr) != errorText(wantErr) {
			t.Errorf("compiled %q/%q = (%v, %v, %v), legacy = (%v, %v, %v)", testCase[0], testCase[1], gotCaptures, gotOK, gotErr, wantCaptures, wantOK, wantErr)
		}
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestCompileRuntimeTemplateMatchersSelectsContextRules(t *testing.T) {
	rules := []policy.Rule{
		{Kind: policy.KindRequireScript, WhenPaths: []string{"scripts/{name}.sh"}},
		{Kind: policy.KindRequireCommand, WhenPaths: []string{"commands/{name}"}},
		{Kind: policy.KindAllOf, WhenPaths: []string{"src/{module}/**"}},
	}
	matchers, err := compileRuntimeTemplateMatchers(rules)
	if err != nil {
		t.Fatalf("compileRuntimeTemplateMatchers: %v", err)
	}
	if _, ok := matchers.byPattern["scripts/{name}.sh"]; !ok {
		t.Fatal("require_script template was not compiled")
	}
	if _, ok := matchers.byPattern["src/{module}/**"]; !ok {
		t.Fatal("composite template was not compiled")
	}
	if _, ok := matchers.byPattern["commands/{name}"]; ok {
		t.Fatal("non-context command template was compiled as a capture matcher")
	}
}

func BenchmarkCompiledTemplateMatcher(b *testing.B) {
	matcher := compileTemplateMatcher("src/{module}/**/file[0-9].go")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := matcher.match("src/runtime/internal/file7.go"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDynamicTemplateMatcher(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		if _, _, err := MatchTemplate("src/{module}/**/file[0-9].go", "src/runtime/internal/file7.go"); err != nil {
			b.Fatal(err)
		}
	}
}

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

func TestMatchTemplatePreservesDoublestarSemantics(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		capture string
	}{
		{name: "zero directories", pattern: "src/{module}/**/file.go", path: "src/auth/file.go", capture: "auth"},
		{name: "nested directories", pattern: "src/{module}/**/file.go", path: "src/auth/http/api/file.go", capture: "auth"},
		{name: "leading globstar consumes zero", pattern: "**/{module}.go", path: "auth.go", capture: "auth"},
		{name: "leading globstar consumes directories", pattern: "**/{module}.go", path: "src/internal/auth.go", capture: "auth"},
		{name: "terminal globstar consumes zero", pattern: "src/{module}/**", path: "src/auth", capture: "auth"},
		{name: "character class", pattern: "src/{module}/file[0-9].go", path: "src/auth/file7.go", capture: "auth"},
		{name: "alternative", pattern: "{src,pkg}/{module}/main.go", path: "pkg/auth/main.go", capture: "auth"},
		{name: "captured glob character stays literal", pattern: "tmp/{module}.txt", path: "tmp/*.txt", capture: "*"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			captures, ok, err := MatchTemplate(test.pattern, test.path)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("expected match")
			}
			if captures["module"] != test.capture {
				t.Fatalf("module capture = %q, want %q", captures["module"], test.capture)
			}
		})
	}
}

func TestMatchTemplateRejectsMalformedGlob(t *testing.T) {
	if _, _, err := MatchTemplate("src/{module}/file[.go", "src/auth/filex.go"); err == nil {
		t.Fatal("malformed glob must fail closed")
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
