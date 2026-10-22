package shellcommand

import (
	"slices"
	"strings"
	"unicode"
)

// PowerShell analysis translates a PowerShell command line into the Bash
// dialect this package analyzes, so one enumeration and one matcher serve both
// shells. Every command position becomes a Bash command with the same static
// words, the executable normalized for Windows (`\` as `/`, without `.exe`,
// `.cmd`, `.bat`, `.com` or `.ps1`). A value PowerShell computes at run time
// becomes a dynamic word. Code that PowerShell runs in place, later or
// elsewhere (subexpressions, grouped pipelines, script blocks, the right side
// of an assignment) is analyzed as a nested command substitution. Every form
// whose executed command cannot be enumerated without running PowerShell
// (Invoke-Expression, Invoke-Command, a call operator on a computed target,
// method invocation, aliases, encoded commands, unparsable input) becomes a
// dynamic command, which callers treat as incomplete analysis.

const (
	powerShellDynamicCommand = `"$POWERSHELL_DYNAMIC_COMMAND"`
	powerShellValue          = `$POWERSHELL_VALUE`
	maxPowerShellDepth       = 16
)

// powerShellDynamicExecution names the cmdlets and aliases that run text,
// script, or a remote or scheduled command Reconc cannot enumerate, or that
// rebind command names for later statements.
var powerShellDynamicExecution = map[string]struct{}{
	"invoke-expression": {}, "iex": {},
	"invoke-command": {}, "icm": {},
	"invoke-item": {}, "ii": {},
	"set-alias": {}, "sal": {}, "new-alias": {}, "nal": {}, "import-alias": {}, "ipal": {},
	"invoke-wmimethod": {}, "invoke-cimmethod": {},
	"register-scheduledtask": {}, "register-scheduledjob": {},
	"new-pssession": {}, "enter-pssession": {}, "etsn": {},
}

// powerShellKeywords open statements whose blocks and conditions are analyzed
// in place; none of them is a command.
var powerShellKeywords = map[string]struct{}{
	"begin": {}, "catch": {}, "class": {}, "clean": {}, "configuration": {}, "data": {},
	"do": {}, "dynamicparam": {}, "else": {}, "elseif": {}, "end": {}, "enum": {},
	"filter": {}, "finally": {}, "for": {}, "foreach": {}, "function": {}, "if": {},
	"param": {}, "process": {}, "switch": {}, "trap": {}, "try": {}, "until": {},
	"using": {}, "while": {}, "workflow": {},
}

// PowerShellAnalysisCommand returns the Bash-dialect command whose analysis
// enumerates the invocations of the PowerShell command line command.
func PowerShellAnalysisCommand(command string) string {
	if len(command) > maxCommandBytes {
		return powerShellDynamicCommand
	}
	parser := &psParser{src: []rune(command)}
	program, ok := parser.statements(0, 0)
	if !ok || !parser.eof() {
		return powerShellDynamicCommand
	}
	// Text the Bash parser rejects, such as some control characters inside a
	// quoted word, is a dynamic command like any other unanalyzable input.
	state := newParserState()
	if _, err := state.parse(program, "powershell-analysis"); err != nil {
		return powerShellDynamicCommand
	}
	return program
}

type psParser struct {
	src []rune
	pos int
}

// psWord is one argument value. A dynamic word carries the translated
// programs that PowerShell executes while computing it.
type psWord struct {
	text    string
	dynamic bool
	nested  []string
}

// psArgument is one command argument; a comma list has several elements,
// which a native command receives as separate arguments.
type psArgument []psWord

func flattenArguments(args []psArgument) []psWord {
	words := []psWord{}
	for _, arg := range args {
		words = append(words, arg...)
	}
	return words
}

func (p *psParser) eof() bool { return p.pos >= len(p.src) }

func (p *psParser) peek(offset int) rune {
	if p.pos+offset >= len(p.src) || p.pos+offset < 0 {
		return 0
	}
	return p.src[p.pos+offset]
}

// skipSpace skips blanks, line continuations and comments, but not newlines.
func (p *psParser) skipSpace() bool {
	for !p.eof() {
		current := p.peek(0)
		switch {
		case current == ' ' || current == '\t' || current == '\r' || current == '\f' || current == '\v' || current == 0xa0:
			p.pos++
		case current == '`' && (p.peek(1) == '\n' || p.peek(1) == '\r' && p.peek(2) == '\n'):
			p.pos += 2
			if p.peek(-1) == '\r' {
				p.pos++
			}
		case current == '<' && p.peek(1) == '#':
			end := indexRunes(p.src, p.pos+2, "#>")
			if end < 0 {
				return false
			}
			p.pos = end + 2
		case current == '#':
			for !p.eof() && p.peek(0) != '\n' {
				p.pos++
			}
		default:
			return true
		}
	}
	return true
}

