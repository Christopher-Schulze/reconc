package action

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
)

func TestCompiledGlobMatchesDoublestarContract(t *testing.T) {
	t.Parallel()
	patterns := []string{
		"", "*", "/*", "a*b?c*x", "ab[b-d]", "ab[^e-g]", `[\\]a]`, `[x\\-]`,
		"[-]", "[x-]", "[-x]", "[a-b-d]", "[a-ζ]*", "**", "a/**", "a/**/",
		"**/c", "a/**/b", "a/**/d", `a/\\**`, "ab{c,d,*}", "a{,bc}",
		"a/{b/c,c/b}", "a/a*{b,c}", "{a/{b,c},abc}", "{a,b}{c,d}",
	}
	names := []string{
		"", "/", "//", "a", "a/", "a//", "abc", "abcd", "abcde", "a/b", "a/b/",
		"a/b/c", "a/b/c/d", "b/c", "c", "axbxcxdxe", "abxbbxdbxebxczzx", "]", "-", "α",
	}
	for _, pattern := range patterns {
		pattern := pattern
		t.Run(pattern, func(t *testing.T) {
			t.Parallel()
			compiled, err := compileGlob(pattern)
			if err != nil {
				t.Fatalf("compile %q: %v", pattern, err)
			}
			for _, name := range names {
				want := doublestar.MatchUnvalidated(pattern, name)
				got, complete := compiled.Match(name)
				if !complete || got != want {
					t.Errorf("pattern %q name %q = %t complete %t, want %t", pattern, name, got, complete, want)
				}
			}
		})
	}
}

func TestCompiledGlobGeneratedParity(t *testing.T) {
	t.Parallel()
	tokens := []string{"a", "b", "/", "*", "?", "**", "[ab]", "[^a]", "{a,b}", "{,a}", `\\*`}
	names := []string{"", "a", "b", "/", "a/", "/a", "aa", "ab", "a/b", "a//b", "x", "*"}
	patterns := []string{""}
	for depth := 0; depth < 3; depth++ {
		previous := append([]string(nil), patterns...)
		for _, prefix := range previous {
			for _, token := range tokens {
				patterns = append(patterns, prefix+token)
			}
		}
	}
	for _, pattern := range patterns {
		if !doublestar.ValidatePattern(pattern) {
			continue
		}
		compiled, err := compileGlob(pattern)
		if err != nil {
			t.Fatalf("compile %q: %v", pattern, err)
		}
		for _, name := range names {
			got, complete := compiled.Match(name)
			if want := doublestar.MatchUnvalidated(pattern, name); !complete || got != want {
				t.Fatalf("pattern %q name %q = %t complete %t, want %t", pattern, name, got, complete, want)
			}
		}
	}
}

func TestCompiledGlobWorkLimitIsDerivedAndCapped(t *testing.T) {
	t.Parallel()
	simple, err := compileGlob("**/missing")
	if err != nil {
		t.Fatal(err)
	}
	want := uint64(4 * (64 + len(simple.programs[0].tokens) + 1))
	if got := simple.matchWorkLimit(64); got != want {
		t.Fatalf("simple work limit = %d, want %d", got, want)
	}
	expanded, err := compileGlob("**/" + strings.Repeat("{a,b}", 10))
	if err != nil {
		t.Fatal(err)
	}
	if got := expanded.matchWorkLimit(MaxJSONStringBytes); got != maxGlobMatchWork {
		t.Fatalf("expanded work limit = %d, want cap %d", got, maxGlobMatchWork)
	}
}

func TestCompiledGlobReportsWorkExhaustion(t *testing.T) {
	t.Parallel()
	compiled, err := compileGlob("**/missing")
	if err != nil {
		t.Fatal(err)
	}
	if matched, complete := compiled.matchWithLimit("a/b/c/present", 1); matched || complete {
		t.Fatalf("limited match = %t complete %t, want fail-closed exhaustion", matched, complete)
	}
	if matched, complete := compiled.Match("a/b/c/present"); matched || !complete {
		t.Fatal("ordinary public match changed the non-match result")
	}
}

func TestCompiledGlobCapsBraceAlternativeScalingBeforeLateMatch(t *testing.T) {
	t.Parallel()
	pattern := "**/" + strings.Repeat("{a,b}", 10)
	compiled, err := compileGlob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("x/", (64<<10)/2) + strings.Repeat("b", 10)
	if !doublestar.MatchUnvalidated(pattern, value) {
		t.Fatal("adversarial fixture must match a late brace alternative")
	}
	if matched, complete := compiled.Match(value); matched || complete {
		t.Fatalf("bounded late match = %t complete %t, want fail-closed exhaustion", matched, complete)
	}
}

