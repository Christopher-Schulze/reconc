// Package manpage renders a roff-formatted man(1) page for reconc.
// Invoked via `reconc manpage`. It consumes the same dependency-neutral command
// metadata as root help and shell completion.
package manpage

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/commandmeta"
	"reconc.dev/reconc/internal/schema"
)

// Render writes a groff man(1) page for reconc to w. The version is
// stamped into the header so `man reconc` on an installed system
// shows which build the docs belong to.
func Render(w io.Writer, version string) error {
	date, err := sourceDate()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, ".TH RECONC 1 %q %q %q\n", date, "reconc "+version, "User Commands")

	fmt.Fprintln(w, ".SH NAME")
	fmt.Fprintln(w, "reconc \\- Repository Control Compiler")

	fmt.Fprintln(w, ".SH SYNOPSIS")
	fmt.Fprintln(w, ".B reconc")
	fmt.Fprintln(w, "[\\fIflags\\fR] \\fIsubcommand\\fR [\\fIargs\\fR...]")

	fmt.Fprintln(w, ".SH DESCRIPTION")
	fmt.Fprintln(w, `Compiles repository policy from AGENTS.md / CLAUDE.md / .reconc.yml and
related YAML sources into a deterministic policy lockfile
(\fB.reconc/policy.lock.json\fR), then evaluates your proposed actions
(reads, writes, commands, claims) against that lockfile. One Go binary,
zero runtime dependencies, offline by default. Designed to make AI
coding agents' behaviour auditable and gate-able rather than hopeful.`)

	fmt.Fprintln(w, ".SH EXIT STATUS")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B 0")
	fmt.Fprintln(w, "Pass or warn. Non-blocking decision.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B 1")
	fmt.Fprintln(w, "Runtime or input error. The tool itself is unhappy.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B 2")
	fmt.Fprintln(w, "At least one blocking policy violation. The action is forbidden.")

	fmt.Fprintln(w, ".SH SUBCOMMANDS")
	for _, command := range commandmeta.Public() {
		fmt.Fprintln(w, ".TP")
		fmt.Fprintf(w, ".B %s\n", escapeRoff(command.Name))
		fmt.Fprintf(w, "%s\n", escapeRoff(command.Summary))
		fmt.Fprintf(w, "Synopsis: \\fB%s\\fR\n", escapeRoff(command.Synopsis))
		for _, nested := range command.Subcommands {
			fmt.Fprintln(w, ".RS")
			fmt.Fprintln(w, ".TP")
			fmt.Fprintf(w, ".B %s %s\n", escapeRoff(command.Name), escapeRoff(nested.Name))
			fmt.Fprintf(w, "%s\n", escapeRoff(nested.Summary))
			fmt.Fprintf(w, "Synopsis: \\fB%s\\fR\n", escapeRoff(nested.Synopsis))
			for _, leaf := range nested.Subcommands {
				fmt.Fprintln(w, ".RS")
				fmt.Fprintln(w, ".TP")
				fmt.Fprintf(w, ".B %s %s %s\n", escapeRoff(command.Name), escapeRoff(nested.Name), escapeRoff(leaf.Name))
				fmt.Fprintf(w, "%s\n", escapeRoff(leaf.Summary))
				fmt.Fprintf(w, "Synopsis: \\fB%s\\fR\n", escapeRoff(leaf.Synopsis))
				fmt.Fprintln(w, ".RE")
			}
			fmt.Fprintln(w, ".RE")
		}
	}

	fmt.Fprintln(w, ".SH ENVIRONMENT")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B RECONC_HOME")
	fmt.Fprintln(w, "User config + presets + templates root. Default: \\fI~/.reconc\\fR.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B RECONC_AUDIT")
	fmt.Fprintln(w, "Set to \\fB1\\fR to enable the append-only decision log at \\fI.reconc/audit.jsonl\\fR. Off by default.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B RECONC_AUDIT_VERBOSE")
	fmt.Fprintln(w, "Set to \\fB1\\fR to store full command strings in audit records instead of the redacted first token. May capture secrets passed as arguments.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B NO_COLOR")
	fmt.Fprintln(w, "Disable ANSI styling in human-readable terminal output. JSON, files, and pipes are always plain.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B RECONC_CLAUDE_STATE_DIR")
	fmt.Fprintln(w, "Override the global agent-session state root.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B RECONC_SCHEMA_BASE_URL")
	fmt.Fprintf(w, "Enterprise override for schema URLs stamped on lockfiles, check reports, and fix plans. Without an override, v1 contracts use \\fI%s\\fR and current policy locks use \\fI%s\\fR.\n", schema.DefaultBaseURL, schema.PolicyLockBaseURL)
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B RECONC_STOP_FINGERPRINT_UNTRACKED")
	fmt.Fprintln(w, "Untracked-file mode for the Stop fingerprint's git status snapshot: \\fBnormal\\fR (default), \\fBall\\fR, or \\fBno\\fR.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B RECONC_GROK_STEER")
	fmt.Fprintln(w, "Set to \\fB0\\fR to disable Grok leader-mode TUI Stop steering over the local Unix socket or Windows named pipe.")

	fmt.Fprintln(w, ".SH FILES")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".I .reconc.yml")
	fmt.Fprintln(w, "Per-repo policy config. Can extend presets and include rule definitions.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".I .reconc/policy.lock.json")
	fmt.Fprintln(w, "Portable compiled policy contract. Commit and review it with policy-source changes.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".I .reconc/install.lock.json")
	fmt.Fprintln(w, "Portable repository ownership receipt used by digest-bound repository synchronization and removal.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".I AGENTS.md")
	fmt.Fprintln(w, "Free-form agent guidance. Inline \\fBreconc\\fR-fenced YAML blocks are picked up by the compiler.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".I .reconc/audit.jsonl")
	fmt.Fprintln(w, "Opt-in decision log. See \\fBRECONC_AUDIT\\fR.")

	fmt.Fprintln(w, ".SH SEE ALSO")
	fmt.Fprintln(w, ".BR reconc (1),")
	fmt.Fprintln(w, "\\fBreconc agent-intro\\fR for the embedded integration guide,")
	fmt.Fprintln(w, "\\fBreconc help\\fR for the full command inventory.")

	fmt.Fprintln(w, ".SH BUGS")
	fmt.Fprintln(w, "Report at https://github.com/Christopher-Schulze/reconc/issues")

	return nil
}

func sourceDate() (string, error) {
	raw := strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH"))
	if raw == "" {
		return time.Now().UTC().Format("2006-01-02"), nil
	}
	epoch, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || epoch < 0 {
		return "", fmt.Errorf("invalid SOURCE_DATE_EPOCH %q", raw)
	}
	return time.Unix(epoch, 0).UTC().Format("2006-01-02"), nil
}

// escapeRoff escapes the groff metacharacters. Limited to backslash
// and hyphens at word-start since we control the input strings
// tightly (from the canonical command metadata).
func escapeRoff(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	// Leading hyphens are treated specially by some man viewers.
	if strings.HasPrefix(s, "-") {
		s = `\` + s
	}
	return s
}