func (p *psParser) skipSeparators() bool {
	for {
		if !p.skipSpace() {
			return false
		}
		if current := p.peek(0); current == '\n' || current == ';' {
			p.pos++
			continue
		}
		return true
	}
}

// statements parses a statement list up to closer (0 for the end of input)
// and leaves closer unconsumed.
func (p *psParser) statements(closer rune, depth int) (string, bool) {
	if depth > maxPowerShellDepth {
		return "", false
	}
	rendered := []string{}
	for {
		if !p.skipSeparators() {
			return "", false
		}
		if p.eof() {
			if closer != 0 {
				return "", false
			}
			break
		}
		if p.peek(0) == closer {
			break
		}
		chain, ok := p.chain(closer, depth)
		if !ok {
			return "", false
		}
		rendered = append(rendered, chain)
		if !p.skipSpace() {
			return "", false
		}
		switch current := p.peek(0); {
		case p.eof() || current == closer:
		case current == '\n' || current == ';':
			p.pos++
		case current == '&' && p.peek(1) != '&':
			// A trailing `&` runs the pipeline as a background job.
			p.pos++
		default:
			return "", false
		}
	}
	if len(rendered) == 0 {
		return ":", true
	}
	return strings.Join(rendered, "; "), true
}

func (p *psParser) chain(closer rune, depth int) (string, bool) {
	rendered, ok := p.pipeline(closer, depth)
	if !ok {
		return "", false
	}
	for {
		if !p.skipSpace() {
			return "", false
		}
		operator := ""
		switch {
		case p.peek(0) == '&' && p.peek(1) == '&':
			operator = " && "
		case p.peek(0) == '|' && p.peek(1) == '|':
			operator = " || "
		default:
			return rendered, true
		}
		p.pos += 2
		if !p.skipSeparatorsInline() {
			return "", false
		}
		next, ok := p.pipeline(closer, depth)
		if !ok {
			return "", false
		}
		rendered += operator + next
	}
}

// skipSeparatorsInline skips blanks and newlines after a binary operator,
// where PowerShell continues the statement on the next line.
func (p *psParser) skipSeparatorsInline() bool {
	for {
		if !p.skipSpace() {
			return false
		}
		if p.peek(0) != '\n' {
			return true
		}
		p.pos++
	}
}

func (p *psParser) pipeline(closer rune, depth int) (string, bool) {
	elements := []string{}
	for {
		if !p.skipSpace() {
			return "", false
		}
		element, ok := p.element(closer, depth)
		if !ok {
			return "", false
		}
		elements = append(elements, element)
		if !p.skipSpace() {
			return "", false
		}
		if p.peek(0) != '|' || p.peek(1) == '|' {
			return strings.Join(elements, " | "), true
		}
		p.pos++
		if !p.skipSeparatorsInline() {
			return "", false
		}
	}
}

func (p *psParser) atElementEnd(closer rune) bool {
	current := p.peek(0)
	return p.eof() || current == '\n' || current == ';' || current == '|' || current == '&' ||
		current == closer || current == ')' || current == '}'
}

func (p *psParser) element(closer rune, depth int) (string, bool) {
	current := p.peek(0)
	switch {
	case current == '&' && p.peek(1) != '&':
		p.pos++
		return p.callOperator(closer, depth)
	case p.atElementEnd(closer):
		return "", false
	case current == '.' && (isPowerShellSpace(p.peek(1)) || isQuote(p.peek(1)) || p.peek(1) == '{' || p.peek(1) == '$' || p.peek(1) == '('):
		// Dot-sourcing runs its target in the current scope.
		p.pos++
		return p.callOperator(closer, depth)
	case isCommandStart(current):
		start := p.pos
		name, ok := p.bareword()
		if !ok {
			return "", false
		}
		lower := strings.ToLower(name.text)
		if !name.dynamic && isPowerShellNumber(lower) {
			p.pos = start
			return p.expression(closer, depth)
		}
		if !name.dynamic {
			if _, keyword := powerShellKeywords[lower]; keyword {
				return p.expression(closer, depth)
			}
			switch lower {
			case "return", "throw":
				if !p.skipSpace() {
					return "", false
				}
				if p.atElementEnd(closer) {
					return ":", true
				}
				return p.element(closer, depth)
			case "exit", "break", "continue":
				return p.expression(closer, depth)
			}
		}
		return p.command(name, closer, depth)
	default:
		return p.expression(closer, depth)
	}
}

