package assurance

import (
	"bytes"
	"strings"
)

type guardLexMode uint8

const (
	guardLexCode guardLexMode = iota
	guardLexBlockComment
	guardLexString
	guardLexRawString
)

type guardLexState struct {
	mode          guardLexMode
	delimiter     string
	escapes       bool
	escaped       bool
	doubledQuotes bool
}

func guardCodeLines(extension string, lines []string) []string {
	extension = strings.ToLower(extension)
	codeLines := make([]string, len(lines))
	state := guardLexState{}
	for index, line := range lines {
		codeLines[index] = scanGuardLine(extension, line, &state)
	}
	return codeLines
}

func scanGuardLine(extension, line string, state *guardLexState) string {
	code := []byte(line)
	for index := 0; index < len(code); {
		if state.mode != guardLexCode {
			index = consumeGuardNonCode(extension, code, index, state)
			continue
		}

		if opening, closing, lineComment := guardCommentStart(extension, code, index); opening != "" {
			if lineComment {
				maskGuardBytes(code, index, len(code))
				break
			}
			state.mode = guardLexBlockComment
			state.delimiter = closing
			maskGuardBytes(code, index, index+len(opening))
			index += len(opening)
			continue
		}

		if consumed, delimiter, escapes, doubledQuotes, raw, ok := guardStringStart(extension, code, index); ok {
			state.delimiter = delimiter
			state.escapes = escapes
			state.escaped = false
			state.doubledQuotes = doubledQuotes
			if raw {
				state.mode = guardLexRawString
			} else {
				state.mode = guardLexString
			}
			maskGuardBytes(code, index, index+consumed)
			index += consumed
			continue
		}

		index++
	}
	return string(code)
}

func guardCommentStart(extension string, line []byte, index int) (opening, closing string, lineComment bool) {
	switch {
	case extension == ".heex" && bytes.HasPrefix(line[index:], []byte("<%!--")):
		return "<%!--", "--%>", false
	case bytes.HasPrefix(line[index:], []byte("<!--")):
		return "<!--", "-->", false
	case powerShellExtension(extension) && bytes.HasPrefix(line[index:], []byte("<#")):
		return "<#", "#>", false
	case bytes.HasPrefix(line[index:], []byte("/*")):
		return "/*", "*/", false
	case bytes.HasPrefix(line[index:], []byte("//")):
		return "//", "", true
	case hashCommentLanguage(extension) && line[index] == '#' &&
		!(extension == ".php" && index+1 < len(line) && line[index+1] == '['):
		return "#", "", true
	default:
		return "", "", false
	}
}

func guardStringStart(extension string, line []byte, index int) (consumed int, delimiter string, escapes, doubledQuotes, raw, ok bool) {
	if powerShellExtension(extension) {
		switch {
		case bytes.HasPrefix(line[index:], []byte("@\"")):
			return 2, "\"@", false, false, true, true
		case bytes.HasPrefix(line[index:], []byte("@'")):
			return 2, "'@", false, false, true, true
		}
	}
	if extension == ".cs" && bytes.HasPrefix(line[index:], []byte("@\"")) {
		return 2, "\"", false, true, false, true
	}
	if consumed, delimiter, ok := guardRustRawStringStart(extension, line, index); ok {
		return consumed, delimiter, false, false, true, true
	}
	if consumed, delimiter, ok := guardCPlusPlusRawStringStart(extension, line, index); ok {
		return consumed, delimiter, false, false, true, true
	}
	for _, delimiter := range []string{`"""`, `'''`} {
		if bytes.HasPrefix(line[index:], []byte(delimiter)) {
			return len(delimiter), delimiter, true, false, false, true
		}
	}
	if index >= len(line) {
		return 0, "", false, false, false, false
	}
	switch line[index] {
	case '\'', '"', '`':
		escapes = line[index] != '`' || extension != ".go"
		return 1, string(line[index]), escapes, false, false, true
	default:
		return 0, "", false, false, false, false
	}
}

func guardRustRawStringStart(extension string, line []byte, index int) (consumed int, delimiter string, ok bool) {
	if extension != ".rs" || index >= len(line) {
		return 0, "", false
	}
	prefixLength := 0
	switch {
	case line[index] == 'r':
		prefixLength = 1
	case line[index] == 'b' && index+1 < len(line) && line[index+1] == 'r':
		prefixLength = 2
	default:
		return 0, "", false
	}
	hashCount := 0
	position := index + prefixLength
	for position < len(line) && line[position] == '#' {
		hashCount++
		position++
	}
	if position >= len(line) || line[position] != '"' {
		return 0, "", false
	}
	return position - index + 1, `"` + strings.Repeat("#", hashCount), true
}

func guardCPlusPlusRawStringStart(extension string, line []byte, index int) (consumed int, delimiter string, ok bool) {
	if !cPlusPlusExtension(extension) || index+1 >= len(line) || line[index] != 'R' || line[index+1] != '"' {
		return 0, "", false
	}
	rest := line[index+2:]
	open := bytes.IndexByte(rest, '(')
	if open < 0 || open > 16 {
		return 0, "", false
	}
	return open + 3, ")" + string(rest[:open]) + `"`, true
}

func cPlusPlusExtension(extension string) bool {
	switch extension {
	case ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx":
		return true
	default:
		return false
	}
}

func consumeGuardNonCode(extension string, line []byte, index int, state *guardLexState) int {
	if state.mode == guardLexBlockComment || state.mode == guardLexRawString {
		closing := []byte(state.delimiter)
		closeAt := bytes.Index(line[index:], closing)
		if closeAt < 0 {
			maskGuardBytes(line, index, len(line))
			return len(line)
		}
		end := index + closeAt + len(closing)
		maskGuardBytes(line, index, end)
		*state = guardLexState{}
		return end
	}

	delimiter := []byte(state.delimiter)
	for index < len(line) {
		if state.escaped {
			maskGuardBytes(line, index, index+1)
			state.escaped = false
			index++
			continue
		}
		if state.escapes && guardStringEscape(extension, line[index]) {
			maskGuardBytes(line, index, index+1)
			state.escaped = true
			index++
			continue
		}
		if state.doubledQuotes && len(delimiter) == 1 && line[index] == delimiter[0] && index+1 < len(line) && line[index+1] == delimiter[0] {
			maskGuardBytes(line, index, index+2)
			index += 2
			continue
		}
		if bytes.HasPrefix(line[index:], delimiter) {
			end := index + len(delimiter)
			maskGuardBytes(line, index, end)
			*state = guardLexState{}
			return end
		}
		maskGuardBytes(line, index, index+1)
		index++
	}
	return index
}

func guardStringEscape(extension string, value byte) bool {
	if powerShellExtension(extension) {
		return value == '`'
	}
	return value == '\\'
}

func maskGuardBytes(value []byte, start, end int) {
	for index := start; index < end; index++ {
		value[index] = ' '
	}
}
