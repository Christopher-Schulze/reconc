package runtime

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"

	"github.com/bmatcuk/doublestar/v4"
)

func TestCompiledPathMatcherMatchesDoublestar(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
	}{
		{"", ""},
		{"src/*.go", "src/main.go"},
		{"src/*.go", "src/internal/main.go"},
		{"src/**/main.go", "src/main.go"},
		{"src/**/main.go", "src/internal/main.go"},
		{"**/{go,rs}", "src/main.go"},
		{"file[!0-9].txt", "filea.txt"},
		{"file[!0-9].txt", "file7.txt"},
		{`literal\\*.txt`, `literal\*.txt`},
		{"  docs/**  ", "docs/readme.md"},
	}
	for _, testCase := range cases {
		matcher, err := CompilePathMatcher(testCase.pattern)
		if err != nil {
			t.Fatalf("CompilePathMatcher(%q): %v", testCase.pattern, err)
		}
		want, err := MatchPath(testCase.pattern, testCase.path)
		if err != nil {
			t.Fatalf("MatchPath(%q, %q): %v", testCase.pattern, testCase.path, err)
		}
		if got := matcher.Match(testCase.path); got != want {
			t.Errorf("compiled %q against %q = %v, want %v", testCase.pattern, testCase.path, got, want)
		}
		baseline, err := doublestar.Match(strings.TrimSpace(testCase.pattern), testCase.path)
		if err != nil || baseline != want {
			t.Fatalf("doublestar baseline for %q/%q = %v, %v; runtime = %v", testCase.pattern, testCase.path, baseline, err, want)
		}
	}
}