// callOperator parses the target and arguments after `&` or dot-sourcing.
func (p *psParser) callOperator(closer rune, depth int) (string, bool) {
	if !p.skipSpace() {
		return "", false
	}
	switch current := p.peek(0); {
	case current == '{':
		block, ok := p.group('{', '}', depth)
		if !ok {
			return "", false
		}
		args, redirects, ok := p.arguments(closer, depth)
		if !ok {
			return "", false
		}
		return renderPowerShellCommand(":", append([]psWord{{dynamic: true, nested: []string{block}}}, flattenArguments(args)...), redirects), true
	case isQuote(current) || isCommandStart(current):
		target, ok := p.primary(depth)
		if !ok {
			return "", false
		}
		return p.command(target, closer, depth)
	default:
		target, ok := p.primary(depth)
		if !ok {
			return "", false
		}
		target.dynamic = true
		return p.command(target, closer, depth)
	}
}

func (p *psParser) command(name psWord, closer rune, depth int) (string, bool) {
	args, redirects, ok := p.arguments(closer, depth)
	if !ok {
		return "", false
	}
	words := flattenArguments(args)
	if name.dynamic {
		return renderPowerShellCommand(powerShellDynamicCommand, append([]psWord{{dynamic: true, nested: name.nested}}, words...), redirects), true
	}
	executable := normalizedPowerShellExecutable(name.text)
	lower := strings.ToLower(executable[strings.LastIndexByte(executable, '/')+1:])
	if _, dynamic := powerShellDynamicExecution[lower]; dynamic {
		return renderPowerShellCommand(powerShellDynamicCommand, words, redirects), true
	}
	if lower == "start-process" || lower == "saps" || lower == "start" {
		launched, ok := startProcessInvocation(args)
		if !ok {
			return renderPowerShellCommand(powerShellDynamicCommand, words, redirects), true
		}
		return renderPowerShellCommand(shellCommandWord(launched[0].text), launched[1:], redirects), true
	}
	return renderPowerShellCommand(shellCommandWord(executable), words, redirects), true
}

// arguments parses command arguments and redirections up to the element end.
func (p *psParser) arguments(closer rune, depth int) ([]psArgument, []string, bool) {
	args := []psArgument{}
	redirects := []string{}
	for {
		if !p.skipSpace() {
			return nil, nil, false
		}
		if p.atElementEnd(closer) {
			return args, redirects, true
		}
		if redirect, matched, ok := p.redirection(depth); matched {
			if !ok {
				return nil, nil, false
			}
			redirects = append(redirects, redirect)
			continue
		}
		if p.peek(0) == '-' && p.peek(1) == '-' && p.peek(2) == '%' && (isPowerShellSpace(p.peek(3)) || p.peek(3) == 0 || p.peek(3) == '\n') {
			// Stop-parsing passes the rest of the line verbatim, expanding
			// %VARIABLE% references, so it is never a static argument list.
			p.pos += 3
			for !p.eof() && p.peek(0) != '\n' && p.peek(0) != '|' {
				p.pos++
			}
			args = append(args, psArgument{{dynamic: true}})
			continue
		}
		arg, ok := p.argument(depth)
		if !ok {
			return nil, nil, false
		}
		args = append(args, arg)
	}
}

// argument parses one argument, including every element of a comma list.
func (p *psParser) argument(depth int) (psArgument, bool) {
	values := psArgument{}
	for {
		value, ok := p.primary(depth)
		if !ok {
			return nil, false
		}
		// Adjacent tokens without a separator form one value whose exact
		// concatenation rules are not modeled.
		for !p.eof() && !isPowerShellSpace(p.peek(0)) && !isArgumentBoundary(p.peek(0)) {
			next, ok := p.primary(depth)
			if !ok {
				return nil, false
			}
			value = joinPowerShellWords(value, next)
		}
		values = append(values, value)
		if p.peek(0) != ',' {
			return values, true
		}
		p.pos++
		if !p.skipSeparatorsInline() {
			return nil, false
		}
	}
}

func joinPowerShellWords(left, right psWord) psWord {
	if !left.dynamic && !right.dynamic && strings.HasPrefix(left.text, "-") && strings.HasSuffix(left.text, ":") {
		// `-Name:value` binds one parameter value.
		return psWord{text: left.text + right.text}
	}
	return psWord{dynamic: true, nested: append(append([]string{}, left.nested...), right.nested...)}
}

