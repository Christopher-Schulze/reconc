// Package shellcommand performs bounded, AST-based discovery of commands that
// a Bash-compatible shell string can execute. It covers compound commands,
// common command wrappers, nested shell -c/eval bodies, and command/process
// substitutions without executing or expanding untrusted input.
package shellcommand

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const maxCommandBytes = 256 << 10

// IncompleteReason names why bounded analysis could not enumerate every
// command a shell string may execute. Enforcement callers fail closed on any
// reason other than IncompleteNone; the specific value lets them emit
// actionable remediation instead of a generic refusal.
type IncompleteReason string

const (
	// IncompleteNone means every executable position was resolved.
	IncompleteNone IncompleteReason = ""
	// IncompleteTooLarge means the command exceeded the analysis byte ceiling.
	IncompleteTooLarge IncompleteReason = "too_large"
	// IncompleteUnparsable means the string is not valid Bash syntax.
	IncompleteUnparsable IncompleteReason = "unparsable"
	// IncompleteDynamicCommand means an executable position is produced by
	// expansion or substitution, so the binary that would run is unknown
	// without executing untrusted input.
	IncompleteDynamicCommand IncompleteReason = "dynamic_command"
	// IncompleteNestingDepth means nested shell bodies or substitutions
	// exceeded the caller's depth budget.
	IncompleteNestingDepth IncompleteReason = "nesting_depth"
	// IncompleteAnalysisState means analysis was called with a negative depth
	// budget or the AST walk reached an inconsistent internal state, so its
	// result cannot be trusted.
	IncompleteAnalysisState IncompleteReason = "analysis_state"
)

// Invocation is one executable command position. Source retains the command
// text used for policy matching; Words starts with the effective executable
// after supported wrappers such as env, sudo, command, and exec.
type Invocation struct {
	Source       string
	Words        []string
	DynamicWords []bool
}

// CompiledExpectation is an immutable parse of one expected command. The
// runtime evaluator retains these values for one evaluation so nested
// invocation loops do not rebuild the same expected syntax tree.
type CompiledExpectation struct {
	invocations []Invocation
	complete    bool
}

// CompileExpectation parses one expected command once using the supplied
// nesting budget. A malformed, dynamic, oversized, or otherwise incomplete
// command is retained as incomplete so Match preserves the existing
// fail-closed result without reparsing it.
func CompileExpectation(command string, maxDepth int) CompiledExpectation {
	invocations, reason := InvocationsWithReason(command, maxDepth)
	return CompiledExpectation{invocations: invocations, complete: reason == IncompleteNone}
}

// Match compares one observed invocation against this precompiled expected
// command. The result is identical to Match/MatchFoldingExecutable, including
// uncertainty for incomplete or dynamic syntax.
func (e CompiledExpectation) Match(invocation Invocation, prefix, foldExecutable bool) (matched, uncertain bool) {
	if !e.complete || len(e.invocations) != 1 {
		return false, true
	}
	return matchInvocation(invocation, e.invocations[0], prefix, foldExecutable)
}

// Match reports whether invocation is the static command expected by a policy.
// Prefix mode permits additional arguments. Uncertain is true only when a
// dynamic word can occupy an expected position, allowing enforcement callers
// to fail closed without blocking unrelated commands that merely use dynamic
// arguments.
func Match(invocation Invocation, expected string, prefix bool) (matched, uncertain bool) {
	return CompileExpectation(expected, 8).Match(invocation, prefix, false)
}

// MatchFoldingExecutable is Match with a case-insensitive comparison of the
// program name only. Arguments stay case-sensitive.
//
// It exists for the deny direction. On the case-insensitive filesystems this
// product supports, `RM` and `rm` name the same program, so a matcher that
// compares the program name byte for byte lets a forbidden command through by
// changing its case. Blocking `RM` where a policy forbids `rm` is the
// fail-closed reading of the author's intent on every platform, which keeps the
// decision identical across hosts. The evidence direction must not use this:
// there, folding would accept a command the author did not name.
func MatchFoldingExecutable(invocation Invocation, expected string, prefix bool) (matched, uncertain bool) {
	return CompileExpectation(expected, 8).Match(invocation, prefix, true)
}

