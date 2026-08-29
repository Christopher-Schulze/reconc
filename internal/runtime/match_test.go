package runtime

import (
	"reflect"
	"sort"
	"strconv"
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
	for _, pattern := range []string{"[", "[!00", `trailing\`} {
		if _, err := CompilePathMatcher(pattern); err == nil {
			t.Errorf("CompilePathMatcher(%q) accepted malformed pattern", pattern)
		}
		if _, err := MatchPath(pattern, "0"); err == nil {
			t.Errorf("MatchPath(%q) accepted malformed pattern", pattern)
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

func TestCompileRuntimePathMatchersPrioritizesStableBenefitPerByte(t *testing.T) {
	t.Parallel()
	rules, hotPatterns := overBudgetRuntimeMatcherRules()
	first, err := compileRuntimePathMatchers(rules)
	if err != nil {
		t.Fatal(err)
	}
	reversed := append([]policy.Rule(nil), rules...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	second, err := compileRuntimePathMatchers(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := compiledRuntimeMatcherPatterns(first), compiledRuntimeMatcherPatterns(second); !reflect.DeepEqual(got, want) {
		t.Fatalf("compiled selection changed with policy order:\nfirst=%v\nsecond=%v", got, want)
	}
	compiledBytes := 0
	for pattern, matcher := range first.byPattern {
		if matcher.glob != nil {
			compiledBytes += matcher.glob.LogicalBytes()
		}
		prefix := pattern
		if index := strings.Index(pattern, "/{"); index >= 0 {
			prefix = pattern[:index]
		}
		for _, path := range []string{prefix + "/entry0/file.go", "unrelated/file.go"} {
			want, err := MatchPath(pattern, path)
			if err != nil {
				t.Fatal(err)
			}
			if got := matcher.Match(path); got != want {
				t.Fatalf("selected matcher %q against %q = %v, want %v", pattern, path, got, want)
			}
		}
	}
	if compiledBytes > maxRuntimeCompiledPathMatchers {
		t.Fatalf("compiled matcher bytes = %d, maximum %d", compiledBytes, maxRuntimeCompiledPathMatchers)
	}
	for _, pattern := range hotPatterns {
		if first.byPattern[pattern].glob == nil {
			t.Fatalf("high-benefit pattern %q fell back dynamically", pattern)
		}
	}
}

func TestRuntimeMatcherCandidatePriorityIsExactAndDeterministic(t *testing.T) {
	t.Parallel()
	candidates := []runtimeMatcherCandidate{
		{pattern: "low", references: 1, logicalBytes: 100},
		{pattern: "few", references: 1, logicalBytes: 5},
		{pattern: "z-many", references: 2, logicalBytes: 10},
		{pattern: "a-many", references: 2, logicalBytes: 10},
	}
	prioritizeRuntimeMatcherCandidates(candidates)
	want := []string{"a-many", "z-many", "few", "low"}
	got := make([]string, len(candidates))
	for index := range candidates {
		got[index] = candidates[index].pattern
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate priority = %v, want %v", got, want)
	}
}

func compiledRuntimeMatcherPatterns(matchers *runtimePathMatchers) []string {
	patterns := []string{}
	for pattern, matcher := range matchers.byPattern {
		if matcher.glob != nil {
			patterns = append(patterns, pattern)
		}
	}
	sort.Strings(patterns)
	return patterns
}

func TestCompileRuntimePathMatchersIncludesNestedRulePatterns(t *testing.T) {
	rules := []policy.Rule{{
		Paths:       []string{"src/**"},
		BeforePaths: []string{"docs/**"},
		WhenPaths:   []string{"**/*.go"},
		ScopePaths:  []string{"pkg/**"},
		Checks: []policy.Check{{
			Paths: []string{"generated/**"},
		}},
	}}
	matchers, err := compileRuntimePathMatchers(rules)
	if err != nil {
		t.Fatalf("compileRuntimePathMatchers: %v", err)
	}
	for _, pattern := range []string{"src/**", "docs/**", "**/*.go", "pkg/**", "generated/**"} {
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

func BenchmarkOverBudgetRuntimePathMatchers(b *testing.B) {
	rules, hotPatterns := overBudgetRuntimeMatcherRules()
	matchers, err := compileRuntimePathMatchers(rules)
	if err != nil {
		b.Fatal(err)
	}
	fallbacks := 0
	for _, pattern := range hotPatterns {
		if matchers.byPattern[pattern].glob == nil {
			fallbacks++
		}
	}
	allFallbacks := 0
	for _, matcher := range matchers.byPattern {
		if matcher.glob == nil {
			allFallbacks++
		}
	}
	cold, err := CompilePathMatcher(rules[0].Paths[0])
	if err != nil || cold.glob == nil {
		b.Fatalf("cold matcher was not compilable: %v", err)
	}
	hot, err := CompilePathMatcher(hotPatterns[0])
	if err != nil || hot.glob == nil {
		b.Fatalf("hot matcher was not compilable: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.ReportMetric(float64(allFallbacks), "all-fallbacks")
	b.ReportMetric(float64(cold.glob.LogicalBytes()), "cold-bytes")
	b.ReportMetric(float64(hot.glob.LogicalBytes()), "hot-bytes")
	b.ReportMetric(float64(fallbacks), "hot-fallbacks")
	for range b.N {
		for index, pattern := range hotPatterns {
			if !matchers.byPattern[pattern].Match("z-hot-" + strconv.Itoa(index) + "/entry0/file.go") {
				b.Fatal("hot matcher missed")
			}
		}
	}
}

func overBudgetRuntimeMatcherRules() ([]policy.Rule, []string) {
	rules := make([]policy.Rule, 0, 48+8*64)
	for index := 0; index < 48; index++ {
		rules = append(rules, policy.Rule{Paths: []string{largeRuntimeMatcherPattern("a-cold-"+strconv.Itoa(index), 256)}})
	}
	hotPatterns := make([]string, 8)
	for index := range hotPatterns {
		pattern := largeRuntimeMatcherPattern("z-hot-"+strconv.Itoa(index), 96)
		hotPatterns[index] = pattern
		for range 64 {
			rules = append(rules, policy.Rule{Paths: []string{pattern}})
		}
	}
	return rules, hotPatterns
}

func largeRuntimeMatcherPattern(prefix string, alternatives int) string {
	var pattern strings.Builder
	pattern.WriteString(prefix)
	pattern.WriteString("/{")
	for index := 0; index < alternatives; index++ {
		if index > 0 {
			pattern.WriteByte(',')
		}
		pattern.WriteString("entry")
		pattern.WriteString(strconv.Itoa(index))
	}
	pattern.WriteString("}/**")
	return pattern.String()
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
