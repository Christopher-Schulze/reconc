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
				if got := compiled.Match(name); got != want {
					t.Errorf("pattern %q name %q = %t, want %t", pattern, name, got, want)
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
			if got, want := compiled.Match(name), doublestar.MatchUnvalidated(pattern, name); got != want {
				t.Fatalf("pattern %q name %q = %t, want %t", pattern, name, got, want)
			}
		}
	}
}

func TestCompiledGlobRejectsExpansionAbovePlanAdmission(t *testing.T) {
	t.Parallel()
	pattern := strings.Repeat("{"+strings.Repeat("a", 100)+","+strings.Repeat("b", 100)+"}", 15)
	if len(pattern) > MaxPatternBytes {
		t.Fatal("test pattern exceeds source boundary")
	}
	if _, err := compileGlob(pattern); err == nil || !strings.Contains(err.Error(), "admission limit") {
		t.Fatalf("error = %v, want admission-limit rejection", err)
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
		if got, want := compiled.Match(name), doublestar.MatchUnvalidated(pattern, name); got != want {
			t.Fatalf("pattern %q name %q = %t, want %t", pattern, name, got, want)
		}
	})
}
