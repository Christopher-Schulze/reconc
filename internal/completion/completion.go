// Package completion generates shell completion scripts for reconc.
// `reconc completion bash|zsh|fish` prints a ready-to-source script
// that provides tab-completion for every public command, nested command, flag,
// and enumerated value in the canonical command metadata.
//
// Scripts are generated deterministically from commandmeta so completion cannot
// drift into an independent command contract.
package completion

import (
	"fmt"
	"io"
	"strings"

	"reconc.dev/reconc/internal/commandmeta"
)

// GenerateBash writes a bash completion script for reconc to w.
func GenerateBash(w io.Writer) error {
	commands := commandmeta.All()
	fmt.Fprintln(w, `# reconc bash completion. Source this script (or drop it into a
# directory scanned by bash-completion, e.g. /etc/bash_completion.d/ or
# /usr/local/etc/bash_completion.d/, then restart your shell).
_reconc() {
    local cur prev sub nested leaf flags values
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
	prev="${COMP_WORDS[COMP_CWORD-1]}"
    sub="${COMP_WORDS[1]}"
    nested="${COMP_WORDS[2]}"
    leaf="${COMP_WORDS[3]}"`)
	fmt.Fprintf(w, "    local subcmds=%q\n", strings.Join(commandmeta.SortedNames(), " "))
	fmt.Fprintln(w, `
    # First word after 'reconc' -> subcommand completion.
    if [[ ${COMP_CWORD} -eq 1 ]]; then
        COMPREPLY=($(compgen -W "${subcmds}" -- "${cur}"))
        return 0
    fi

	# Complete a nested subcommand or a direct-mode flag.
    if [[ ${COMP_CWORD} -eq 2 ]]; then
        case "${sub}" in`)
	for _, command := range commands {
		if len(command.Subcommands) == 0 {
			continue
		}
		candidates := append(subcommandNames(command), flagNames(command.Flags)...)
		fmt.Fprintf(w, "            %s) values=%q ;;\n", command.Name, strings.Join(candidates, " "))
	}
	fmt.Fprintln(w, `        esac
        if [[ -n "${values}" ]]; then
            COMPREPLY=($(compgen -W "${values}" -- "${cur}"))
            return 0
        fi
    fi

    # Complete a third-level command such as repo sync plan.
    if [[ ${COMP_CWORD} -eq 3 ]]; then
        case "${sub}:${nested}" in`)
	for _, command := range commands {
		for _, nested := range command.Subcommands {
			if len(nested.Subcommands) == 0 {
				continue
			}
			fmt.Fprintf(w, "            %s:%s) values=%q ;;\n", command.Name, nested.Name, strings.Join(nestedSubcommandNames(nested), " "))
		}
	}
	fmt.Fprintln(w, `        esac
        if [[ -n "${values}" ]]; then
            COMPREPLY=($(compgen -W "${values}" -- "${cur}"))
            return 0
        fi
    fi

    # Exact direct or nested flag surface.
    case "${sub}:${nested}:${leaf}" in`)
	for _, command := range commands {
		for _, nested := range command.Subcommands {
			for _, leaf := range nested.Subcommands {
				if len(leaf.Flags) != 0 {
					fmt.Fprintf(w, "        %s:%s:%s) flags=%q ;;\n", command.Name, nested.Name, leaf.Name, strings.Join(flagNames(leaf.Flags), " "))
				}
			}
		}
	}
	fmt.Fprintln(w, `    esac
    if [[ -z "${flags}" ]]; then
        case "${sub}:${nested}" in`)
	for _, command := range commands {
		for _, nested := range command.Subcommands {
			if len(nested.Flags) != 0 {
				fmt.Fprintf(w, "        %s:%s) flags=%q ;;\n", command.Name, nested.Name, strings.Join(flagNames(nested.Flags), " "))
			}
		}
	}
	fmt.Fprintln(w, `        esac
    fi
    if [[ -z "${flags}" ]]; then
        case "${sub}" in`)
	for _, command := range commands {
		if len(command.Flags) != 0 {
			fmt.Fprintf(w, "            %s) flags=%q ;;\n", command.Name, strings.Join(flagNames(command.Flags), " "))
		}
	}
	fmt.Fprintln(w, `        esac
    fi

    # Enumerated flag values.
    case "${sub}:${nested}:${leaf}:${prev}" in`)
	writeBashLeafFlagValueCases(w, commands)
	fmt.Fprintln(w, `    esac
    if [[ -z "${values}" ]]; then
        case "${sub}:${nested}:${prev}" in`)
	writeBashFlagValueCases(w, commands, true)
	fmt.Fprintln(w, `        esac
    fi
    if [[ -z "${values}" ]]; then
        case "${sub}::${prev}" in`)
	writeBashFlagValueCases(w, commands, false)
	fmt.Fprintln(w, `        esac
    fi
    if [[ -n "${values}" ]]; then
        COMPREPLY=($(compgen -W "${values}" -- "${cur}"))
        return 0
    fi

    # Enumerated positional values.
    case "${sub}:${nested}:${leaf}:${COMP_CWORD}" in`)
	writeBashLeafArgumentValueCases(w, commands)
	fmt.Fprintln(w, `    esac
    if [[ -z "${values}" ]]; then
        case "${sub}:${nested}:${COMP_CWORD}" in`)
	writeBashArgumentValueCases(w, commands)
	fmt.Fprintln(w, `        esac
    fi
    if [[ -n "${values}" ]]; then
        COMPREPLY=($(compgen -W "${values}" -- "${cur}"))
        return 0
    fi

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
	commands := commandmeta.All()
	fmt.Fprintln(w, `#compdef reconc
# reconc zsh completion. Drop this into a directory on $fpath (e.g.
# /usr/local/share/zsh/site-functions/_reconc) or source it directly.

_reconc() {
    local -a subcmds flags values
    subcmds=(`)
	for _, command := range commands {
		fmt.Fprintf(w, "        %q\n", command.Name+":"+command.Summary)
	}
	fmt.Fprintln(w, `    )

    if (( CURRENT == 2 )); then
        _describe 'reconc subcommand' subcmds
        return
    fi

    local sub="${words[2]}"
	local nested="${words[3]}"
	local leaf="${words[4]}"

    if (( CURRENT == 3 )); then
        case "${sub}" in`)
	for _, command := range commands {
		if len(command.Subcommands) == 0 {
			continue
		}
		fmt.Fprintf(w, "            %s) values=(%s) ;;\n", command.Name, zshCandidates(command))
	}
	fmt.Fprintln(w, `        esac
        if (( ${#values[@]} > 0 )); then
            _describe 'reconc nested command or flag' values
            return
        fi
    fi

    if (( CURRENT == 4 )); then
        case "${sub}:${nested}" in`)
	for _, command := range commands {
		for _, nested := range command.Subcommands {
			if len(nested.Subcommands) == 0 {
				continue
			}
			fmt.Fprintf(w, "            %s:%s) values=(%s) ;;\n", command.Name, nested.Name, zshNestedCandidates(nested))
		}
	}
	fmt.Fprintln(w, `        esac
        if (( ${#values[@]} > 0 )); then
            _describe 'reconc third-level command' values
            return
        fi
    fi

    case "${sub}:${nested}:${leaf}" in`)
	for _, command := range commands {
		for _, nested := range command.Subcommands {
			for _, leaf := range nested.Subcommands {
				if len(leaf.Flags) != 0 {
					fmt.Fprintf(w, "        %s:%s:%s) flags=(%s) ;;\n", command.Name, nested.Name, leaf.Name, zshFlagArray(leaf.Flags))
				}
			}
		}
	}
	fmt.Fprintln(w, `    esac
    if (( ${#flags[@]} == 0 )); then
        case "${sub}:${nested}" in`)
	for _, command := range commands {
		for _, nested := range command.Subcommands {
			if len(nested.Flags) != 0 {
				fmt.Fprintf(w, "        %s:%s) flags=(%s) ;;\n", command.Name, nested.Name, zshFlagArray(nested.Flags))
			}
		}
	}
	fmt.Fprintln(w, `        esac
    fi
    if (( ${#flags[@]} == 0 )); then
        case "${sub}" in`)
	for _, command := range commands {
		if len(command.Flags) != 0 {
			fmt.Fprintf(w, "            %s) flags=(%s) ;;\n", command.Name, zshFlagArray(command.Flags))
		}
	}
	fmt.Fprintln(w, `        esac
    fi

    case "${sub}:${nested}:${leaf}:${words[CURRENT-1]}" in`)
	writeZshLeafFlagValueCases(w, commands)
	fmt.Fprintln(w, `    esac
    if (( ${#values[@]} == 0 )); then
        case "${sub}:${nested}:${words[CURRENT-1]}" in`)
	writeZshFlagValueCases(w, commands, true)
	fmt.Fprintln(w, `        esac
    fi
    if (( ${#values[@]} == 0 )); then
        case "${sub}::${words[CURRENT-1]}" in`)
	writeZshFlagValueCases(w, commands, false)
	fmt.Fprintln(w, `        esac
    fi
    if (( ${#values[@]} > 0 )); then
        _values 'value' "${values[@]}"
        return
    fi

    case "${sub}:${nested}:${leaf}:${CURRENT}" in`)
	writeZshLeafArgumentValueCases(w, commands)
	fmt.Fprintln(w, `    esac
    if (( ${#values[@]} == 0 )); then
        case "${sub}:${nested}:${CURRENT}" in`)
	writeZshArgumentValueCases(w, commands)
	fmt.Fprintln(w, `        esac
    fi
    if (( ${#values[@]} > 0 )); then
        _values 'value' "${values[@]}"
        return
    fi

    if [[ ${words[CURRENT]} == -* && ${#flags[@]} -gt 0 ]]; then
        _values 'flag' "${flags[@]}"
        return
    fi
    _files
}
_reconc "$@"`)
	return nil
}

// GenerateFish writes a fish completion script to w.
func GenerateFish(w io.Writer) error {
	commands := commandmeta.All()
	fmt.Fprintln(w, "# reconc fish completion. Drop into ~/.config/fish/completions/reconc.fish")
	fmt.Fprintln(w, "# or source directly.")
	for _, command := range commands {
		fmt.Fprintf(w, "complete -c reconc -f -n '__fish_use_subcommand' -a %q -d %q\n", command.Name, command.Summary)
		if len(command.Subcommands) != 0 {
			condition := fishParentCondition(command)
			for _, nested := range command.Subcommands {
				fmt.Fprintf(w, "complete -c reconc -f -n %q -a %q -d %q\n", condition, nested.Name, nested.Summary)
			}
		}
		writeFishFlags(w, fishDirectCondition(command), command.Flags)
		writeFishArguments(w, fishDirectCondition(command), 2, command.Arguments)
		for _, nested := range command.Subcommands {
			condition := fishNestedCondition(command.Name, nested.Name)
			writeFishFlags(w, condition, nested.Flags)
			writeFishArguments(w, condition, 3, nested.Arguments)
			if len(nested.Subcommands) != 0 {
				leafCondition := fishNestedParentCondition(command.Name, nested)
				for _, leaf := range nested.Subcommands {
					fmt.Fprintf(w, "complete -c reconc -f -n %q -a %q -d %q\n", leafCondition, leaf.Name, leaf.Summary)
					condition := fishLeafCondition(command.Name, nested.Name, leaf.Name)
					writeFishFlags(w, condition, leaf.Flags)
					writeFishArguments(w, condition, 4, leaf.Arguments)
				}
			}
		}
	}
	return nil
}

func subcommandNames(command commandmeta.Command) []string {
	out := make([]string, 0, len(command.Subcommands))
	for _, nested := range command.Subcommands {
		out = append(out, nested.Name)
	}
	return out
}

func nestedSubcommandNames(command commandmeta.Subcommand) []string {
	out := make([]string, 0, len(command.Subcommands))
	for _, nested := range command.Subcommands {
		out = append(out, nested.Name)
	}
	return out
}

func flagNames(flags []commandmeta.Flag) []string {
	out := make([]string, 0, len(flags))
	for _, flag := range flags {
		out = append(out, flag.Name)
	}
	return out
}

func writeBashFlagValueCases(w io.Writer, commands []commandmeta.Command, nestedMode bool) {
	for _, command := range commands {
		if nestedMode {
			for _, nested := range command.Subcommands {
				for _, flag := range nested.Flags {
					if len(flag.Values) != 0 {
						fmt.Fprintf(w, "        %s:%s:%s) values=%q ;;\n", command.Name, nested.Name, flag.Name, strings.Join(flag.Values, " "))
					}
				}
			}
			continue
		}
		for _, flag := range command.Flags {
			if len(flag.Values) != 0 {
				fmt.Fprintf(w, "            %s::%s) values=%q ;;\n", command.Name, flag.Name, strings.Join(flag.Values, " "))
			}
		}
	}
}

func writeBashArgumentValueCases(w io.Writer, commands []commandmeta.Command) {
	for _, command := range commands {
		for index, argument := range command.Arguments {
			if len(argument.Values) != 0 {
				fmt.Fprintf(w, "        %s::%d) values=%q ;;\n", command.Name, index+2, strings.Join(argument.Values, " "))
			}
		}
		for _, nested := range command.Subcommands {
			for index, argument := range nested.Arguments {
				if len(argument.Values) != 0 {
					fmt.Fprintf(w, "        %s:%s:%d) values=%q ;;\n", command.Name, nested.Name, index+3, strings.Join(argument.Values, " "))
				}
			}
		}
	}
}

func writeBashLeafFlagValueCases(w io.Writer, commands []commandmeta.Command) {
	for _, command := range commands {
		for _, nested := range command.Subcommands {
			for _, leaf := range nested.Subcommands {
				for _, flag := range leaf.Flags {
					if len(flag.Values) != 0 {
						fmt.Fprintf(w, "        %s:%s:%s:%s) values=%q ;;\n", command.Name, nested.Name, leaf.Name, flag.Name, strings.Join(flag.Values, " "))
					}
				}
			}
		}
	}
}

func writeBashLeafArgumentValueCases(w io.Writer, commands []commandmeta.Command) {
	for _, command := range commands {
		for _, nested := range command.Subcommands {
			for _, leaf := range nested.Subcommands {
				for index, argument := range leaf.Arguments {
					if len(argument.Values) != 0 {
						fmt.Fprintf(w, "        %s:%s:%s:%d) values=%q ;;\n", command.Name, nested.Name, leaf.Name, index+4, strings.Join(argument.Values, " "))
					}
				}
			}
		}
	}
}

func zshCandidates(command commandmeta.Command) string {
	values := make([]string, 0, len(command.Subcommands)+len(command.Flags))
	for _, nested := range command.Subcommands {
		values = append(values, fmt.Sprintf("%q", nested.Name+":"+nested.Summary))
	}
	for _, flag := range command.Flags {
		values = append(values, fmt.Sprintf("%q", flag.Name))
	}
	return strings.Join(values, " ")
}

func zshNestedCandidates(command commandmeta.Subcommand) string {
	values := make([]string, 0, len(command.Subcommands)+len(command.Flags))
	for _, nested := range command.Subcommands {
		values = append(values, fmt.Sprintf("%q", nested.Name+":"+nested.Summary))
	}
	for _, flag := range command.Flags {
		values = append(values, fmt.Sprintf("%q", flag.Name))
	}
	return strings.Join(values, " ")
}

func zshFlagArray(flags []commandmeta.Flag) string {
	quoted := make([]string, 0, len(flags))
	for _, flag := range flags {
		quoted = append(quoted, fmt.Sprintf("%q", flag.Name))
	}
	return strings.Join(quoted, " ")
}

func writeZshFlagValueCases(w io.Writer, commands []commandmeta.Command, nestedMode bool) {
	for _, command := range commands {
		if nestedMode {
			for _, nested := range command.Subcommands {
				for _, flag := range nested.Flags {
					if len(flag.Values) != 0 {
						fmt.Fprintf(w, "        %s:%s:%s) values=(%s) ;;\n", command.Name, nested.Name, flag.Name, zshValues(flag.Values))
					}
				}
			}
			continue
		}
		for _, flag := range command.Flags {
			if len(flag.Values) != 0 {
				fmt.Fprintf(w, "            %s::%s) values=(%s) ;;\n", command.Name, flag.Name, zshValues(flag.Values))
			}
		}
	}
}

func writeZshArgumentValueCases(w io.Writer, commands []commandmeta.Command) {
	for _, command := range commands {
		for index, argument := range command.Arguments {
			if len(argument.Values) != 0 {
				fmt.Fprintf(w, "        %s::%d) values=(%s) ;;\n", command.Name, index+3, zshValues(argument.Values))
			}
		}
		for _, nested := range command.Subcommands {
			for index, argument := range nested.Arguments {
				if len(argument.Values) != 0 {
					fmt.Fprintf(w, "        %s:%s:%d) values=(%s) ;;\n", command.Name, nested.Name, index+4, zshValues(argument.Values))
				}
			}
		}
	}
}

func writeZshLeafFlagValueCases(w io.Writer, commands []commandmeta.Command) {
	for _, command := range commands {
		for _, nested := range command.Subcommands {
			for _, leaf := range nested.Subcommands {
				for _, flag := range leaf.Flags {
					if len(flag.Values) != 0 {
						fmt.Fprintf(w, "        %s:%s:%s:%s) values=(%s) ;;\n", command.Name, nested.Name, leaf.Name, flag.Name, zshValues(flag.Values))
					}
				}
			}
		}
	}
}

func writeZshLeafArgumentValueCases(w io.Writer, commands []commandmeta.Command) {
	for _, command := range commands {
		for _, nested := range command.Subcommands {
			for _, leaf := range nested.Subcommands {
				for index, argument := range leaf.Arguments {
					if len(argument.Values) != 0 {
						fmt.Fprintf(w, "        %s:%s:%s:%d) values=(%s) ;;\n", command.Name, nested.Name, leaf.Name, index+5, zshValues(argument.Values))
					}
				}
			}
		}
	}
}

func zshValues(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return strings.Join(quoted, " ")
}

func fishParentCondition(command commandmeta.Command) string {
	names := strings.Join(subcommandNames(command), " ")
	return fmt.Sprintf("__fish_seen_subcommand_from %s; and not __fish_seen_subcommand_from %s", command.Name, names)
}

func fishDirectCondition(command commandmeta.Command) string {
	if len(command.Subcommands) == 0 {
		return "__fish_seen_subcommand_from " + command.Name
	}
	return fishParentCondition(command)
}

func fishNestedCondition(command, nested string) string {
	return fmt.Sprintf("__fish_seen_subcommand_from %s; and __fish_seen_subcommand_from %s", command, nested)
}

func fishNestedParentCondition(command string, nested commandmeta.Subcommand) string {
	return fmt.Sprintf(
		"__fish_seen_subcommand_from %s; and __fish_seen_subcommand_from %s; and not __fish_seen_subcommand_from %s",
		command,
		nested.Name,
		strings.Join(nestedSubcommandNames(nested), " "),
	)
}

func fishLeafCondition(command, nested, leaf string) string {
	return fmt.Sprintf(
		"__fish_seen_subcommand_from %s; and __fish_seen_subcommand_from %s; and __fish_seen_subcommand_from %s",
		command,
		nested,
		leaf,
	)
}

func writeFishFlags(w io.Writer, condition string, flags []commandmeta.Flag) {
	for _, flag := range flags {
		option := "-l " + strings.TrimPrefix(flag.Name, "--")
		if strings.HasPrefix(flag.Name, "-") && !strings.HasPrefix(flag.Name, "--") {
			option = "-s " + strings.TrimPrefix(flag.Name, "-")
		}
		if flag.Value != "" {
			option += " -r"
		}
		if len(flag.Values) != 0 {
			option += " -a " + fmt.Sprintf("%q", strings.Join(flag.Values, " "))
		}
		fmt.Fprintf(w, "complete -c reconc -f -n %q %s\n", condition, option)
	}
}

func writeFishArguments(w io.Writer, condition string, precedingWords int, arguments []commandmeta.Argument) {
	for index, argument := range arguments {
		if len(argument.Values) == 0 {
			continue
		}
		position := precedingWords + index
		positionalCondition := fmt.Sprintf("%s; and test (count (commandline -opc)) -eq %d", condition, position)
		fmt.Fprintf(w, "complete -c reconc -f -n %q -a %q\n", positionalCondition, strings.Join(argument.Values, " "))
	}
}
