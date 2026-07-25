package shellcommand

import (
	"reflect"
	"strings"
	"testing"
)

func TestInvocationsFindsExecutablePositionsWithoutLiteralFalsePositives(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    [][]string
	}{
		{name: "direct", command: "git clean -fd", want: [][]string{{"git", "clean", "-fd"}}},
		{name: "compound", command: "echo ready && git clean -nd", want: [][]string{{"echo", "ready"}, {"git", "clean", "-nd"}}},
		{name: "quoted literal", command: `echo "git clean -fd"`, want: [][]string{{"echo", "git clean -fd"}}},
		{name: "plain arguments", command: "echo git clean -fd", want: [][]string{{"echo", "git", "clean", "-fd"}}},
		{name: "shell body", command: `bash -lc "git clean -fd"`, want: [][]string{{"bash", "-lc", "git clean -fd"}, {"git", "clean", "-fd"}}},
		{name: "eval body", command: `eval 'git clean -fd'`, want: [][]string{{"eval", "git clean -fd"}, {"git", "clean", "-fd"}}},
		{name: "substitution", command: `echo "$(git clean -fd)"`, want: [][]string{{"echo", `"$(git clean -fd)"`}, {"git", "clean", "-fd"}}},
		{name: "wrapped", command: "env MODE=1 sudo -- git clean -fd", want: [][]string{{"git", "clean", "-fd"}}},
		{name: "find exec shell", command: `find . -exec sh -c 'git clean -fd' \;`, want: [][]string{{"find", ".", "-exec", "sh", "-c", "git clean -fd", `\;`}, {"sh", "-c", "git clean -fd"}, {"git", "clean", "-fd"}}},
		{name: "find ok shell", command: `find . -ok sh -c 'git clean -fd' \;`, want: [][]string{{"find", ".", "-ok", "sh", "-c", "git clean -fd", `\;`}, {"sh", "-c", "git clean -fd"}, {"git", "clean", "-fd"}}},
		{name: "xargs shell", command: `printf '%s\n' x | xargs -n 1 sh -c 'git clean -fd'`, want: [][]string{{"printf", "%s\\n", "x"}, {"xargs", "-n", "1", "sh", "-c", "git clean -fd"}, {"sh", "-c", "git clean -fd"}, {"git", "clean", "-fd"}}},
		{name: "subshell group", command: `(git clean -fd)`, want: [][]string{{"git", "clean", "-fd"}}},
		{name: "brace group", command: `{ git clean -fd; }`, want: [][]string{{"git", "clean", "-fd"}}},
		{name: "process substitution", command: `cat <(git clean -fd)`, want: [][]string{{"cat", "<(git clean -fd)"}, {"git", "clean", "-fd"}}},
		{name: "leading redirect", command: `>output.log git clean -fd`, want: [][]string{{"git", "clean", "-fd"}}},
		{name: "comment stays literal", command: "echo ready # git clean -fd\nprintf done", want: [][]string{{"echo", "ready"}, {"printf", "done"}}},
		{name: "here document stays literal", command: "cat <<'EOF'\ngit clean -fd\nEOF\nprintf done", want: [][]string{{"cat"}, {"printf", "done"}}},
		{name: "case body", command: "case x in x) git clean -fd;; esac", want: [][]string{{"git", "clean", "-fd"}}},
		{name: "time wrapper", command: "/usr/bin/time -p git clean -fd", want: [][]string{{"git", "clean", "-fd"}}},
		{name: "timeout wrapper", command: "timeout -k 1s 5s git clean -fd", want: [][]string{{"git", "clean", "-fd"}}},
		{name: "nice wrapper", command: "nice -n 5 git clean -fd", want: [][]string{{"git", "clean", "-fd"}}},
		{name: "flock launcher", command: "flock -n /tmp/reconc.lock git clean -fd", want: [][]string{{"flock", "-n", "/tmp/reconc.lock", "git", "clean", "-fd"}, {"git", "clean", "-fd"}}},
		{name: "flock shell launcher", command: `flock /tmp/reconc.lock -c 'git clean -fd'`, want: [][]string{{"flock", "/tmp/reconc.lock", "-c", "git clean -fd"}, {"sh", "-c", "git clean -fd"}, {"git", "clean", "-fd"}}},
		{name: "watch launcher", command: "watch -n 2 git clean -fd", want: [][]string{{"watch", "-n", "2", "git", "clean", "-fd"}, {"git", "clean", "-fd"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, complete := Invocations(test.command, 16)
			if !complete {
				t.Fatal("analysis unexpectedly incomplete")
			}
			words := make([][]string, 0, len(got))
			for _, invocation := range got {
				words = append(words, invocation.Words)
			}
			if !reflect.DeepEqual(words, test.want) {
				t.Fatalf("Invocations(%q) words = %#v, want %#v", test.command, words, test.want)
			}
		})
	}
}

