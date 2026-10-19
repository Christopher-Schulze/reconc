package shellcommand

import "testing"

func TestDenyMatchSeesThroughGitGlobalOptions(t *testing.T) {
	cases := []struct {
		command   string
		matched   bool
		uncertain bool
	}{
		{command: "git -C other clean -n", matched: true},
		{command: "git -Cother clean -fd", matched: true},
		{command: "git -c core.pager=cat clean -nd", matched: true},
		{command: "git --git-dir=.git --work-tree=. clean", matched: true},
		{command: "git --no-pager -C dir clean -x", matched: true},
		{command: "git clean -n", matched: true},
		{command: "git -C other status", matched: false},
		{command: "git --version", matched: false},
		{command: `git -C "$DIR" clean -n`, uncertain: true},
		{command: "git --unknown-global clean", uncertain: true},
	}
	for _, testCase := range cases {
		invocations, complete := Invocations(testCase.command, 8)
		if !complete || len(invocations) != 1 {
			t.Fatalf("%q: invocations=%+v complete=%v", testCase.command, invocations, complete)
		}
		matched, uncertain := MatchFoldingExecutable(invocations[0], "git clean", true)
		if matched != testCase.matched || uncertain != testCase.uncertain {
			t.Errorf("%q: matched=%v uncertain=%v, want matched=%v uncertain=%v", testCase.command, matched, uncertain, testCase.matched, testCase.uncertain)
		}
	}
}

func TestEvidenceMatchKeepsGitGlobalOptionsLiteral(t *testing.T) {
	invocations, complete := Invocations("git -C ../other diff --check", 8)
	if !complete || len(invocations) != 1 {
		t.Fatalf("invocations=%+v complete=%v", invocations, complete)
	}
	if matched, uncertain := Match(invocations[0], "git diff --check", false); matched || uncertain {
		t.Fatalf("evidence matching credited a command run in another directory: matched=%v uncertain=%v", matched, uncertain)
	}
}

func TestHasFileWriteRedirectSeparatesWritesFromDescriptorDuplication(t *testing.T) {
	cases := []struct {
		command string
		writes  bool
	}{
		{"reconc status .", false},
		{"reconc status . 2>&1", false},
		{"reconc status . >&2", false},
		{"reconc status . 2>/dev/null", false},
		{"reconc status . <input.txt", false},
		{"cat <<'EOF'\nx\nEOF", false},
		{"reconc status . > out.txt", true},
		{"reconc status . >> out.txt", true},
		{"reconc status . &> out.txt", true},
		{"reconc status . >| out.txt", true},
		{"reconc status . >&out.txt", true},
		{`reconc status . > "$TARGET"`, true},
		{"echo ok && (reconc status . > nested.txt)", true},
	}
	for _, testCase := range cases {
		writes, complete := HasFileWriteRedirect(testCase.command)
		if !complete || writes != testCase.writes {
			t.Errorf("%q: writes=%v complete=%v, want writes=%v", testCase.command, writes, complete, testCase.writes)
		}
	}
	if _, complete := HasFileWriteRedirect("reconc status . >"); complete {
		t.Error("unparsable redirect was reported complete")
	}
}
