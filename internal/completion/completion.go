// Package completion generates shell completion scripts for reconc.
// `reconc completion bash|zsh|fish` prints a ready-to-source script
// that provides tab-completion for subcommands and the most-used flags.
//
// Scripts are generated deterministically from the Subcommands table
// so adding a new subcommand in cli requires only a single table
// update to keep completion in sync.
package completion

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Subcommand is one entry in the top-level reconc command table.
// Flags lists the short-form flags (including leading --) that are
// worth completing; keep it trimmed to the most common ones rather
// than every possible switch.
type Subcommand struct {
	Name  string
	Help  string
	Flags []string
}

// Subcommands is the canonical table of all top-level reconc
// subcommands in a stable order. Keep alphabetical within categories
// so the generated completion reads naturally.
var Subcommands = []Subcommand{
	// setup & inspection
	{Name: "adopt", Help: "detect tooling and suggest rules", Flags: []string{"--yaml", "--json", "--apply"}},
	{Name: "bootstrap", Help: "init + compile + install hooks", Flags: []string{"--preset", "--force", "--skip-git-hook", "--skip-agent-hooks", "--json"}},
	{Name: "doctor", Help: "inspect discovery + validation", Flags: []string{"--deep", "--json", "--output"}},
	{Name: "extract", Help: "prose-to-rule heuristic scan", Flags: []string{"--from", "--yaml", "--json"}},
	{Name: "init", Help: "scaffold .reconc.yml and AGENTS.md", Flags: []string{"--preset", "--force", "--json", "--output"}},
	{Name: "setup", Help: "friendly alias for bootstrap", Flags: []string{"--preset", "--force", "--skip-git-hook", "--skip-agent-hooks", "--json"}},
	{Name: "status", Help: "one-line policy health summary", Flags: []string{"--json", "--output"}},
	{Name: "verify", Help: "end-to-end setup health check", Flags: []string{"--json"}},
	// compile & evaluate
	{Name: "assert", Help: "evaluate one rule by id", Flags: []string{"--var", "--read", "--write", "--command", "--claim", "--json"}},
	{Name: "can", Help: "ultra-terse yes/no decision", Flags: []string{"--why", "--json"}},
	{Name: "check", Help: "evaluate runtime evidence", Flags: []string{"--read", "--write", "--command", "--command-success", "--command-failure", "--claim", "--auto-claim", "--json", "--terse", "--output"}},
	{Name: "ci", Help: "check git diff under a CI gate", Flags: []string{"--staged", "--base", "--head", "--read", "--command", "--claim", "--auto-claim", "--json", "--output"}},
	{Name: "compile", Help: "build the policy lockfile", Flags: []string{"--json", "--strict-conflicts", "--output"}},
	{Name: "diff", Help: "compare two compiled lockfiles", Flags: []string{"--json"}},
	{Name: "done", Help: "task-finish gate", Flags: []string{"--window", "--require-clean-git", "--json"}},
	{Name: "watch", Help: "poll sources and recompile", Flags: []string{"--interval-ms"}},
	// explain & remediate
	{Name: "explain", Help: "render a check report as text / md", Flags: []string{"--read", "--write", "--command", "--claim", "--format", "--json", "--output"}},
	{Name: "fix", Help: "structured remediation plan", Flags: []string{"--read", "--write", "--command", "--claim", "--json", "--next", "--output"}},
	{Name: "next", Help: "friendly alias for fix --next", Flags: []string{"--read", "--write", "--command", "--command-success", "--command-failure", "--claim", "--json"}},
	{Name: "why", Help: "print full details of one rule", Flags: []string{"--json", "--terse"}},
	// packs & wiring
	{Name: "hook", Help: "generate / install / claim hooks", Flags: []string{"--force", "--json", "--output"}},
	{Name: "preset", Help: "list / show bundled presets", Flags: []string{"--json", "--output"}},
	{Name: "template", Help: "list / show rule templates", Flags: []string{"--json"}},
	// workflow maintenance
	{Name: "agent-intro", Help: "print embedded agent guide", Flags: []string{"--section", "--list-sections", "--json"}},
	{Name: "audit", Help: "tail / stats / export audit log", Flags: []string{"-n", "--rule", "--since", "--decision", "--json", "--compact"}},
	{Name: "changelog", Help: "rotate / list-archives", Flags: []string{"--force", "--lines", "--json"}},
	{Name: "context", Help: "token-budget size check", Flags: []string{"--limit", "--files", "--json"}},
	{Name: "coverage", Help: "minimum-percentage gate", Flags: []string{"--file", "--min-pct", "--json"}},
	{Name: "delta", Help: "audit activity since a point in time", Flags: []string{"--since", "--json"}},
	{Name: "post-task-check", Help: "pre-done gate", Flags: []string{"--window", "--require-clean-git", "--json"}},
	{Name: "session-briefing", Help: "token-efficient session start dump", Flags: []string{"--json"}},
	{Name: "spec", Help: "docs/spec.md freshness check", Flags: []string{"--file", "--max-age-days", "--json"}},
	{Name: "start", Help: "render canonical start.md", Flags: []string{"--write", "--force", "--json", "--minimal"}},
	{Name: "tui", Help: "terminal dashboard for policy state", Flags: []string{"--json", "--output"}},
	// top-level meta
	{Name: "completion", Help: "print shell completion script", Flags: []string{}},
	{Name: "manpage", Help: "emit groff man(1) page for reconc(1)", Flags: []string{}},
	{Name: "version", Help: "print the build version", Flags: []string{"--json"}},
}