// primary parses one value: a string, a variable, a group, a script block or
// a bareword, followed by any member access or invocation.
func (p *psParser) primary(depth int) (psWord, bool) {
	var value psWord
	var ok bool
	current := p.peek(0)
	switch {
	case current == '@' && (p.peek(1) == '\'' || isSingleQuote(p.peek(1)) || p.peek(1) == '"' || isDoubleQuote(p.peek(1))) && isLineEnd(p.src, p.pos+2):
		value, ok = p.hereString(depth)
	case isSingleQuote(current):
		value, ok = p.singleQuoted()
	case isDoubleQuote(current):
		value, ok = p.doubleQuoted(depth)
	case current == '$' && p.peek(1) == '(':
		p.pos++
		value, ok = p.groupValue('(', ')', depth)
	case current == '@' && (p.peek(1) == '(' || p.peek(1) == '{'):
		p.pos++
		value, ok = p.groupValue(p.peek(0), closerFor(p.peek(0)), depth)
	case current == '(' || current == '{':
		value, ok = p.groupValue(current, closerFor(current), depth)
	case current == '$' || current == '@' && isVariableStart(p.peek(1)):
		ok = p.variable()
		value = psWord{dynamic: true}
	case current == '[':
		value, ok = p.bracketed(depth)
	default:
		value, ok = p.bareword()
	}
	if !ok {
		return psWord{}, false
	}
	return p.postfix(value, depth)
}

// postfix consumes member access, indexing and invocation after a value.
// Any method invocation can run arbitrary code (ScriptBlock.Invoke,
// Process.Start, COM objects), so it is a dynamic command.
func (p *psParser) postfix(value psWord, depth int) (psWord, bool) {
	for {
		switch {
		case p.peek(0) == '.' && isIdentifierStart(p.peek(1)) && (value.dynamic || p.methodCallFollows(1)),
			p.peek(0) == ':' && p.peek(1) == ':' && isIdentifierStart(p.peek(2)):
			if p.peek(0) == ':' {
				p.pos++
			}
			p.pos++
			for isIdentifierPart(p.peek(0)) {
				p.pos++
			}
			value.dynamic = true
			if p.peek(0) == '(' {
				arguments, ok := p.groupValue('(', ')', depth)
				if !ok {
					return psWord{}, false
				}
				value.nested = append(value.nested, arguments.nested...)
				value.nested = append(value.nested, powerShellDynamicCommand)
			}
		case p.peek(0) == '[' && value.dynamic:
			index, ok := p.bracketed(depth)
			if !ok {
				return psWord{}, false
			}
			value.nested = append(value.nested, index.nested...)
		default:
			return value, true
		}
	}
}

// methodCallFollows reports whether the identifier starting at offset is
// immediately followed by an argument list.
func (p *psParser) methodCallFollows(offset int) bool {
	for isIdentifierPart(p.peek(offset)) {
		offset++
	}
	return p.peek(offset) == '('
}

func (p *psParser) groupValue(opener, closer rune, depth int) (psWord, bool) {
	program, ok := p.group(opener, closer, depth)
	if !ok {
		return psWord{}, false
	}
	return psWord{dynamic: true, nested: []string{program}}, true
}

// group parses a parenthesized pipeline, subexpression, script block or
// hashtable body as a statement list.
func (p *psParser) group(opener, closer rune, depth int) (string, bool) {
	if p.peek(0) != opener {
		return "", false
	}
	p.pos++
	program, ok := p.statements(closer, depth+1)
	if !ok || p.peek(0) != closer {
		return "", false
	}
	p.pos++
	return program, true
}

// bracketed parses a type literal or an index; any group inside is analyzed.
func (p *psParser) bracketed(depth int) (psWord, bool) {
	p.pos++
	value := psWord{dynamic: true}
	for level := 1; ; {
		if p.eof() {
			return psWord{}, false
		}
		switch current := p.peek(0); {
		case current == '[':
			level++
			p.pos++
		case current == ']':
			level--
			p.pos++
			if level == 0 {
				return value, true
			}
		case current == '(' || current == '{' || current == '$' || isQuote(current):
			inner, ok := p.primary(depth)
			if !ok {
				return psWord{}, false
			}
			value.nested = append(value.nested, inner.nested...)
		default:
			p.pos++
		}
	}
}

