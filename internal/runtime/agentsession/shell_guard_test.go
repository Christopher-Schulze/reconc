package agentsession

import (
	"os/exec"
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

func TestForbiddenShellCommandReasonRejectsDynamicFindExpressions(t *testing.T) {
	for _, command := range []string{
		`find "$ROOT" -exec git clean -fd \;`,
		`find . -exec git "$ACTION" \;`,
		`find . -exec git clean -fd \; "$AFTER"`,
	} {
		reason := forbiddenShellCommandReason(command)
		if reason == "" {
			t.Fatalf("dynamic find expression %q was accepted", command)
		}
		if !strings.Contains(reason, "executable") || !strings.Contains(reason, "literal") {
			t.Fatalf("dynamic find expression %q returned an unhelpful block: %s", command, reason)
		}
	}
}

func TestForbiddenShellCommandReasonExpandsInlineGitAliases(t *testing.T) {
	blocked := []string{
		`git -c alias.wipe='!git clean -fd' wipe`,
		`git -c alias.rewind='reset --hard' rewind`,
		`git -c alias.rewind=reset rewind --hard`,
		`git -c alias.rewind='!git reset' rewind --hard`,
		`git -c alias.rewind=reset rewind "$MODE"`,
		`git -c alias.one=two -c 'alias.two=reset --hard' one`,
		`git config alias.blast '!git clean -fd' && git blast`,
	}
	for _, command := range blocked {
		if reason := forbiddenShellCommandReason(command); reason == "" {
			t.Fatalf("destructive alias command %q must be blocked", command)
		}
	}

	allowed := []string{
		`git -c alias.st=status st --short`,
		`git -c alias.foo="$VALUE" status --short`,
		`git config --get alias.st`,
	}
	for _, command := range allowed {
		if reason := forbiddenShellCommandReason(command); reason != "" {
			t.Fatalf("safe alias command %q must be permitted, got: %s", command, reason)
		}
	}

	reason := forbiddenShellCommandReason(`git -c alias.once=status once && git once`)
	if !strings.Contains(reason, "unknown git subcommand") {
		t.Fatalf("inline alias must not leak into a later invocation, got: %s", reason)
	}
}

func TestForbiddenShellCommandReasonExpandsConfiguredGitAliases(t *testing.T) {
	repo := t.TempDir()
	runGitGuardTestCommand(t, "-C", repo, "init", "--quiet")
	runGitGuardTestCommand(t, "-C", repo, "config", "alias.wipe", "!git clean -fd")
	runGitGuardTestCommand(t, "-C", repo, "config", "alias.rewind", "reset")
	runGitGuardTestCommand(t, "-C", repo, "config", "alias.st", "status")

	for _, command := range []string{"git wipe", "git rewind --hard"} {
		if reason := forbiddenShellCommandReasonInRepo(repo, command); reason == "" {
			t.Fatalf("configured destructive alias %q must be blocked", command)
		}
	}
	if reason := forbiddenShellCommandReasonInRepo(repo, "git st --short"); reason != "" {
		t.Fatalf("configured safe alias must be permitted, got: %s", reason)
	}
	if reason := forbiddenShellCommandReasonInRepo(repo, "git reconc-command-that-does-not-exist"); !strings.Contains(reason, "unknown git subcommand") {
		t.Fatalf("unknown git subcommand must fail closed, got: %s", reason)
	}
}

func TestForbiddenShellCommandReasonIgnoresAmbientGlobalGitAlias(t *testing.T) {
	globalConfig := t.TempDir() + "/gitconfig"
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	runGitGuardTestCommand(t, "config", "--global", "alias.global-wipe", "!git reset --hard")

	reason := forbiddenShellCommandReasonInRepo(t.TempDir(), "git global-wipe")
	if !strings.Contains(reason, "unknown git subcommand") {
		t.Fatalf("ambient global alias entered inspection: %s", reason)
	}
}

func runGitGuardTestCommand(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
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