// GenerateBash writes a bash completion script for reconc to w.
func GenerateBash(w io.Writer) error {
	names := subcommandNames()
	fmt.Fprintln(w, `# reconc bash completion. Source this script (or drop it into a
# directory scanned by bash-completion, e.g. /etc/bash_completion.d/ or
# /usr/local/etc/bash_completion.d/, then restart your shell).
_reconc() {
    local cur prev
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"`)
	fmt.Fprintf(w, "    local subcmds=%q\n", strings.Join(names, " "))
	fmt.Fprintln(w, `
    # First word after 'reconc' -> subcommand completion.
    if [[ ${COMP_CWORD} -eq 1 ]]; then
        COMPREPLY=($(compgen -W "${subcmds}" -- "${cur}"))
        return 0
    fi

    # Subcommand-specific flag completion.
    local sub="${COMP_WORDS[1]}"
    local flags=""
    case "${sub}" in`)
	for _, s := range Subcommands {
		if len(s.Flags) == 0 {
			continue
		}
		fmt.Fprintf(w, "        %s) flags=%q ;;\n", s.Name, strings.Join(s.Flags, " "))
	}
	fmt.Fprintln(w, `    esac

    if [[ "${cur}" == -* && -n "${flags}" ]]; then
        COMPREPLY=($(compgen -W "${flags}" -- "${cur}"))
        return 0
    fi

    # Default: complete as a path (most subcommands take [repo]).
    COMPREPLY=($(compgen -f -- "${cur}"))
}
complete -F _reconc reconc`)
	return nil
}

// GenerateZsh writes a zsh completion script to w.
func GenerateZsh(w io.Writer) error {
	fmt.Fprintln(w, `#compdef reconc
# reconc zsh completion. Drop this into a directory on $fpath (e.g.
# /usr/local/share/zsh/site-functions/_reconc) or source it directly.

_reconc() {
    local -a subcmds
    subcmds=(`)
	for _, s := range Subcommands {
		fmt.Fprintf(w, "        %q\n", s.Name+":"+s.Help)
	}
	fmt.Fprintln(w, `    )

    if (( CURRENT == 2 )); then
        _describe 'reconc subcommand' subcmds
        return
    fi

    local sub="${words[2]}"
    local -a flags
    case "${sub}" in`)
	for _, s := range Subcommands {
		if len(s.Flags) == 0 {
			continue
		}
		fmt.Fprintf(w, "        %s) flags=(%s) ;;\n", s.Name, zshFlagArray(s.Flags))
	}
	fmt.Fprintln(w, `    esac

    if [[ ${words[CURRENT]} == -* && ${#flags[@]} -gt 0 ]]; then
        _values 'flag' "${flags[@]}"
        return
    fi
    _files
}
_reconc "$@"`)
	return nil
}

func zshFlagArray(flags []string) string {
	quoted := make([]string, 0, len(flags))
	for _, f := range flags {
		quoted = append(quoted, fmt.Sprintf("%q", f))
	}
	return strings.Join(quoted, " ")
}

// GenerateFish writes a fish completion script to w.
func GenerateFish(w io.Writer) error {
	fmt.Fprintln(w, "# reconc fish completion. Drop into ~/.config/fish/completions/reconc.fish")
	fmt.Fprintln(w, "# or source directly.")
	for _, s := range Subcommands {
		fmt.Fprintf(w, "complete -c reconc -n '__fish_use_subcommand' -a %q -d %q\n", s.Name, s.Help)
	}
	for _, s := range Subcommands {
		for _, f := range s.Flags {
			long := strings.TrimPrefix(f, "--")
			short := ""
			if !strings.HasPrefix(f, "--") && strings.HasPrefix(f, "-") {
				short = strings.TrimPrefix(f, "-")
				fmt.Fprintf(w, "complete -c reconc -n '__fish_seen_subcommand_from %s' -s %s\n", s.Name, short)
			} else {
				fmt.Fprintf(w, "complete -c reconc -n '__fish_seen_subcommand_from %s' -l %s\n", s.Name, long)
			}
		}
	}
	return nil
}

func subcommandNames() []string {
	out := make([]string, 0, len(Subcommands))
	for _, s := range Subcommands {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}