func (p *psParser) variable() bool {
	p.pos++
	switch current := p.peek(0); {
	case current == '{':
		for p.pos++; !p.eof() && p.peek(0) != '}'; p.pos++ {
			if p.peek(0) == '`' {
				p.pos++
			}
		}
		if p.eof() {
			return false
		}
		p.pos++
	case current == '$' || current == '?' || current == '^':
		p.pos++
	default:
		for isIdentifierPart(p.peek(0)) || p.peek(0) == ':' && isIdentifierPart(p.peek(1)) {
			p.pos++
		}
	}
	return true
}

// expression parses an expression-mode element: values, operators and
// assignments. Only the code it runs contributes invocations.
func (p *psParser) expression(closer rune, depth int) (string, bool) {
	nested := []string{}
	redirects := []string{}
	for {
		if !p.skipSpace() {
			return "", false
		}
		if p.atElementEnd(closer) {
			break
		}
		if redirect, matched, ok := p.redirection(depth); matched {
			if !ok {
				return "", false
			}
			redirects = append(redirects, redirect)
			continue
		}
		if assignment := assignmentOperatorLength(p.src, p.pos); assignment > 0 {
			// The right side of an assignment is a statement.
			p.pos += assignment
			if !p.skipSeparatorsInline() {
				return "", false
			}
			if p.atElementEnd(closer) {
				return "", false
			}
			program, ok := p.pipeline(closer, depth)
			if !ok {
				return "", false
			}
			nested = append(nested, program)
			continue
		}
		current := p.peek(0)
		switch {
		case isDash(current) && isIdentifierStart(p.peek(1)):
			// An operator such as -eq, -not or -join.
			p.pos++
			for isIdentifierPart(p.peek(0)) {
				p.pos++
			}
		case current == ',' || isExpressionOperator(current):
			p.pos++
		default:
			value, ok := p.primary(depth)
			if !ok {
				return "", false
			}
			nested = append(nested, value.nested...)
		}
	}
	return renderPowerShellCommand(":", []psWord{{dynamic: len(nested) > 0, nested: nested}}, redirects), true
}

// redirection parses `>`, `>>`, `n>`, `n>>`, `*>`, `n>&m` and their target.
func (p *psParser) redirection(depth int) (string, bool, bool) {
	start := p.pos
	stream := ""
	if current := p.peek(0); current == '*' || current >= '1' && current <= '6' {
		if p.peek(1) != '>' {
			return "", false, true
		}
		stream = string(current)
		p.pos++
	}
	switch {
	case p.peek(0) == '<':
		// PowerShell reserves `<` for future use; it is a parse error.
		return "", true, false
	case p.peek(0) != '>':
		p.pos = start
		return "", false, true
	}
	operator := ">"
	p.pos++
	if p.peek(0) == '>' {
		operator = ">>"
		p.pos++
	}
	if stream == "*" {
		stream = "&"
	}
	if operator == ">" && p.peek(0) == '&' && p.peek(1) >= '1' && p.peek(1) <= '6' {
		target := string(p.peek(1))
		p.pos += 2
		if stream == "&" {
			stream = "2"
		}
		return stream + ">&" + target, true, true
	}
	if !p.skipSpace() {
		return "", true, false
	}
	if p.peek(0) == '$' && strings.EqualFold(string(p.src[p.pos:min(p.pos+5, len(p.src))]), "$null") && !isIdentifierPart(p.peek(5)) {
		p.pos += 5
		return stream + operator + "/dev/null", true, true
	}
	if p.atElementEnd(0) {
		return "", true, false
	}
	target, ok := p.primary(depth)
	if !ok {
		return "", true, false
	}
	return stream + operator + renderPowerShellWord(target), true, true
}

func (p *psParser) singleQuoted() (psWord, bool) {
	p.pos++
	var text strings.Builder
	for !p.eof() {
		current := p.peek(0)
		p.pos++
		if isSingleQuote(current) {
			if isSingleQuote(p.peek(0)) {
				text.WriteRune(p.peek(0))
				p.pos++
				continue
			}
			return psWord{text: text.String()}, true
		}
		text.WriteRune(current)
	}
	return psWord{}, false
}

