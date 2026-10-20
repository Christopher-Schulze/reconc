package shellcommand

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// WriteTargets lists the files a Bash command writes through literal
// targets: output redirections, `tee`, `cp`/`mv`/`install`/`ln`
// destinations (and `mv` sources, which disappear), `touch`, `truncate`,
// `rm`, and `sed -i` operands, including nested shell bodies. Complete is
// false when a target of a known writer is not a literal or the command
// cannot be analyzed; callers keep enforcing those writes after execution.
// ChangesDirectory reports a `cd`, `pushd` or `popd`, after which a relative
// target no longer names a path relative to the starting directory.
type WriteTargets struct {
	Paths            []string
	Complete         bool
	ChangesDirectory bool
}

// LiteralWriteTargets analyzes command within the nesting budget.
func LiteralWriteTargets(command string, maxDepth int) WriteTargets {
	targets := WriteTargets{Complete: true}
	if len(command) > maxCommandBytes || maxDepth < 0 {
		targets.Complete = false
		return targets
	}
	state := newParserState()
	collectWriteTargets(&state, command, maxDepth, &targets)
	targets.Paths = dedupeStrings(targets.Paths)
	return targets
}

func collectWriteTargets(state *parserState, command string, depth int, targets *WriteTargets) {
	if depth < 0 {
		targets.Complete = false
		return
	}
	file, err := state.parse(command, "write-targets")
	if err != nil {
		targets.Complete = false
		return
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch typed := node.(type) {
		case *syntax.Redirect:
			collectRedirectTarget(typed, targets)
		case *syntax.CallExpr:
			if len(typed.Args) == 0 {
				return true
			}
			effective, _, resolved := effectiveWords(commandWords(typed.Args))
			if !resolved || len(effective) == 0 || effective[0].dynamic {
				return true
			}
			collectWriterOperands(effective, targets)
			if nested, ok := nestedCommandString(effective); !ok {
				targets.Complete = false
			} else if nested != "" {
				collectWriteTargets(state, nested, depth-1, targets)
			}
		}
		return true
	})
}

func collectRedirectTarget(redirect *syntax.Redirect, targets *WriteTargets) {
	if !redirectWritesFile(redirect) {
		return
	}
	target, static := "", false
	if redirect.Word != nil {
		target, static = staticWordParts(redirect.Word.Parts)
	}
	if !static || target == "" {
		targets.Complete = false
		return
	}
	targets.Paths = append(targets.Paths, target)
}

func collectWriterOperands(words []commandWord, targets *WriteTargets) {
	switch baseName(words[0].value) {
	case "cd", "pushd", "popd":
		targets.ChangesDirectory = true
	case "tee", "touch", "truncate", "rm":
		addOperands(operandWords(words[1:]), targets)
	case "cp", "install", "ln":
		addDestinations(words[1:], targets)
	case "mv":
		operands := addDestinations(words[1:], targets)
		addOperands(operands, targets)
	case "sed":
		collectSedInPlaceFiles(words[1:], targets)
	}
}

// operandWords drops options; after `--` every word is an operand.
func operandWords(words []commandWord) []commandWord {
	operands := []commandWord{}
	for index, word := range words {
		if !word.dynamic && word.value == "--" {
			return append(operands, words[index+1:]...)
		}
		if word.dynamic || !strings.HasPrefix(word.value, "-") || word.value == "-" {
			operands = append(operands, word)
		}
	}
	return operands
}

func addOperands(operands []commandWord, targets *WriteTargets) {
	for _, operand := range operands {
		if operand.dynamic {
			targets.Complete = false
			continue
		}
		if operand.value != "-" && operand.value != "/dev/null" {
			targets.Paths = append(targets.Paths, operand.value)
		}
	}
}

// addDestinations records the destination of a copy-like command: the
// `-t`/`--target-directory` directory joined with each source name, or the
// last operand itself and, when it may be a directory, joined with each
// source name. It returns the source operands.
func addDestinations(words []commandWord, targets *WriteTargets) []commandWord {
	directory, hasDirectory := "", false
	filtered := make([]commandWord, 0, len(words))
	for index := 0; index < len(words); index++ {
		word := words[index]
		switch {
		case !word.dynamic && (word.value == "-t" || word.value == "--target-directory"):
			if index+1 >= len(words) || words[index+1].dynamic {
				targets.Complete = false
				return nil
			}
			directory, hasDirectory = words[index+1].value, true
			index++
		case !word.dynamic && strings.HasPrefix(word.value, "--target-directory="):
			directory, hasDirectory = strings.TrimPrefix(word.value, "--target-directory="), true
		case !word.dynamic && strings.HasPrefix(word.value, "-t") && len(word.value) > 2 && !strings.HasPrefix(word.value, "--"):
			directory, hasDirectory = word.value[2:], true
		default:
			filtered = append(filtered, word)
		}
	}
	operands := operandWords(filtered)
	if !hasDirectory {
		if len(operands) < 2 {
			return operands
		}
		destination := operands[len(operands)-1]
		operands = operands[:len(operands)-1]
		if destination.dynamic {
			targets.Complete = false
			return operands
		}
		directory = destination.value
		targets.Paths = append(targets.Paths, directory)
	}
	for _, source := range operands {
		if source.dynamic {
			targets.Complete = false
			continue
		}
		targets.Paths = append(targets.Paths, strings.TrimSuffix(directory, "/")+"/"+baseName(strings.TrimSuffix(source.value, "/")))
	}
	return operands
}

// collectSedInPlaceFiles records the files `sed -i` rewrites: every operand
// after the script, or every operand when -e/-f supplies the script.
func collectSedInPlaceFiles(words []commandWord, targets *WriteTargets) {
	inPlace, scriptGiven := false, false
	operands := []commandWord{}
	for index := 0; index < len(words); index++ {
		word := words[index]
		switch {
		case word.dynamic || !strings.HasPrefix(word.value, "-") || word.value == "-":
			operands = append(operands, word)
		case word.value == "--":
			operands = append(operands, words[index+1:]...)
			index = len(words)
		case word.value == "-i" || strings.HasPrefix(word.value, "-i") || strings.HasPrefix(word.value, "--in-place"):
			inPlace = true
			// BSD sed takes the backup suffix as a separate, possibly empty word.
			if word.value == "-i" && index+1 < len(words) && !words[index+1].dynamic && words[index+1].value == "" {
				index++
			}
		case word.value == "-e" || word.value == "-f" || word.value == "--expression" || word.value == "--file":
			scriptGiven = true
			index++
		case strings.HasPrefix(word.value, "--expression=") || strings.HasPrefix(word.value, "--file="):
			scriptGiven = true
		}
	}
	if !inPlace {
		return
	}
	if !scriptGiven && len(operands) > 0 {
		operands = operands[1:]
	}
	addOperands(operands, targets)
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