func matchInvocation(invocation, target Invocation, prefix bool, foldExecutable bool) (matched, uncertain bool) {
	if anyDynamic(target.DynamicWords) || len(target.Words) == 0 || len(invocation.Words) == 0 {
		return false, true
	}
	for index, word := range target.Words {
		if index >= len(invocation.Words) {
			return false, false
		}
		if index < len(invocation.DynamicWords) && invocation.DynamicWords[index] {
			return false, true
		}
		if index == 0 && executableWordMatches(invocation.Words[index], word, foldExecutable) {
			continue
		}
		if invocation.Words[index] != word {
			return false, false
		}
	}
	if prefix || len(invocation.Words) == len(target.Words) {
		return true, false
	}
	if anyDynamic(invocation.DynamicWords[len(target.Words):]) {
		return false, true
	}
	return false, false
}

func executableWordMatches(actual, expected string, fold bool) bool {
	if strings.Contains(expected, "/") {
		return equalCommandWord(actual, expected, fold)
	}
	return equalCommandWord(baseName(actual), expected, fold)
}

func equalCommandWord(actual, expected string, fold bool) bool {
	if actual == expected {
		return true
	}
	return fold && strings.EqualFold(actual, expected)
}

func anyDynamic(dynamic []bool) bool {
	for _, value := range dynamic {
		if value {
			return true
		}
	}
	return false
}

// Invocations returns direct and nested executable command positions. Complete
// is false only when analysis could not enumerate every executable position,
// allowing callers to fail closed instead of silently accepting an
// adversarially deep or dynamically dispatched command. Use
// InvocationsWithReason when the caller needs to explain the failure.
func Invocations(command string, maxDepth int) ([]Invocation, bool) {
	invocations, reason := InvocationsWithReason(command, maxDepth)
	return invocations, reason == IncompleteNone
}

// InvocationsWithReason returns the same positions as Invocations plus the
// structural reason analysis stopped short. The reason is IncompleteNone
// exactly when Invocations reports complete. When several causes occur, the
// first one reached in the fixed AST walk order wins, so the result is
// deterministic for a given input.
func InvocationsWithReason(command string, maxDepth int) ([]Invocation, IncompleteReason) {
	if maxDepth < 0 {
		// A negative budget is a caller programming error, not a property of
		// the command; report it as an analysis fault rather than blaming size.
		return nil, IncompleteAnalysisState
	}
	if len(command) > maxCommandBytes {
		return nil, IncompleteTooLarge
	}
	return invocationsAt(command, maxDepth, 0)
}

// StripTrailingRedirects removes only syntactic redirections that form the
// final suffix of a valid Bash command. Quoted and escaped redirect-looking
// arguments are ordinary words in the AST and remain untouched. The boolean
// is false when parsing or bounded analysis cannot prove the transformation.
func StripTrailingRedirects(command string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" || len(command) > maxCommandBytes {
		return command, false
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "redirect-command")
	if err != nil {
		return command, false
	}
	redirects := make([]*syntax.Redirect, 0, 2)
	syntax.Walk(file, func(node syntax.Node) bool {
		if redirect, ok := node.(*syntax.Redirect); ok {
			redirects = append(redirects, redirect)
		}
		return true
	})
	end := len(command)
	stripped := false
	for {
		candidateStart := -1
		for _, redirect := range redirects {
			start := int(redirect.Pos().Offset())
			redirectEnd := int(redirect.End().Offset())
			if start < 0 || redirectEnd < start || redirectEnd > end {
				continue
			}
			if strings.TrimSpace(command[redirectEnd:end]) == "" && start > candidateStart {
				candidateStart = start
			}
		}
		if candidateStart < 0 {
			break
		}
		end = candidateStart
		for end > 0 && (command[end-1] == ' ' || command[end-1] == '\t' || command[end-1] == '\r' || command[end-1] == '\n') {
			end--
		}
		stripped = true
	}
	if !stripped {
		return command, true
	}
	result := strings.TrimSpace(command[:end])
	if result == "" {
		return command, true
	}
	if invocations, reason := InvocationsWithReason(result, 16); reason != IncompleteNone || len(invocations) == 0 {
		return command, true
	}
	return result, true
}