func TestInvocationsFailClosedOnDynamicExecutable(t *testing.T) {
	for _, command := range []string{`$COMMAND clean -fd`, `eval "$COMMAND"`, `sh -c "$COMMAND"`, `$(printf git) clean -fd`, `env -S "git clean -fd"`, `xargs "$COMMAND"`} {
		if _, complete := Invocations(command, 16); complete {
			t.Fatalf("dynamic executable analysis must be incomplete: %s", command)
		}
	}
}

func TestInvocationsWithReasonAttributesEachIncompletenessCause(t *testing.T) {
	deep := "git clean -fd"
	for range 18 {
		deep = "echo $(" + deep + ")"
	}
	tests := []struct {
		name     string
		command  string
		maxDepth int
		want     IncompleteReason
	}{
		{name: "literal command resolves", command: "git status", maxDepth: 16, want: IncompleteNone},
		{name: "literal chain resolves", command: "git status && echo done", maxDepth: 16, want: IncompleteNone},
		{name: "dynamic argument still resolves", command: `git commit -m "$MESSAGE"`, maxDepth: 16, want: IncompleteNone},
		{name: "variable command word", command: `$COMMAND clean -fd`, maxDepth: 16, want: IncompleteDynamicCommand},
		{name: "assignment then variable command word", command: `B=echo; $B hello`, maxDepth: 16, want: IncompleteDynamicCommand},
		{name: "substituted command word", command: `$(printf git) clean -fd`, maxDepth: 16, want: IncompleteDynamicCommand},
		{name: "dynamic eval body", command: `eval "$COMMAND"`, maxDepth: 16, want: IncompleteDynamicCommand},
		{name: "dynamic launcher target", command: `xargs "$COMMAND"`, maxDepth: 16, want: IncompleteDynamicCommand},
		{name: "nesting past budget", command: deep, maxDepth: 16, want: IncompleteNestingDepth},
		{name: "oversized command", command: strings.Repeat("x", maxCommandBytes+1), maxDepth: 16, want: IncompleteTooLarge},
		{name: "unbalanced quote", command: `git "clean -fd`, maxDepth: 16, want: IncompleteUnparsable},
		{name: "unbalanced parenthesis", command: `echo $(git status`, maxDepth: 16, want: IncompleteUnparsable},
		{name: "negative depth budget", command: "git status", maxDepth: -1, want: IncompleteAnalysisState},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, got := InvocationsWithReason(test.command, test.maxDepth); got != test.want {
				t.Fatalf("reason = %q, want %q", got, test.want)
			}
		})
	}
}

// TestInvocationsWrapperAgreesWithReason pins the compatibility contract: the
// boolean wrapper must permit exactly the inputs whose reason is
// IncompleteNone, so adding cause attribution cannot silently change which
// commands the enforcement callers accept.
func TestInvocationsWrapperAgreesWithReason(t *testing.T) {
	deep := "git clean -fd"
	for range 18 {
		deep = "echo $(" + deep + ")"
	}
	for _, command := range []string{
		"git status",
		"git status && echo done",
		`git commit -m "$MESSAGE"`,
		"git \\\nclean -fd",
		`$COMMAND clean -fd`,
		`B=echo; $B hello`,
		`eval "$COMMAND"`,
		`sh -c "$COMMAND"`,
		`env -S "git clean -fd"`,
		`xargs "$COMMAND"`,
		deep,
		`git "clean -fd`,
		strings.Repeat("x", maxCommandBytes+1),
	} {
		invocations, complete := Invocations(command, 16)
		reasonInvocations, reason := InvocationsWithReason(command, 16)
		if complete != (reason == IncompleteNone) {
			t.Fatalf("complete=%t but reason=%q for %q", complete, reason, command)
		}
		if !reflect.DeepEqual(invocations, reasonInvocations) {
			t.Fatalf("wrapper returned different invocations for %q", command)
		}
	}
}

