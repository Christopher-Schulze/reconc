package shellcommand

import "testing"

// TestWordsResolveShellQuotingAndEscapes pins what a matcher is allowed to
// compare: the literal string the shell hands to execve. Quote removal alone
// is not enough. A backslash outside quotes preserves the next character, so
// `\rm` runs rm; that escape is the documented alias bypass and an agent can
// use it to walk past a forbid_command rule.
func TestWordsResolveShellQuotingAndEscapes(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{name: "plain", command: `rm -rf build`, want: []string{"rm", "-rf", "build"}},
		{name: "alias bypass escape", command: `\rm -rf build`, want: []string{"rm", "-rf", "build"}},
		{name: "escape on every character", command: `\r\m -rf build`, want: []string{"rm", "-rf", "build"}},
		{name: "empty single quotes", command: `r''m -rf build`, want: []string{"rm", "-rf", "build"}},
		{name: "double quoted program", command: `"rm" -rf build`, want: []string{"rm", "-rf", "build"}},
		{name: "single quoted program", command: `'rm' -rf build`, want: []string{"rm", "-rf", "build"}},
		{name: "escaped separator", command: `find . -exec rm {} \;`, want: []string{"find", ".", "-exec", "rm", "{}", ";"}},
		{name: "escaped space joins one word", command: `rm my\ file`, want: []string{"rm", "my file"}},
		{name: "backslash is literal in single quotes", command: `rm '\x'`, want: []string{"rm", `\x`}},
		{name: "backslash is literal before a plain char in double quotes", command: `rm "a\xb"`, want: []string{"rm", `a\xb`}},
		{name: "backslash escapes a quote inside double quotes", command: `rm "a\"b"`, want: []string{"rm", `a"b`}},
		{name: "backslash escapes a dollar inside double quotes", command: `rm "a\$b"`, want: []string{"rm", "a$b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invocations, complete := Invocations(tc.command, 8)
			if !complete || len(invocations) == 0 {
				t.Fatalf("analysis incomplete for %q", tc.command)
			}
			got := invocations[0].Words
			if len(got) != len(tc.want) {
				t.Fatalf("words = %q, want %q", got, tc.want)
			}
			for index := range tc.want {
				if got[index] != tc.want[index] {
					t.Fatalf("words = %q, want %q", got, tc.want)
				}
			}
		})
	}
}

// TestAnsiCQuotingIsNotSilentlyMisread keeps the one escape family this package
// does not decode out of the comparison path. $'\x72\x6d' is "rm"; reporting
// the undecoded text as static would let a matcher approve a program it never
// examined, so the word is reported dynamic and callers fail closed.
func TestAnsiCQuotingIsNotSilentlyMisread(t *testing.T) {
	invocations, complete := Invocations(`$'\x72\x6d' -rf build`, 8)
	if complete {
		t.Fatalf("ANSI-C quoted program was reported as fully analysed: %+v", invocations)
	}
	if _, reason := InvocationsWithReason(`$'\x72\x6d' -rf build`, 8); reason != IncompleteDynamicCommand {
		t.Fatalf("reason = %q, want %q", reason, IncompleteDynamicCommand)
	}
	// A dollar-single-quoted string without escapes carries no hidden decoding.
	plain, complete := Invocations(`$'rm' -rf build`, 8)
	if !complete || len(plain) == 0 || plain[0].Words[0] != "rm" {
		t.Fatalf("plain ANSI-C word = %+v complete=%v", plain, complete)
	}
}

// TestMatchFoldingExecutableIsDirectional keeps the case rule where it belongs:
// the program name only, and only for callers that deny.
func TestMatchFoldingExecutableIsDirectional(t *testing.T) {
	invocations, complete := Invocations(`RM -rf build`, 8)
	if !complete || len(invocations) == 0 {
		t.Fatal("analysis incomplete")
	}
	if matched, _ := Match(invocations[0], "rm -rf build", false); matched {
		t.Fatal("the evidence direction must stay case-sensitive")
	}
	if matched, _ := MatchFoldingExecutable(invocations[0], "rm -rf build", false); !matched {
		t.Fatal("the deny direction must fold the program name")
	}

	argued, complete := Invocations(`rm -RF build`, 8)
	if !complete || len(argued) == 0 {
		t.Fatal("analysis incomplete")
	}
	if matched, _ := MatchFoldingExecutable(argued[0], "rm -rf build", false); matched {
		t.Fatal("arguments must stay case-sensitive in both directions")
	}
}