func invocationsAt(command string, maxDepth, depth int) ([]Invocation, IncompleteReason) {
	if depth > maxDepth {
		return nil, IncompleteNestingDepth
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "hook-command")
	if err != nil {
		return nil, IncompleteUnparsable
	}
	result := make([]Invocation, 0, 4)
	incomplete := IncompleteNone
	// note records the first cause reached; later causes never overwrite it so
	// the reported reason stays stable across runs.
	note := func(reason IncompleteReason) {
		if incomplete == IncompleteNone {
			incomplete = reason
		}
	}
	nestingDepth := depth
	stack := make([]bool, 0, 32)
	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil {
			if len(stack) == 0 {
				note(IncompleteAnalysisState)
				return true
			}
			last := len(stack) - 1
			if stack[last] {
				nestingDepth--
			}
			stack = stack[:last]
			return true
		}
		nested := shellNestingNode(node)
		stack = append(stack, nested)
		if nested {
			nestingDepth++
			if nestingDepth > maxDepth {
				note(IncompleteNestingDepth)
				nestingDepth--
				stack = stack[:len(stack)-1]
				return false
			}
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || nestingDepth > maxDepth || len(call.Args) == 0 {
			return true
		}
		words := commandWords(call.Args)
		effective, wrapped, resolved := effectiveWords(words)
		if !resolved || len(effective) == 0 || effective[0].dynamic {
			note(IncompleteDynamicCommand)
			return true
		}
		invocation := invocationFromWords(effective)
		if !wrapped {
			invocation.Source = wordsSource(words)
		}
		result = append(result, invocation)

		nestedCommand, nestedResolved := nestedCommandString(effective)
		if !nestedResolved {
			note(IncompleteDynamicCommand)
		} else if nestedCommand != "" {
			nestedInvocations, nestedReason := invocationsAt(nestedCommand, maxDepth, nestingDepth+1)
			result = append(result, nestedInvocations...)
			note(nestedReason)
		}
		launchedCommands, launchersResolved := launcherCommands(effective)
		if !launchersResolved {
			note(IncompleteDynamicCommand)
		}
		for _, launchedWords := range launchedCommands {
			launched, launchedWrapped, launchedResolved := effectiveWords(launchedWords)
			if !launchedResolved || len(launched) == 0 || launched[0].dynamic {
				note(IncompleteDynamicCommand)
				continue
			}
			launchedInvocation := invocationFromWords(launched)
			if !launchedWrapped {
				launchedInvocation.Source = wordsSource(launchedWords)
			}
			result = append(result, launchedInvocation)
			launchedNested, launchedNestedResolved := nestedCommandString(launched)
			if !launchedNestedResolved {
				note(IncompleteDynamicCommand)
				continue
			}
			if launchedNested == "" {
				continue
			}
			nestedInvocations, nestedReason := invocationsAt(launchedNested, maxDepth, nestingDepth+1)
			result = append(result, nestedInvocations...)
			note(nestedReason)
		}
		return true
	})
	return dedupe(result), incomplete
}

type commandWord struct {
	// value is the exact static argument after the outer shell's quote removal
	// and supported escape processing. In particular, quote characters only
	// remain here when they were literal data passed to eval.
	value   string
	dynamic bool
}

func shellNestingNode(node syntax.Node) bool {
	switch node.(type) {
	case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.Subshell:
		return true
	default:
		return false
	}
}

func commandWords(words []*syntax.Word) []commandWord {
	result := make([]commandWord, 0, len(words))
	for _, word := range words {
		value, static := staticWordParts(word.Parts)
		if !static {
			value = renderedWord(word)
		}
		result = append(result, commandWord{value: value, dynamic: !static})
	}
	return result
}

func staticWordParts(parts []syntax.WordPart) (string, bool) {
	return staticWordPartsIn(parts, false)
}