func TestCompiledGlobRejectsExpansionAbovePlanAdmission(t *testing.T) {
	t.Parallel()
	pattern := strings.Repeat("{"+strings.Repeat("a", 100)+","+strings.Repeat("b", 100)+"}", 15)
	if len(pattern) > MaxPatternBytes {
		t.Fatal("test pattern exceeds source boundary")
	}
	if _, err := compileGlob(pattern); err == nil || !strings.Contains(err.Error(), "program") {
		t.Fatalf("error = %v, want program-limit rejection", err)
	}
}

func TestGlobExpansionBudgetsPreserveMaximumLegalPrograms(t *testing.T) {
	t.Parallel()
	maximum := strings.Repeat("{a,b}", 10)
	first, err := expandGlobAlternatives(maximum)
	if err != nil {
		t.Fatal(err)
	}
	second, err := expandGlobAlternatives(maximum)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != maxGlobPrograms || len(second) != maxGlobPrograms {
		t.Fatalf("maximum expansion counts = %d and %d, want %d", len(first), len(second), maxGlobPrograms)
	}
	for index := range first {
		if first[index].key != second[index].key {
			t.Fatalf("maximum expansion key %d changed", index)
		}
	}

	if _, err := expandGlobAlternatives(strings.Repeat("{a,b}", 11)); err == nil || !strings.Contains(err.Error(), "program") {
		t.Fatalf("over-program error = %v", err)
	}
	stateAmplification := strings.Repeat("{a,b}", 10) + strings.Repeat("{a}", 20)
	if _, err := expandGlobAlternatives(stateAmplification); err == nil || !strings.Contains(err.Error(), "state expansion") {
		t.Fatalf("over-state error = %v", err)
	}
}

func TestCountGlobExpansionProgramsHandlesNestedGroups(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern string
		want    int
	}{
		{pattern: "plain", want: 1},
		{pattern: "{a,b}{c,d}", want: 4},
		{pattern: "{a/{b,c},abc}", want: 3},
		{pattern: "{a,{b,c}}{d,e}", want: 6},
		{pattern: `[{}]{a,b}`, want: 2},
		{pattern: `\{a,b\}`, want: 1},
	}
	for _, test := range tests {
		got, withinLimit := countGlobExpansionPrograms(test.pattern, maxGlobPrograms)
		if !withinLimit || got != test.want {
			t.Fatalf("pattern %q count = %d within limit %t, want %d", test.pattern, got, withinLimit, test.want)
		}
	}
}

func TestGlobExpansionIdentityIsExactAndStable(t *testing.T) {
	t.Parallel()
	pattern := "a/**/{b,c}/{d,e}"
	first, err := expandGlobAlternatives(pattern)
	if err != nil {
		t.Fatal(err)
	}
	second, err := expandGlobAlternatives(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("expansion count = %d and %d", len(first), len(second))
	}
	seen := make(map[string]struct{}, len(first))
	for index := range first {
		if first[index].key == "" || first[index].key != buildGlobExpansionKey(first[index]) {
			t.Fatalf("expansion %d key is not the exact cached identity", index)
		}
		if first[index].key != second[index].key {
			t.Fatalf("expansion %d key changed across equivalent runs", index)
		}
		if _, duplicate := seen[first[index].key]; duplicate {
			t.Fatalf("duplicate expansion identity at index %d", index)
		}
		seen[first[index].key] = struct{}{}
	}
}

func FuzzCompiledGlobParity(f *testing.F) {
	for _, seed := range [][2]string{
		{"a/**/b", "a/x/y/b"}, {"{a,b}/*.go", "a/main.go"}, {"[a-z]?", "a1"},
		{"/**", ""}, {"a/**/", "a"}, {`a/\\*`, "a/*"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, pattern string, name string) {
		if !doublestar.ValidatePattern(pattern) || !utf8.ValidString(pattern) {
			return
		}
		compiled, err := compileGlob(pattern)
		if len(pattern) > MaxPatternBytes {
			if err == nil {
				t.Fatal("over-limit pattern compiled")
			}
			return
		}
		if err != nil {
			return
		}
		got, complete := compiled.Match(name)
		if !complete {
			if got {
				t.Fatal("work-exhausted glob reported a match")
			}
			return
		}
		if want := doublestar.MatchUnvalidated(pattern, name); got != want {
			t.Fatalf("pattern %q name %q = %t complete %t, want %t", pattern, name, got, complete, want)
		}
	})
}