func (p *psParser) doubleQuoted(depth int) (psWord, bool) {
	p.pos++
	var text strings.Builder
	word := psWord{}
	for !p.eof() {
		current := p.peek(0)
		switch {
		case isDoubleQuote(current):
			p.pos++
			if isDoubleQuote(p.peek(0)) {
				text.WriteRune(p.peek(0))
				p.pos++
				continue
			}
			word.text = text.String()
			return word, true
		case current == '`':
			escaped, ok := powerShellEscape(p.peek(1))
			if p.peek(1) == 0 || !ok {
				// `u{...} and other escapes are not decoded here.
				word.dynamic = true
				p.pos += 2
				continue
			}
			text.WriteRune(escaped)
			p.pos += 2
		case current == '$' && p.peek(1) == '(':
			p.pos++
			inner, ok := p.groupValue('(', ')', depth)
			if !ok {
				return psWord{}, false
			}
			word.dynamic = true
			word.nested = append(word.nested, inner.nested...)
		case current == '$' && isVariableStart(p.peek(1)):
			// Only the variable expands; a following `.Name` stays text.
			if !p.variable() {
				return psWord{}, false
			}
			word.dynamic = true
		default:
			text.WriteRune(current)
			p.pos++
		}
	}
	return psWord{}, false
}

func (p *psParser) hereString(depth int) (psWord, bool) {
	expandable := isDoubleQuote(p.peek(1))
	p.pos += 2
	for !p.eof() && p.peek(0) != '\n' {
		p.pos++
	}
	p.pos++
	start := p.pos
	for !p.eof() {
		lineStart := p.pos == start || p.peek(-1) == '\n'
		if lineStart && (expandable && isDoubleQuote(p.peek(0)) || !expandable && isSingleQuote(p.peek(0))) && p.peek(1) == '@' {
			body := string(p.src[start:max(start, p.pos-1)])
			p.pos += 2
			if !expandable {
				return psWord{text: body}, true
			}
			inner := &psParser{src: append(append([]rune{'"'}, []rune(strings.ReplaceAll(body, `"`, `""`))...), '"')}
			return inner.doubleQuoted(depth)
		}
		p.pos++
	}
	return psWord{}, false
}

// bareword parses a generic token. Backtick escapes the next character;
// an escape PowerShell decodes to a control character, an embedded quote or
// variable, or a Unicode dash makes the token's exact value uncertain.
func (p *psParser) bareword() (psWord, bool) {
	var text strings.Builder
	word := psWord{}
	start := p.pos
	for !p.eof() {
		current := p.peek(0)
		if isPowerShellSpace(current) || isArgumentBoundary(current) || current == '\n' {
			break
		}
		if p.pos > start && (current == '>' || current == '<') {
			break
		}
		switch {
		case current == '`':
			if strings.ContainsRune("0abefnrtuv", p.peek(1)) {
				// PowerShell may decode this pair to a control character.
				word.dynamic = true
			}
			if p.peek(1) == 0 {
				return psWord{}, false
			}
			text.WriteRune(p.peek(1))
			p.pos += 2
			continue
		case current == '$' || isQuote(current):
			word.dynamic = true
		case p.pos == start && isDash(current):
			current = '-'
		}
		text.WriteRune(current)
		p.pos++
	}
	if p.pos == start {
		return psWord{}, false
	}
	word.text = text.String()
	return word, true
}

func renderPowerShellCommand(command string, args []psWord, redirects []string) string {
	parts := make([]string, 0, 1+len(args)+len(redirects))
	parts = append(parts, command)
	for _, arg := range args {
		parts = append(parts, renderPowerShellWord(arg))
	}
	parts = append(parts, redirects...)
	return strings.Join(parts, " ")
}

func renderPowerShellWord(word psWord) string {
	if !word.dynamic {
		return shellQuote(word.text)
	}
	var rendered strings.Builder
	rendered.WriteString(`"` + powerShellValue)
	for _, program := range word.nested {
		rendered.WriteString("$(" + program + ")")
	}
	rendered.WriteString(`"`)
	return rendered.String()
}

// startProcessInvocation resolves `Start-Process <file> <arguments>` to the
// invocation it launches. It fails for any dynamic file, unknown or ambiguous
// parameter, or surplus positional argument.
func startProcessInvocation(args []psArgument) ([]psWord, bool) {
	var file []psWord
	var launched []psWord
	positional := 0
	for index := 0; index < len(args); index++ {
		arg := args[index]
		name, value, hasValue := "", psArgument(nil), false
		if first := arg[0]; !first.dynamic && strings.HasPrefix(first.text, "-") && len(first.text) > 1 {
			name = strings.ToLower(first.text[1:])
			if before, _, joined := strings.Cut(name, ":"); joined {
				name, hasValue = before, true
				value = append(psArgument{{text: first.text[len(before)+2:]}}, arg[1:]...)
			} else if len(arg) > 1 {
				return nil, false
			}
		}
		if name == "" {
			switch positional {
			case 0:
				file = arg
			case 1:
				launched = append(launched, splitArgumentList(arg)...)
			default:
				return nil, false
			}
			positional++
			continue
		}
		parameter, ok := resolveStartProcessParameter(name)
		if !ok {
			return nil, false
		}
		if !parameter.takesValue {
			continue
		}
		if !hasValue {
			index++
			if index >= len(args) {
				return nil, false
			}
			value = args[index]
		}
		switch parameter.name {
		case "filepath":
			file = value
		case "argumentlist":
			launched = append(launched, splitArgumentList(value)...)
		}
	}
	if len(file) != 1 || file[0].dynamic {
		return nil, false
	}
	return append([]psWord{{text: normalizedPowerShellExecutable(file[0].text)}}, launched...), true
}