// staticWordPartsIn resolves a word to the literal string the shell would pass
// to execve. Quote removal alone is not enough: outside quotes a backslash
// preserves the next character, so `\rm` and `rm` name the same program. That
// escape is the documented way to bypass an alias, agents emit it, and a
// matcher that compares the unresolved text would let it through a
// forbid_command rule.
func staticWordPartsIn(parts []syntax.WordPart, insideDoubleQuotes bool) (string, bool) {
	var value strings.Builder
	for _, part := range parts {
		switch typed := part.(type) {
		case *syntax.Lit:
			value.WriteString(unescapeShellLiteral(typed.Value, insideDoubleQuotes))
		case *syntax.SglQuoted:
			if typed.Dollar && strings.Contains(typed.Value, `\`) {
				// ANSI-C quoting ($'\x72\x6d') builds a word from escape
				// sequences this package does not decode. Report it as
				// non-static so callers fail closed instead of comparing text
				// that is not what the shell will run.
				return "", false
			}
			value.WriteString(typed.Value)
		case *syntax.DblQuoted:
			inside, static := staticWordPartsIn(typed.Parts, true)
			if !static {
				return "", false
			}
			value.WriteString(inside)
		default:
			return "", false
		}
	}
	return value.String(), true
}

// doubleQuotedEscapes are the only characters a backslash escapes inside double
// quotes. Anywhere else in a double-quoted string the backslash is literal.
const doubleQuotedEscapes = "$`\"\\\n"

func unescapeShellLiteral(value string, insideDoubleQuotes bool) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var out strings.Builder
	out.Grow(len(value))
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' || index+1 >= len(value) {
			out.WriteByte(value[index])
			continue
		}
		next := value[index+1]
		if insideDoubleQuotes && !strings.ContainsRune(doubleQuotedEscapes, rune(next)) {
			out.WriteByte(value[index])
			continue
		}
		if next == '\n' {
			// A line continuation removes both bytes.
			index++
			continue
		}
		out.WriteByte(next)
		index++
	}
	return out.String()
}

func renderedWord(word *syntax.Word) string {
	var rendered strings.Builder
	if err := syntax.NewPrinter().Print(&rendered, word); err != nil {
		return "<dynamic>"
	}
	return rendered.String()
}

func invocationFromWords(words []commandWord) Invocation {
	invocation := Invocation{
		Source:       wordsSource(words),
		Words:        make([]string, len(words)),
		DynamicWords: make([]bool, len(words)),
	}
	for index, word := range words {
		invocation.Words[index] = word.value
		invocation.DynamicWords[index] = word.dynamic
	}
	return invocation
}

func wordsSource(words []commandWord) string {
	values := make([]string, len(words))
	for index, word := range words {
		values[index] = word.value
	}
	return strings.Join(values, " ")
}

func nestedCommandString(words []commandWord) (string, bool) {
	if len(words) == 0 {
		return "", true
	}
	switch {
	case isShell(words[0].value):
		return shellCommandArgument(words[1:])
	case baseName(words[0].value) == "eval":
		return evalCommandArgument(words[1:])
	default:
		return "", true
	}
}

// evalCommandArgument implements the POSIX eval boundary: the outer shell has
// already removed syntactic quotes and resolved static escapes, then eval joins
// those resulting arguments with one space before parsing them again. Restoring
// the outer quote boundaries would change what the shell executes. Literal
// quote or backslash bytes that survive in commandWord.value remain available
// to the nested parse. Any expansion we cannot resolve stays fail-closed.
func evalCommandArgument(words []commandWord) (string, bool) {
	for _, word := range words {
		if word.dynamic {
			return "", false
		}
	}
	return wordsSource(words), true
}

// StripLineContinuations applies shell backslash-newline folding everywhere
// except single-quoted literal text.
func StripLineContinuations(command string) string {
	var out strings.Builder
	out.Grow(len(command))
	var quote byte
	escaped := false
	for index := 0; index < len(command); index++ {
		current := command[index]
		if escaped {
			out.WriteByte(current)
			escaped = false
			continue
		}
		if quote == '\'' {
			out.WriteByte(current)
			if current == '\'' {
				quote = 0
			}
			continue
		}
		if current == '\\' {
			if index+1 < len(command) && command[index+1] == '\n' {
				index++
				continue
			}
			if index+2 < len(command) && command[index+1] == '\r' && command[index+2] == '\n' {
				index += 2
				continue
			}
			out.WriteByte(current)
			escaped = true
			continue
		}
		out.WriteByte(current)
		if current == '\'' || current == '"' || current == '`' {
			if quote == current {
				quote = 0
			} else if quote == 0 {
				quote = current
			}
		}
	}
	return out.String()
}

func effectiveWords(words []commandWord) ([]commandWord, bool, bool) {
	index := 0
	wrapped := false
	for index < len(words) {
		if words[index].dynamic {
			return nil, wrapped, false
		}
		switch baseName(words[index].value) {
		case "command":
			index++
			wrapped = true
			if index < len(words) && (words[index].value == "-v" || words[index].value == "-V") {
				return words[index-1:], false, true
			}
			for index < len(words) && (words[index].value == "-p" || words[index].value == "--") {
				index++
			}
		case "builtin":
			index++
			wrapped = true
		case "exec":
			index++
			wrapped = true
			var resolved bool
			index, resolved = skipExecOptions(words, index)
			if !resolved {
				return nil, wrapped, false
			}
		case "env":
			index++
			wrapped = true
			var resolved bool
			index, resolved = skipEnvOptions(words, index)
			if !resolved {
				return nil, wrapped, false
			}
		case "sudo", "doas":
			index++
			wrapped = true
			var resolved bool
			index, resolved = skipPrivilegeOptions(words, index)
			if !resolved {
				return nil, wrapped, false
			}
		case "nohup":
			index++
			wrapped = true
			if index < len(words) && words[index].value == "--" {
				index++
			} else if index < len(words) && strings.HasPrefix(words[index].value, "-") {
				return nil, wrapped, false
			}
		case "rtk":
			index++
			wrapped = true
		case "nice":
			index++
			wrapped = true
			var resolved bool
			index, resolved = skipNiceOptions(words, index)
			if !resolved {
				return nil, wrapped, false
			}
		case "timeout":
			index++
			wrapped = true
			var resolved bool
			index, resolved = skipTimeoutOptions(words, index)
			if !resolved {
				return nil, wrapped, false
			}
		case "setsid":
			index++
			wrapped = true
			var resolved bool
			index, resolved = skipNoArgumentOptions(words, index, "-c", "--ctty", "-f", "--fork", "-w", "--wait")
			if !resolved {
				return nil, wrapped, false
			}
		case "stdbuf":
			index++
			wrapped = true
			var resolved bool
			index, resolved = skipStdbufOptions(words, index)
			if !resolved {
				return nil, wrapped, false
			}
		case "time":
			index++
			wrapped = true
			var resolved bool
			index, resolved = skipTimeOptions(words, index)
			if !resolved {
				return nil, wrapped, false
			}
		case "chroot":
			index++
			wrapped = true
			var resolved bool
			index, resolved = skipChrootOptions(words, index)
			if !resolved {
				return nil, wrapped, false
			}
		default:
			return words[index:], wrapped, true
		}
	}
	return nil, wrapped, true
}

func skipEnvOptions(words []commandWord, index int) (int, bool) {
	for index < len(words) {
		if words[index].dynamic && !dynamicAssignment(words[index]) {
			return index, false
		}
		word := words[index].value
		if isAssignment(word) {
			index++
			continue
		}
		if word == "--" {
			return index + 1, true
		}
		if word == "-S" || word == "--split-string" || strings.HasPrefix(word, "--split-string=") {
			return index, false
		}
		if word == "-u" || word == "--unset" || word == "-C" || word == "--chdir" {
			if index+1 >= len(words) || words[index+1].dynamic {
				return index, false
			}
			index += 2
			continue
		}
		if strings.HasPrefix(word, "--unset=") || strings.HasPrefix(word, "--chdir=") || word == "-i" || word == "--ignore-environment" || word == "-0" || word == "--null" || word == "-v" || word == "--debug" {
			index++
			continue
		}
		if strings.HasPrefix(word, "-") {
			return index, false
		}
		break
	}
	return index, true
}

func dynamicAssignment(word commandWord) bool {
	if !word.dynamic {
		return isAssignment(word.value)
	}
	separator := strings.IndexByte(word.value, '=')
	return separator > 0 && isAssignment(word.value[:separator]+"=x")
}

func skipPrivilegeOptions(words []commandWord, index int) (int, bool) {
	for index < len(words) {
		if words[index].dynamic {
			return index, false
		}
		word := words[index].value
		if isAssignment(word) {
			index++
			continue
		}
		if word == "--" {
			return index + 1, true
		}
		if word == "-u" || word == "-g" || word == "-h" || word == "-p" || word == "-r" || word == "-t" || word == "-C" || word == "-D" || word == "--user" || word == "--group" || word == "--host" || word == "--prompt" || word == "--role" || word == "--type" || word == "--chdir" {
			if index+1 >= len(words) || words[index+1].dynamic {
				return index, false
			}
			index += 2
			continue
		}
		if strings.HasPrefix(word, "--user=") || strings.HasPrefix(word, "--group=") || strings.HasPrefix(word, "--host=") || strings.HasPrefix(word, "--prompt=") || strings.HasPrefix(word, "--role=") || strings.HasPrefix(word, "--type=") || strings.HasPrefix(word, "--chdir=") || word == "-A" || word == "--askpass" || word == "-E" || word == "--preserve-env" || word == "-H" || word == "--set-home" || word == "-n" || word == "--non-interactive" || word == "-S" || word == "--stdin" || word == "-i" || word == "--login" {
			index++
			continue
		}
		if strings.HasPrefix(word, "-") {
			return index, false
		}
		break
	}
	return index, true
}

func skipExecOptions(words []commandWord, index int) (int, bool) {
	for index < len(words) {
		if words[index].dynamic {
			return index, false
		}
		word := words[index].value
		switch {
		case word == "--":
			return index + 1, true
		case word == "-a" || word == "--argv0":
			if index+1 >= len(words) || words[index+1].dynamic {
				return index, false
			}
			index += 2
		case strings.HasPrefix(word, "--argv0=") || word == "-c" || word == "-l" || word == "-cl" || word == "-lc":
			index++
		case strings.HasPrefix(word, "-"):
			return index, false
		default:
			return index, true
		}
	}
	return index, true
}

func skipNiceOptions(words []commandWord, index int) (int, bool) {
	for index < len(words) {
		if words[index].dynamic {
			return index, false
		}
		word := words[index].value
		switch {
		case word == "--":
			return index + 1, true
		case word == "-n" || word == "--adjustment":
			if index+1 >= len(words) || words[index+1].dynamic {
				return index, false
			}
			index += 2
		case strings.HasPrefix(word, "--adjustment=") || negativeDecimal(word):
			index++
		case strings.HasPrefix(word, "-"):
			return index, false
		default:
			return index, true
		}
	}
	return index, true
}

func negativeDecimal(word string) bool {
	if len(word) < 2 || word[0] != '-' {
		return false
	}
	for _, character := range word[1:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func skipTimeoutOptions(words []commandWord, index int) (int, bool) {
	afterOptions := false
	for index < len(words) {
		if words[index].dynamic {
			return index, false
		}
		word := words[index].value
		if afterOptions || !strings.HasPrefix(word, "-") || word == "-" {
			if index+1 >= len(words) {
				return index, false
			}
			return index + 1, true
		}
		switch {
		case word == "--":
			afterOptions = true
			index++
		case word == "-k" || word == "--kill-after" || word == "-s" || word == "--signal":
			if index+1 >= len(words) || words[index+1].dynamic {
				return index, false
			}
			index += 2
		case strings.HasPrefix(word, "--kill-after=") || strings.HasPrefix(word, "--signal=") || word == "--foreground" || word == "--preserve-status" || word == "--verbose":
			index++
		default:
			return index, false
		}
	}
	return index, false
}

func skipNoArgumentOptions(words []commandWord, index int, allowed ...string) (int, bool) {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, option := range allowed {
		allowedSet[option] = struct{}{}
	}
	for index < len(words) {
		if words[index].dynamic {
			return index, false
		}
		word := words[index].value
		if word == "--" {
			return index + 1, true
		}
		if !strings.HasPrefix(word, "-") {
			return index, true
		}
		if _, ok := allowedSet[word]; !ok {
			return index, false
		}
		index++
	}
	return index, true
}

func skipStdbufOptions(words []commandWord, index int) (int, bool) {
	for index < len(words) {
		if words[index].dynamic {
			return index, false
		}
		word := words[index].value
		if word == "--" {
			return index + 1, true
		}
		if word == "-i" || word == "-o" || word == "-e" || word == "--input" || word == "--output" || word == "--error" {
			if index+1 >= len(words) || words[index+1].dynamic {
				return index, false
			}
			index += 2
			continue
		}
		if strings.HasPrefix(word, "-i") || strings.HasPrefix(word, "-o") || strings.HasPrefix(word, "-e") || strings.HasPrefix(word, "--input=") || strings.HasPrefix(word, "--output=") || strings.HasPrefix(word, "--error=") {
			index++
			continue
		}
		if strings.HasPrefix(word, "-") {
			return index, false
		}
		return index, true
	}
	return index, true
}

func skipTimeOptions(words []commandWord, index int) (int, bool) {
	for index < len(words) {
		if words[index].dynamic {
			return index, false
		}
		word := words[index].value
		if word == "--" {
			return index + 1, true
		}
		if word == "-o" || word == "--output" || word == "-f" || word == "--format" {
			if index+1 >= len(words) || words[index+1].dynamic {
				return index, false
			}
			index += 2
			continue
		}
		if strings.HasPrefix(word, "--output=") || strings.HasPrefix(word, "--format=") || word == "-a" || word == "--append" || word == "-p" || word == "--portability" || word == "-v" || word == "--verbose" || word == "-q" || word == "--quiet" {
			index++
			continue
		}
		if strings.HasPrefix(word, "-") {
			return index, false
		}
		return index, true
	}
	return index, true
}

func skipChrootOptions(words []commandWord, index int) (int, bool) {
	for index < len(words) {
		if words[index].dynamic {
			return index, false
		}
		word := words[index].value
		if word == "--" {
			index++
			break
		}
		if word == "--userspec" || word == "--groups" || word == "--skip-chdir" {
			if word == "--skip-chdir" {
				index++
				continue
			}
			if index+1 >= len(words) || words[index+1].dynamic {
				return index, false
			}
			index += 2
			continue
		}
		if strings.HasPrefix(word, "--userspec=") || strings.HasPrefix(word, "--groups=") {
			index++
			continue
		}
		if strings.HasPrefix(word, "-") {
			return index, false
		}
		break
	}
	if index >= len(words) || words[index].dynamic || index+1 >= len(words) {
		return index, false
	}
	return index + 1, true
}

func shellCommandArgument(words []commandWord) (string, bool) {
	for index, word := range words {
		if word.dynamic {
			return "", false
		}
		if word.value == "-c" || word.value == "-lc" || (strings.HasPrefix(word.value, "-") && !strings.HasPrefix(word.value, "--") && strings.Contains(word.value, "c")) {
			if index+1 < len(words) {
				if words[index+1].dynamic {
					return "", false
				}
				return words[index+1].value, true
			}
			return "", false
		}
	}
	return "", true
}

func launcherCommands(words []commandWord) ([][]commandWord, bool) {
	if len(words) == 0 {
		return nil, true
	}
	switch baseName(words[0].value) {
	case "find":
		var commands [][]commandWord
		complete := true
		for index := 1; index < len(words); index++ {
			if words[index].dynamic {
				continue
			}
			if words[index].value != "-exec" && words[index].value != "-execdir" && words[index].value != "-ok" && words[index].value != "-okdir" {
				continue
			}
			start := index + 1
			end := start
			for end < len(words) && (words[end].dynamic || words[end].value != ";" && words[end].value != `\;` && words[end].value != "+") {
				end++
			}
			if start < end {
				commands = append(commands, words[start:end])
				for _, word := range words[start:end] {
					complete = complete && !word.dynamic
				}
			} else {
				complete = false
			}
			index = end
		}
		return commands, complete
	case "xargs":
		command, complete := xargsCommand(words[1:])
		if len(command) > 0 {
			return [][]commandWord{command}, complete
		}
		return nil, complete
	case "flock":
		command, complete := flockCommand(words[1:])
		if len(command) > 0 {
			return [][]commandWord{command}, complete
		}
		return nil, complete
	case "watch":
		command, complete := watchCommand(words[1:])
		if len(command) > 0 {
			return [][]commandWord{command}, complete
		}
		return nil, complete
	}
	return nil, true
}

func flockCommand(words []commandWord) ([]commandWord, bool) {
	index := 0
	for index < len(words) {
		if words[index].dynamic {
			return nil, false
		}
		word := words[index].value
		switch {
		case word == "--":
			index++
			goto lockTarget
		case word == "-w" || word == "--wait" || word == "-E" || word == "--conflict-exit-code":
			if index+1 >= len(words) || words[index+1].dynamic {
				return nil, false
			}
			index += 2
		case strings.HasPrefix(word, "--wait=") || strings.HasPrefix(word, "--conflict-exit-code=") || strings.HasPrefix(word, "-w") && len(word) > 2 || strings.HasPrefix(word, "-E") && len(word) > 2:
			index++
		case word == "-s" || word == "--shared" || word == "-x" || word == "--exclusive" || word == "-u" || word == "--unlock" || word == "-n" || word == "--nb" || word == "--nonblock" || word == "-o" || word == "--close" || word == "-F" || word == "--no-fork" || word == "--verbose":
			index++
		case strings.HasPrefix(word, "-"):
			return nil, false
		default:
			goto lockTarget
		}
	}

lockTarget:
	if index >= len(words) || words[index].dynamic {
		return nil, false
	}
	index++ // lock file, directory, or descriptor
	if index >= len(words) {
		return nil, true
	}
	if words[index].dynamic {
		return nil, false
	}
	if words[index].value == "-c" || words[index].value == "--command" {
		if index+1 >= len(words) || words[index+1].dynamic {
			return nil, false
		}
		return []commandWord{{value: "sh"}, {value: "-c"}, words[index+1]}, true
	}
	return words[index:], allStatic(words[index:])
}

func watchCommand(words []commandWord) ([]commandWord, bool) {
	for index := 0; index < len(words); index++ {
		if words[index].dynamic {
			return nil, false
		}
		word := words[index].value
		if word == "--" {
			return words[index+1:], allStatic(words[index+1:])
		}
		if word == "-n" || word == "--interval" || word == "-q" || word == "--equexit" || word == "--shotsdir" {
			if index+1 >= len(words) || words[index+1].dynamic {
				return nil, false
			}
			index++
			continue
		}
		if strings.HasPrefix(word, "--interval=") || strings.HasPrefix(word, "--equexit=") || strings.HasPrefix(word, "--shotsdir=") || strings.HasPrefix(word, "-n") && len(word) > 2 || strings.HasPrefix(word, "-q") && len(word) > 2 {
			continue
		}
		if word == "-b" || word == "--beep" || word == "-c" || word == "--color" || word == "-d" || word == "--differences" || word == "-e" || word == "--errexit" || word == "-g" || word == "--chgexit" || word == "-p" || word == "--precise" || word == "-t" || word == "--no-title" || word == "-w" || word == "--no-wrap" || word == "-x" || word == "--exec" {
			continue
		}
		if strings.HasPrefix(word, "-") {
			return nil, false
		}
		return words[index:], allStatic(words[index:])
	}
	return nil, true
}

func xargsCommand(words []commandWord) ([]commandWord, bool) {
	for index := 0; index < len(words); index++ {
		if words[index].dynamic {
			return nil, false
		}
		word := words[index].value
		if word == "--" {
			return words[index+1:], allStatic(words[index+1:])
		}
		if word == "-a" || word == "--arg-file" || word == "-E" || word == "--eof" || word == "-I" || word == "--replace" || word == "-L" || word == "--max-lines" || word == "-n" || word == "--max-args" || word == "-P" || word == "--max-procs" || word == "-s" || word == "--max-chars" {
			if index+1 >= len(words) || words[index+1].dynamic {
				return nil, false
			}
			index++
			continue
		}
		if strings.HasPrefix(word, "--arg-file=") || strings.HasPrefix(word, "--eof=") || strings.HasPrefix(word, "--replace=") || strings.HasPrefix(word, "--max-lines=") || strings.HasPrefix(word, "--max-args=") || strings.HasPrefix(word, "--max-procs=") || strings.HasPrefix(word, "--max-chars=") || word == "-0" || word == "--null" || word == "-r" || word == "--no-run-if-empty" || word == "-t" || word == "--verbose" || word == "-x" || word == "--exit" {
			continue
		}
		if word == "-d" || word == "--delimiter" {
			if index+1 >= len(words) || words[index+1].dynamic {
				return nil, false
			}
			index++
			continue
		}
		if strings.HasPrefix(word, "--delimiter=") {
			continue
		}
		if strings.HasPrefix(word, "-") {
			return nil, false
		}
		return words[index:], allStatic(words[index:])
	}
	return nil, true
}

func allStatic(words []commandWord) bool {
	for _, word := range words {
		if word.dynamic {
			return false
		}
	}
	return true
}

func isAssignment(word string) bool {
	separator := strings.IndexByte(word, '=')
	if separator <= 0 {
		return false
	}
	for index, current := range word[:separator] {
		if (current >= 'a' && current <= 'z') || (current >= 'A' && current <= 'Z') || current == '_' || (index > 0 && current >= '0' && current <= '9') {
			continue
		}
		return false
	}
	return true
}

func isShell(word string) bool {
	switch baseName(word) {
	case "sh", "bash", "zsh", "dash", "ksh", "fish":
		return true
	default:
		return false
	}
}

func baseName(word string) string {
	word = strings.TrimSpace(word)
	if index := strings.LastIndexByte(word, '/'); index >= 0 {
		return word[index+1:]
	}
	return word
}

func dedupe(invocations []Invocation) []Invocation {
	seen := make(map[string]struct{}, len(invocations))
	result := make([]Invocation, 0, len(invocations))
	for _, invocation := range invocations {
		var dynamicKey strings.Builder
		for _, dynamic := range invocation.DynamicWords {
			if dynamic {
				dynamicKey.WriteByte('1')
			} else {
				dynamicKey.WriteByte('0')
			}
		}
		key := invocation.Source + "\x00" + strings.Join(invocation.Words, "\x00") + "\x00" + dynamicKey.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, invocation)
	}
	return result
}
