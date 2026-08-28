package shellcommand

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEvalUsesPostQuoteRemovalArguments(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    [][]string
	}{
		{
			name:    "outer quotes do not survive",
			command: `eval printf "a b"`,
			want:    [][]string{{"eval", "printf", "a b"}, {"printf", "a", "b"}},
		},
		{
			name:    "literal quotes survive",
			command: `eval printf '"a b"'`,
			want:    [][]string{{"eval", "printf", `"a b"`}, {"printf", "a b"}},
		},
		{
			name:    "literal backslash protects nested space",
			command: `eval printf 'a\ b'`,
			want:    [][]string{{"eval", "printf", `a\ b`}, {"printf", "a b"}},
		},
		{
			name:    "empty outer argument becomes separator only",
			command: `eval printf "" tail`,
			want:    [][]string{{"eval", "printf", "", "tail"}, {"printf", "tail"}},
		},
		{
			name:    "unquoted nested semicolon remains syntax",
			command: `eval echo ';'`,
			want:    [][]string{{"eval", "echo", ";"}, {"echo"}},
		},
		{
			name:    "backslash-protected nested semicolon remains data",
			command: `eval echo '\;'`,
			want:    [][]string{{"eval", "echo", `\;`}, {"echo", ";"}},
		},
		{
			name:    "pipeline and redirect are reparsed",
			command: `eval 'printf x | cat >out'`,
			want:    [][]string{{"eval", "printf x | cat >out"}, {"printf", "x"}, {"cat"}},
		},
		{
			name:    "nested command substitution remains executable",
			command: `eval 'echo $(git clean -fd)'`,
			want:    [][]string{{"eval", "echo $(git clean -fd)"}, {"echo", "$(git clean -fd)"}, {"git", "clean", "-fd"}},
		},
		{
			name:    "nested shell wrapper preserves its body",
			command: `eval "sh -c 'git clean -fd'"`,
			want:    [][]string{{"eval", "sh -c 'git clean -fd'"}, {"sh", "-c", "git clean -fd"}, {"git", "clean", "-fd"}},
		},
		{
			name:    "nested static eval",
			command: `eval "eval 'git clean -fd'"`,
			want:    [][]string{{"eval", "eval 'git clean -fd'"}, {"eval", "git clean -fd"}, {"git", "clean", "-fd"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocations, reason := InvocationsWithReason(test.command, 16)
			if reason != IncompleteNone {
				t.Fatalf("analysis reason = %q", reason)
			}
			got := make([][]string, len(invocations))
			for index := range invocations {
				got[index] = invocations[index].Words
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Invocations(%q) = %#v, want %#v", test.command, got, test.want)
			}
		})
	}
}

func TestEvalMatchingPreservesSecurityBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		wantExact    bool
		wantPrefix   bool
		wantComplete bool
	}{
		{
			name:         "literal nested quotes preserve one argument",
			command:      `eval rm -rf '"build cache"'`,
			wantExact:    true,
			wantPrefix:   true,
			wantComplete: true,
		},
		{
			name:         "outer quotes do not preserve one argument",
			command:      `eval rm -rf "build cache"`,
			wantPrefix:   true,
			wantComplete: true,
		},
		{
			name:         "dynamic eval remains uncertain",
			command:      `eval "$COMMAND"`,
			wantComplete: false,
		},
	}
	compiled := CompileExpectation(`rm -rf "build cache"`, 8)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocations, complete := Invocations(test.command, 16)
			if complete != test.wantComplete {
				t.Fatalf("complete = %t, want %t", complete, test.wantComplete)
			}
			var exact, prefix bool
			for _, invocation := range invocations {
				matched, uncertain := compiled.Match(invocation, false, true)
				exact = exact || matched || uncertain
				matched, uncertain = MatchFoldingExecutable(invocation, "rm -rf", true)
				prefix = prefix || matched || uncertain
			}
			if exact != test.wantExact || prefix != test.wantPrefix {
				t.Fatalf("matches = exact:%t prefix:%t, want exact:%t prefix:%t", exact, prefix, test.wantExact, test.wantPrefix)
			}
		})
	}
}

func FuzzEvalStaticBodyMatchesDirectParse(f *testing.F) {
	for _, seed := range []string{"", "a b", "a'b", `a\b`, "; | >", "line one\nline two", `"quoted"`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return
		}
		body := "printf '%s' " + quoteShellWordForTest(value)
		direct, directReason := InvocationsWithReason(body, 16)
		if directReason != IncompleteNone || len(direct) != 1 {
			// Some valid UTF-8 control characters are outside mvdan's shell
			// grammar even inside a quoted word. There is no direct/eval
			// equivalence to assert for a body the reference parser rejects.
			if directReason == IncompleteUnparsable {
				return
			}
			t.Fatalf("direct reference parse failed: reason=%q invocations=%#v", directReason, direct)
		}

		evaluated, evalReason := InvocationsWithReason("eval "+quoteShellWordForTest(body), 16)
		if evalReason != IncompleteNone {
			t.Fatalf("eval parse failed: reason=%q", evalReason)
		}
		// The outer shell may normalize line endings while parsing the
		// argument. If quote removal did not preserve the exact eval body,
		// the direct body is not the command that eval reparses.
		if len(evaluated) == 0 || len(evaluated[0].Words) < 2 || evaluated[0].Words[1] != body {
			return
		}
		if len(evaluated) != 2 || !reflect.DeepEqual(evaluated[1], direct[0]) {
			t.Fatalf("eval nested parse = %#v, direct = %#v", evaluated, direct)
		}
	})
}

func quoteShellWordForTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