func TestCompilePathMatcherRejectsInvalidPattern(t *testing.T) {
	for _, pattern := range []string{"[", `trailing\`} {
		if _, err := CompilePathMatcher(pattern); err == nil {
			t.Errorf("CompilePathMatcher(%q) accepted malformed pattern", pattern)
		}
	}
}

func TestCompileRuntimePathMatchersHonorsAggregateBound(t *testing.T) {
	pattern := strings.Repeat("a", maxRuntimeCompiledPathMatcherBytes+1)
	compiled, err := compileRuntimePathMatchers([]policy.Rule{{Paths: []string{pattern}}})
	if err != nil {
		t.Fatalf("compileRuntimePathMatchers: %v", err)
	}
	if compiled.byPattern[pattern].glob != nil {
		t.Fatal("runtime matcher exceeded its explicit aggregate bound")
	}
}

func TestCompileRuntimePathMatchersIncludesNestedRulePatterns(t *testing.T) {
	rules := []policy.Rule{{
		Paths:       []string{"src/**"},
		BeforePaths: []string{"docs/**"},
		WhenPaths:   []string{"**/*.go"},
		ScopePaths:  []string{"pkg/**"},
		Checks: []policy.Check{{
			Paths:       []string{"generated/**"},
			BeforePaths: []string{"tests/**"},
			WhenPaths:   []string{"fixtures/**"},
		}},
	}}
	matchers, err := compileRuntimePathMatchers(rules)
	if err != nil {
		t.Fatalf("compileRuntimePathMatchers: %v", err)
	}
	for _, pattern := range []string{"src/**", "docs/**", "**/*.go", "pkg/**", "generated/**", "tests/**", "fixtures/**"} {
		matcher, ok := matchers.byPattern[pattern]
		if !ok {
			t.Errorf("matcher map omitted %q", pattern)
			continue
		}
		if matcher.Pattern() != pattern {
			t.Errorf("matcher %q retained pattern %q", pattern, matcher.Pattern())
		}
	}
}

func FuzzCompiledPathMatcherParity(f *testing.F) {
	for _, seed := range [][2]string{
		{"src/**", "src/main.go"},
		{"**/{go,rs}", "internal/file.rs"},
		{"[abc]", "b"},
		{`literal\\*`, `literal\*`},
		{"[", "x"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, pattern, path string) {
		matcher, err := CompilePathMatcher(pattern)
		want, matchErr := MatchPath(pattern, path)
		if err != nil || matchErr != nil {
			if err == nil || matchErr == nil {
				t.Fatalf("compile/match validation disagreement for %q: compile=%v match=%v", pattern, err, matchErr)
			}
			return
		}
		if got := matcher.Match(path); got != want {
			t.Fatalf("compiled matcher mismatch for %q/%q: got %v, want %v", pattern, path, got, want)
		}
	})
}

func BenchmarkCompiledPathMatcherRules(b *testing.B) {
	patterns := []string{"src/**", "internal/**", "pkg/**", "docs/**", "**/*.go", "**/*.rs", "generated/**", "vendor/**"}
	paths := []string{"src/main.go", "internal/runtime/evaluator.go", "pkg/client/client.go", "docs/spec.md", "generated/lock.json", "vendor/lib/a.go", "README.md", "src/sub/file.rs"}
	matchers := make([]CompiledPathMatcher, len(patterns))
	for index, pattern := range patterns {
		matcher, err := CompilePathMatcher(pattern)
		if err != nil {
			b.Fatal(err)
		}
		matchers[index] = matcher
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, path := range paths {
			for _, matcher := range matchers {
				_ = matcher.Match(path)
			}
		}
	}
}

func BenchmarkDynamicPathMatcherRules(b *testing.B) {
	patterns := []string{"src/**", "internal/**", "pkg/**", "docs/**", "**/*.go", "**/*.rs", "generated/**", "vendor/**"}
	paths := []string{"src/main.go", "internal/runtime/evaluator.go", "pkg/client/client.go", "docs/spec.md", "generated/lock.json", "vendor/lib/a.go", "README.md", "src/sub/file.rs"}
	b.ReportAllocs()
	for range b.N {
		for _, path := range paths {
			for _, pattern := range patterns {
				if _, err := MatchPath(pattern, path); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}

func TestMatchPathLiteral(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"foo.txt", "foo.txt", true},
		{"foo.txt", "bar.txt", false},
		{"src/main.go", "src/main.go", true},
		{"src/main.go", "src/other.go", false},
	}
	for _, c := range cases {
		got, err := MatchPath(c.pattern, c.path)
		if err != nil {
			t.Errorf("MatchPath(%q, %q) error: %v", c.pattern, c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("MatchPath(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchPathSingleStar(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.txt", "foo.txt", true},
		{"*.txt", "foo.go", false},
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/sub/main.go", false}, // * does NOT cross /
		{"src/*", "src/main.go", true},
		{"src/*", "src/sub/main.go", false},
	}
	for _, c := range cases {
		got, err := MatchPath(c.pattern, c.path)
		if err != nil {
			t.Errorf("MatchPath(%q, %q) error: %v", c.pattern, c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("MatchPath(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchPathDoubleStar(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"src/**", "src/main.go", true},
		{"src/**", "src/sub/main.go", true},
		{"src/**", "src/sub/sub2/main.go", true},
		{"src/**", "tests/main.go", false},
		{"**/main.go", "src/main.go", true},
		{"**/main.go", "src/sub/main.go", true},
		{"**/main.go", "main.go", true},
		{"generated/**", "generated/file.go", true},
		{"generated/**", "generated/sub/file.go", true},
		{"generated/**", "src/file.go", false},
		{"**/generated/**", "pkg/generated/file.go", true},
		{"**/*.generated.*", "src/foo.generated.go", true},
		{"**/*.generated.*", "src/foo.go", false},
	}
	for _, c := range cases {
		got, err := MatchPath(c.pattern, c.path)
		if err != nil {
			t.Errorf("MatchPath(%q, %q) error: %v", c.pattern, c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("MatchPath(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchPathQuestionMark(t *testing.T) {
	got, _ := MatchPath("file?.txt", "file1.txt")
	if !got {
		t.Errorf("? should match single char")
	}
	got, _ = MatchPath("file?.txt", "file12.txt")
	if got {
		t.Errorf("? should match exactly one char, not two")
	}
}

func TestMatchPathCharClass(t *testing.T) {
	got, _ := MatchPath("file[abc].txt", "filea.txt")
	if !got {
		t.Errorf("[abc] should match a")
	}
	got, _ = MatchPath("file[abc].txt", "filed.txt")
	if got {
		t.Errorf("[abc] should not match d")
	}
}

func TestMatchAnyHit(t *testing.T) {
	pat, ok, err := MatchAny([]string{"docs/**", "src/**"}, "src/main.go")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Error("expected match")
	}
	if pat != "src/**" {
		t.Errorf("expected pattern src/**, got %q", pat)
	}
}

func TestMatchAnyMiss(t *testing.T) {
	_, ok, err := MatchAny([]string{"docs/**", "src/**"}, "config.json")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("expected no match")
	}
}

func TestMatchAnyEmptyPatternList(t *testing.T) {
	_, ok, err := MatchAny(nil, "anything")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("empty pattern list should never match")
	}
}

func TestMatchAnyPath(t *testing.T) {
	path, pat, ok, err := MatchAnyPath(
		[]string{"src/**"},
		[]string{"docs/file.md", "src/main.go", "src/util.go"},
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if path != "src/main.go" {
		t.Errorf("expected first matching path src/main.go, got %q", path)
	}
	if pat != "src/**" {
		t.Errorf("expected pattern src/**, got %q", pat)
	}
}

func TestMatchAnyPathNoHit(t *testing.T) {
	_, _, ok, _ := MatchAnyPath(
		[]string{"src/**"},
		[]string{"docs/a.md", "config.json"},
	)
	if ok {
		t.Error("expected no match")
	}
}

func TestMatchPathTrimsPatternWhitespace(t *testing.T) {
	got, err := MatchPath("  src/main.go  ", "src/main.go")
	if err != nil || !got {
		t.Errorf("expected whitespace-trimmed match, got %v, err: %v", got, err)
	}
}

func TestMatchPathPreservesPathWhitespace(t *testing.T) {
	for _, path := range []string{" src/main.go", "src/main.go "} {
		got, err := MatchPath("src/main.go", path)
		if err != nil {
			t.Fatalf("MatchPath(%q): %v", path, err)
		}
		if got {
			t.Fatalf("path whitespace was discarded for %q", path)
		}
	}
}

func TestMatchPathExpectsPOSIXInput(t *testing.T) {
	// MatchPath assumes POSIX-style paths (forward slashes). It is
	// the caller's responsibility to normalize OS-native paths to
	// POSIX form BEFORE calling MatchPath. This test documents the
	// contract: backslashes are treated as glob escape chars and
	// will NOT match path separators.
	got, _ := MatchPath("src/**", "src/sub/main.go")
	if !got {
		t.Errorf("POSIX path should match")
	}
}