// splitArgumentList splits each literal ArgumentList element the way a
// Windows process splits its command line: on blanks outside double quotes.
func splitArgumentList(elements psArgument) []psWord {
	words := []psWord{}
	for _, element := range elements {
		words = append(words, splitArgumentElement(element)...)
	}
	return words
}

func splitArgumentElement(value psWord) []psWord {
	if value.dynamic {
		return []psWord{value}
	}
	words := []psWord{}
	var current strings.Builder
	quoted, started := false, false
	for _, char := range value.text {
		switch {
		case char == '"':
			quoted = !quoted
			started = true
		case !quoted && (char == ' ' || char == '\t'):
			if started {
				words = append(words, psWord{text: current.String()})
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(char)
			started = true
		}
	}
	if quoted {
		return []psWord{{dynamic: true}}
	}
	if started {
		words = append(words, psWord{text: current.String()})
	}
	return words
}

type powerShellParameter struct {
	name       string
	aliases    []string
	takesValue bool
}

var startProcessParameters = []powerShellParameter{
	{name: "argumentlist", aliases: []string{"args"}, takesValue: true},
	{name: "credential", takesValue: true},
	{name: "environment", takesValue: true},
	{name: "filepath", aliases: []string{"pspath", "path"}, takesValue: true},
	{name: "loaduserprofile", aliases: []string{"lup"}},
	{name: "nonewwindow", aliases: []string{"nnw"}},
	{name: "passthru"},
	{name: "redirectstandarderror", aliases: []string{"rse"}, takesValue: true},
	{name: "redirectstandardinput", aliases: []string{"rsi"}, takesValue: true},
	{name: "redirectstandardoutput", aliases: []string{"rso"}, takesValue: true},
	{name: "usenewenvironment"},
	{name: "verb", takesValue: true},
	{name: "wait"},
	{name: "windowstyle", takesValue: true},
	{name: "workingdirectory", takesValue: true},
	{name: "debug", aliases: []string{"db"}},
	{name: "erroraction", aliases: []string{"ea"}, takesValue: true},
	{name: "errorvariable", aliases: []string{"ev"}, takesValue: true},
	{name: "informationaction", aliases: []string{"infa"}, takesValue: true},
	{name: "informationvariable", aliases: []string{"iv"}, takesValue: true},
	{name: "outbuffer", aliases: []string{"ob"}, takesValue: true},
	{name: "outvariable", aliases: []string{"ov"}, takesValue: true},
	{name: "pipelinevariable", aliases: []string{"pv"}, takesValue: true},
	{name: "progressaction", aliases: []string{"proga"}, takesValue: true},
	{name: "verbose", aliases: []string{"vb"}},
	{name: "warningaction", aliases: []string{"wa"}, takesValue: true},
	{name: "warningvariable", aliases: []string{"wv"}, takesValue: true},
	{name: "whatif", aliases: []string{"wi"}},
	{name: "confirm", aliases: []string{"cf"}},
}

func resolveStartProcessParameter(name string) (powerShellParameter, bool) {
	return resolvePowerShellParameter(startProcessParameters, name)
}

// resolvePowerShellParameter applies PowerShell's rule that an alias or an
// unambiguous prefix names a parameter.
func resolvePowerShellParameter(parameters []powerShellParameter, name string) (powerShellParameter, bool) {
	if name == "" {
		return powerShellParameter{}, false
	}
	for _, parameter := range parameters {
		if parameter.name == name || slices.Contains(parameter.aliases, name) {
			return parameter, true
		}
	}
	var match *powerShellParameter
	for index := range parameters {
		parameter := &parameters[index]
		if strings.HasPrefix(parameter.name, name) {
			if match != nil {
				return powerShellParameter{}, false
			}
			match = parameter
		}
	}
	if match == nil {
		return powerShellParameter{}, false
	}
	return *match, true
}

// normalizedPowerShellExecutable writes a Windows command path with `/` and
// drops the executable suffix Windows resolves implicitly.
func normalizedPowerShellExecutable(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	base := name[strings.LastIndexByte(name, '/')+1:]
	lower := strings.ToLower(base)
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".com", ".ps1"} {
		if strings.HasSuffix(lower, suffix) && len(base) > len(suffix) {
			return name[:len(name)-len(suffix)]
		}
	}
	return name
}

