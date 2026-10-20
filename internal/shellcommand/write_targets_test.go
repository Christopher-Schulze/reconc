package shellcommand

import (
	"slices"
	"testing"
)

func TestLiteralWriteTargetsFindsStaticWriters(t *testing.T) {
	tests := []struct {
		command  string
		want     []string
		complete bool
		moves    bool
	}{
		{"printf '' >> AGENTS.md", []string{"AGENTS.md"}, true, false},
		{"echo x > AGENTS.md 2>&1", []string{"AGENTS.md"}, true, false},
		{"echo x 2>/dev/null; cat <<EOF > notes.md\nx\nEOF", []string{"notes.md"}, true, false},
		{"echo x | tee -a AGENTS.md log.txt", []string{"AGENTS.md", "log.txt"}, true, false},
		{"cp -r src AGENTS.md", []string{"AGENTS.md", "AGENTS.md/src"}, true, false},
		{"cp a b docs/", []string{"docs/", "docs/a", "docs/b"}, true, false},
		{"cp -t docs a", []string{"docs/a"}, true, false},
		{"mv old.md AGENTS.md", []string{"AGENTS.md", "AGENTS.md/old.md", "old.md"}, true, false},
		{"touch AGENTS.md && rm -f stale.txt", []string{"AGENTS.md", "stale.txt"}, true, false},
		{"sed -i 's/a/b/' AGENTS.md", []string{"AGENTS.md"}, true, false},
		{"sed -i '' -e 's/a/b/' AGENTS.md", []string{"AGENTS.md"}, true, false},
		{"sh -c 'echo x > AGENTS.md'", []string{"AGENTS.md"}, true, false},
		{"echo \"$(touch AGENTS.md)\"", []string{"AGENTS.md"}, true, false},
		{"echo x > \"$TARGET\"", nil, false, false},
		{"cp a \"$DEST\"", nil, false, false},
		{"cd docs && echo x > notes.md", []string{"notes.md"}, true, true},
		{"cat AGENTS.md; grep x AGENTS.md; ls 2>&1", nil, true, false},
		{"sed 's/a/b/' AGENTS.md > out.md", []string{"out.md"}, true, false},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			got := LiteralWriteTargets(test.command, 16)
			if !slices.Equal(sortedStrings(got.Paths), sortedStrings(test.want)) || got.Complete != test.complete || got.ChangesDirectory != test.moves {
				t.Fatalf("LiteralWriteTargets = %+v, want paths %v complete %v moves %v", got, test.want, test.complete, test.moves)
			}
		})
	}
}

func TestXargsWithALiteralUtilityIsAnalyzed(t *testing.T) {
	for _, test := range []struct {
		command string
		want    []string
	}{
		{"ls | xargs -n1 dirname", []string{"dirname"}},
		{"find . -print0 | xargs -0rn1 rm", []string{"rm"}},
		{"xargs -I{} cp {} dst", []string{"cp", "{}", "dst"}},
		{"xargs -i echo", []string{"echo"}},
		{"xargs -J % mv % dst", []string{"mv", "%", "dst"}},
		{"xargs --max-args=1 -P 4 dirname", []string{"dirname"}},
	} {
		t.Run(test.command, func(t *testing.T) {
			invocations, reason := InvocationsWithReason(test.command, 16)
			if reason != IncompleteNone || !slices.ContainsFunc(invocations, func(invocation Invocation) bool {
				return slices.Equal(invocation.Words, test.want)
			}) {
				t.Fatalf("invocations=%v reason=%q, want %v", invocations, reason, test.want)
			}
		})
	}
	for _, command := range []string{`xargs "$UTILITY"`, "xargs -q dirname", "xargs --max-arg 1 dirname", "xargs -n"} {
		if _, reason := InvocationsWithReason(command, 16); reason == IncompleteNone {
			t.Fatalf("%q was analyzed as complete", command)
		}
	}
}

func sortedStrings(values []string) []string {
	copied := append([]string{}, values...)
	slices.Sort(copied)
	return copied
}
