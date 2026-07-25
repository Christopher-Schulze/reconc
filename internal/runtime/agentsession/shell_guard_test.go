package agentsession

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/shellcommand"
)

// TestForbiddenShellCommandReasonAllowsAnalyzableCommands pins the boundary:
// adding cause attribution must not turn previously permitted commands into
// blocks. An empty reason means the guard permits the command.
func TestForbiddenShellCommandReasonAllowsAnalyzableCommands(t *testing.T) {
	for _, command := range []string{
		"git status",
		"git status --short --branch -uall",
		"git clean -nd",
		"git reset --soft HEAD~1",
		`git commit -m "$MESSAGE"`,
		"go test ./... -race -count=1",
		"echo a && echo b && echo c",
		`B=value; echo "$B"`,
	} {
		if reason := forbiddenShellCommandReason(command); reason != "" {
			t.Fatalf("command %q must be permitted, got block: %s", command, reason)
		}
	}
}

// TestForbiddenShellCommandReasonKeepsDestructiveBlocks pins that the
// destructive-command branches still fire and still carry their remediation.
func TestForbiddenShellCommandReasonKeepsDestructiveBlocks(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		wantPhrases []string
	}{
		{
			name:        "git clean without dry run",
			command:     "git clean -fd",
			wantPhrases: []string{"git clean", "git clean -nd", "approval"},
		},
		{
			name:        "git hard reset",
			command:     "git reset --hard origin/main",
			wantPhrases: []string{"git reset --hard", "approval"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reason := forbiddenShellCommandReason(test.command)
			if reason == "" {
				t.Fatal("command must be blocked")
			}
			assertPhrases(t, reason, test.wantPhrases)
		})
	}
}

// TestForbiddenShellCommandReasonExplainsUnanalyzableCommands is the regression
// for the fail-closed block that returned one generic sentence with no cause and
// no fix, forcing the agent to bisect its own command. Each branch must now name
// what went wrong and what to write instead.
func TestForbiddenShellCommandReasonExplainsUnanalyzableCommands(t *testing.T) {
	deep := "git clean -fd"
	for range maxShellGuardDepth + 4 {
		deep = "echo $(" + deep + ")"
	}
	tests := []struct {
		name        string
		command     string
		wantPhrases []string
	}{
		{
			name:        "variable command word names the dynamic executable and the literal fix",
			command:     `B=echo; $B hello`,
			wantPhrases: []string{"executable", "expansion", "literal"},
		},
		{
			name:        "substituted command word takes the same branch",
			command:     `$(printf git) clean -fd`,
			wantPhrases: []string{"executable", "literal"},
		},
		{
			name:        "over-deep nesting names the depth and the flatten fix",
			command:     deep,
			wantPhrases: []string{"nested", "16", "Flatten"},
		},
		{
			name:        "oversized command names the split fix",
			command:     strings.Repeat("x", (256<<10)+1),
			wantPhrases: []string{"larger than", "Split"},
		},
		{
			name:        "invalid syntax names the syntax fix",
			command:     `git "clean -fd`,
			wantPhrases: []string{"Bash syntax", "Fix the syntax"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reason := forbiddenShellCommandReason(test.command)
			if reason == "" {
				t.Fatal("unanalyzable command must be blocked")
			}
			if strings.Contains(reason, "bounded policy safety limits") {
				t.Fatalf("block fell back to the generic pre-fix message: %s", reason)
			}
			assertPhrases(t, reason, test.wantPhrases)
			if strings.Contains(reason, "\n") {
				t.Fatalf("block message must stay single-line: %q", reason)
			}
		})
	}
}

// TestUnanalyzableReasonsAreDistinct guards against two causes collapsing onto
// one message, which would reintroduce the ambiguity this fix removed.
func TestUnanalyzableReasonsAreDistinct(t *testing.T) {
	seen := map[string]shellcommand.IncompleteReason{}
	for _, reason := range []shellcommand.IncompleteReason{
		shellcommand.IncompleteDynamicCommand,
		shellcommand.IncompleteNestingDepth,
		shellcommand.IncompleteTooLarge,
		shellcommand.IncompleteUnparsable,
		shellcommand.IncompleteAnalysisState,
	} {
		message := unanalyzableShellCommandReason(reason)
		if message == "" {
			t.Fatalf("reason %q produced an empty message", reason)
		}
		if previous, duplicate := seen[message]; duplicate {
			t.Fatalf("reasons %q and %q share the same message", previous, reason)
		}
		seen[message] = reason
	}
}

func assertPhrases(t *testing.T, reason string, phrases []string) {
	t.Helper()
	for _, phrase := range phrases {
		if !strings.Contains(reason, phrase) {
			t.Fatalf("block message %q is missing %q", reason, phrase)
		}
	}
}