func TestAdversarialCommandDiscovery(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		wantMatch    bool
		wantComplete bool
	}{
		{name: "single-quoted inert text", command: `printf '%s' 'git clean -fd'`, wantComplete: true},
		{name: "double-quoted inert text", command: `printf '%s' "git clean -fd"`, wantComplete: true},
		{name: "nested static eval", command: `eval "eval 'git clean -fd'"`, wantMatch: true, wantComplete: true},
		{name: "wrapper chain", command: `env -i sudo -n command -- exec git clean -fd`, wantMatch: true, wantComplete: true},
		{name: "command substitution", command: `printf '%s' "$(sh -c 'git clean -fd')"`, wantMatch: true, wantComplete: true},
		{name: "process substitution", command: `cat <(env -- git clean -fd)`, wantMatch: true, wantComplete: true},
		{name: "dynamic eval", command: `eval "$DANGEROUS_COMMAND"`, wantComplete: false},
		{name: "dynamic nested shell", command: `sh -c "$(printf '%s' "$DANGEROUS_COMMAND")"`, wantComplete: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocations, complete := Invocations(test.command, 16)
			if complete != test.wantComplete {
				t.Fatalf("complete=%t, want %t; invocations=%#v", complete, test.wantComplete, invocations)
			}
			matched := false
			for _, invocation := range invocations {
				invocationMatched, _ := Match(invocation, "git clean -fd", false)
				matched = matched || invocationMatched
			}
			if matched != test.wantMatch {
				t.Fatalf("matched=%t, want %t; invocations=%#v", matched, test.wantMatch, invocations)
			}
		})
	}
}

func TestMatchScopesDynamicUncertaintyToRelevantCommands(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		expected  string
		prefix    bool
		matched   bool
		uncertain bool
	}{
		{name: "static prefix", command: "pip install requests", expected: "pip install", prefix: true, matched: true},
		{name: "absolute executable", command: "/usr/local/bin/pip install requests", expected: "pip install", prefix: true, matched: true},
		{name: "explicit executable path stays exact", command: "/opt/tools/pip install requests", expected: "/usr/local/bin/pip install", prefix: true},
		{name: "quoted static", command: `rm -f "file name"`, expected: `rm -f 'file name'`, matched: true},
		{name: "relevant dynamic argument", command: `pip "$ACTION" requests`, expected: "pip install", prefix: true, uncertain: true},
		{name: "unrelated dynamic argument", command: `echo "$ACTION"`, expected: "pip install", prefix: true},
		{name: "static mismatch before dynamic", command: `pip uninstall "$PACKAGE"`, expected: "pip install", prefix: true},
		{name: "dynamic duplicate stays visible", command: `echo '$ACTION'; echo "$ACTION"`, expected: "echo install", prefix: true, uncertain: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocations, complete := Invocations(test.command, 16)
			if !complete || len(invocations) == 0 {
				t.Fatalf("invocations=%#v complete=%t", invocations, complete)
			}
			matched := false
			uncertain := false
			for _, invocation := range invocations {
				invocationMatched, invocationUncertain := Match(invocation, test.expected, test.prefix)
				matched = matched || invocationMatched
				uncertain = uncertain || invocationUncertain
			}
			if matched != test.matched || uncertain != test.uncertain {
				t.Fatalf("Match()=(%t,%t), want (%t,%t)", matched, uncertain, test.matched, test.uncertain)
			}
		})
	}
}

func TestInvocationsFoldsContinuationsAndBoundsDepth(t *testing.T) {
	invocations, complete := Invocations("git \\\nclean -fd", 16)
	if !complete || len(invocations) != 1 || !reflect.DeepEqual(invocations[0].Words, []string{"git", "clean", "-fd"}) {
		t.Fatalf("line-continuation analysis = %#v, complete=%t", invocations, complete)
	}

	deep := "git clean -fd"
	for range 18 {
		deep = "echo $(" + deep + ")"
	}
	if _, complete := Invocations(deep, 16); complete {
		t.Fatal("over-deep nested shell analysis must report incomplete")
	}
	if _, complete := Invocations(strings.Repeat("x", maxCommandBytes+1), 16); complete {
		t.Fatal("oversized shell analysis must report incomplete")
	}
}
