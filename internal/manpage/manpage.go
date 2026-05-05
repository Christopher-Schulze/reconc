// Package manpage renders a roff-formatted man(1) page for reconc.
// Invoked via `reconc manpage`. Uses the same Subcommands table as
// the shell-completion generator so adding a new subcommand in cli
// automatically flows into both the completion scripts AND the man
// page with no extra work.
package manpage

import (
	"fmt"
	"io"
	"strings"
	"time"

	"reconc.dev/reconc/internal/completion"
)

// Render writes a groff man(1) page for reconc to w. The version is
// stamped into the header so `man reconc` on an installed system
// shows which build the docs belong to.
func Render(w io.Writer, version string) error {
	date := time.Now().UTC().Format("2006-01-02")
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
	for _, s := range completion.Subcommands {
		fmt.Fprintln(w, ".TP")
		fmt.Fprintf(w, ".B %s\n", escapeRoff(s.Name))
		fmt.Fprintf(w, "%s\n", escapeRoff(s.Help))
	}

	fmt.Fprintln(w, ".SH ENVIRONMENT")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B RECONC_HOME")
	fmt.Fprintln(w, "User config + presets + templates root. Default: \\fI~/.reconc\\fR.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B RECONC_AUDIT")
	fmt.Fprintln(w, "Set to \\fB1\\fR to enable the append-only decision log at \\fI.reconc/audit.jsonl\\fR. Off by default.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".B RECONC_SCHEMA_BASE_URL")
	fmt.Fprintln(w, "Enterprise override for schema URLs stamped on lockfiles, check reports, and fix plans. Default: \\fIhttps://reconc.dev\\fR.")

	fmt.Fprintln(w, ".SH FILES")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".I .reconc.yml")
	fmt.Fprintln(w, "Per-repo policy config. Can extend presets and include rule definitions.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, ".I .reconc/policy.lock.json")
	fmt.Fprintln(w, "Compiled lockfile. Byte-stable; safe to commit.")
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

// escapeRoff escapes the groff metacharacters. Limited to backslash
// and hyphens at word-start since we control the input strings
// tightly (from the Subcommands table).
func escapeRoff(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	// Leading hyphens are treated specially by some man viewers.
	if strings.HasPrefix(s, "-") {
		s = `\` + s
	}
	return s
}