func powerShellEscape(char rune) (rune, bool) {
	switch char {
	case '0':
		return 0, true
	case 'a':
		return '\a', true
	case 'b':
		return '\b', true
	case 'e':
		return 0x1b, true
	case 'f':
		return '\f', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	case 'v':
		return '\v', true
	case 'u':
		return 0, false
	default:
		return char, true
	}
}

func assignmentOperatorLength(src []rune, pos int) int {
	at := func(offset int) rune {
		if pos+offset < len(src) {
			return src[pos+offset]
		}
		return 0
	}
	switch {
	case at(0) == '=' && at(1) != '=':
		return 1
	case at(0) == '?' && at(1) == '?' && at(2) == '=':
		return 3
	case strings.ContainsRune("+-*/%", at(0)) && at(1) == '=':
		return 2
	default:
		return 0
	}
}

func isExpressionOperator(char rune) bool {
	return strings.ContainsRune("+-*/%!=<>?:.", char) || isDash(char)
}

func isCommandStart(char rune) bool {
	return unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_' || char == '\\' || char == '/' ||
		char == '.' || char == '~' || char == '%' || char == '?'
}

func isArgumentBoundary(char rune) bool {
	return char == ';' || char == '|' || char == '&' || char == '(' || char == ')' || char == '{' || char == '}' || char == ','
}

func isVariableStart(char rune) bool {
	return isIdentifierPart(char) || char == '{' || char == '$' || char == '?' || char == '^' || char == ':'
}

func isIdentifierStart(char rune) bool {
	return unicode.IsLetter(char) || char == '_'
}

func isIdentifierPart(char rune) bool {
	return unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_'
}

func isPowerShellSpace(char rune) bool {
	return char == ' ' || char == '\t' || char == '\r' || char == '\f' || char == '\v' || char == 0xa0
}

func isSingleQuote(char rune) bool {
	return char == '\'' || char == '‘' || char == '’' || char == '‚' || char == '‛'
}

func isDoubleQuote(char rune) bool {
	return char == '"' || char == '“' || char == '”' || char == '„'
}

func isQuote(char rune) bool { return isSingleQuote(char) || isDoubleQuote(char) }

func isDash(char rune) bool {
	return char == '-' || char == '–' || char == '—' || char == '―'
}

func closerFor(opener rune) rune {
	if opener == '(' {
		return ')'
	}
	return '}'
}

func isLineEnd(src []rune, pos int) bool {
	for ; pos < len(src); pos++ {
		switch src[pos] {
		case ' ', '\t', '\r':
		case '\n':
			return true
		default:
			return false
		}
	}
	return false
}

func isPowerShellNumber(token string) bool {
	if token == "" || !unicode.IsDigit(rune(token[0])) {
		return false
	}
	if strings.HasPrefix(token, "0x") {
		return len(token) > 2 && strings.Trim(token[2:], "0123456789abcdef") == ""
	}
	body := strings.TrimRight(token, "kmgtpbld")
	return strings.Trim(body, "0123456789._e") == "" || strings.Contains(token, "..")
}

func indexRunes(src []rune, from int, needle string) int {
	target := []rune(needle)
	for index := from; index+len(target) <= len(src); index++ {
		if string(src[index:index+len(target)]) == needle {
			return index
		}
	}
	return -1
}

// bashReservedWords maps reserved command names to fixed quoted literals;
// PowerShell aliases such as `select` (Select-Object) collide with them.
var bashReservedWords = map[string]string{
	"!": "'!'", "[[": "'[['", "]]": "']]'", "{": "'{'", "}": "'}'", "case": "'case'", "coproc": "'coproc'", "do": "'do'", "done": "'done'",
	"elif": "'elif'", "else": "'else'", "esac": "'esac'", "fi": "'fi'", "for": "'for'", "function": "'function'", "if": "'if'", "in": "'in'",
	"select": "'select'", "then": "'then'", "time": "'time'", "until": "'until'", "while": "'while'",
}

func shellCommandWord(word string) string {
	if quoted, reserved := bashReservedWords[word]; reserved {
		return quoted
	}
	return shellQuote(word)
}

func shellQuote(word string) string {
	if word != "" && strings.Trim(word, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-") == "" {
		return word
	}
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}
