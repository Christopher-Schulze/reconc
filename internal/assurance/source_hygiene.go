package assurance

import (
	"bytes"
	"fmt"
	"path/filepath"

	"reconc.dev/reconc/internal/policy"
)

var debtMarkers = [][]byte{
	[]byte("TODO"),
	[]byte("FIXME"),
	[]byte("XXX"),
	[]byte("STUB"),
	[]byte("PLACEHOLDER"),
	[]byte("TEMPORARY"),
	[]byte("NOT IMPLEMENTED"),
}

var javaScriptUnimplementedSentinels = [][]byte{
	[]byte(`throw new error("not implemented")`),
	[]byte("throw new error('not implemented')"),
	[]byte("throw new error(`not implemented`)"),
}

func evaluateSourceHygiene(root string, gate policy.AssuranceGate, changed []string, state *evaluationState) ([]Finding, error) {
	files, err := changedFiles(root, changed, gate.ScanPaths, gate.ExcludePaths, gate.Exemptions, state)
	if err != nil {
		return nil, err
	}
	findings := []Finding{}
	for _, file := range files {
		body, err := state.read(file.full)
		if err != nil {
			return nil, err
		}
		lineNumber := 1
		for len(body) > 0 {
			line := body
			if newline := bytes.IndexByte(body, '\n'); newline >= 0 {
				line = body[:newline]
				body = body[newline+1:]
			} else {
				body = nil
			}
			if problem := sourceHygieneProblem(filepath.Ext(file.relative), line); problem != "" {
				findings = append(findings, Finding{
					GateID: gate.ID, Paths: []string{file.relative},
					Message:     fmt.Sprintf("%s at %s:%d", problem, file.relative, lineNumber),
					Remediation: "Implement or remove the shipped-code marker, handle the ignored error, or add a narrowly reasoned path exemption.",
				})
			}
			lineNumber++
		}
	}
	return findings, nil
}

func sourceHygieneProblem(extension string, line []byte) string {
	trimmed := bytes.TrimSpace(line)
	if comment, ok := sourceCommentBody(extension, trimmed); ok {
		comment = trimCommentDecoration(comment)
		for _, marker := range debtMarkers {
			if hasFoldedTokenPrefix(comment, marker) {
				return "implementation-debt marker " + string(marker)
			}
		}
		return ""
	}

	switch extension {
	case ".go":
		if bytes.Equal(trimmed, []byte("_ = err")) {
			return "ignored Go error sentinel"
		}
		if containsCodeFold(trimmed, []byte(`panic("not implemented")`)) ||
			containsCodeFold(trimmed, []byte("panic(`not implemented`)")) {
			return "unimplemented Go panic sentinel"
		}
	case ".rs":
		if containsCodeFold(trimmed, []byte("todo!(")) || containsCodeFold(trimmed, []byte("unimplemented!(")) {
			return "unimplemented Rust macro sentinel"
		}
	case ".js", ".jsx", ".ts", ".tsx":
		for _, sentinel := range javaScriptUnimplementedSentinels {
			if containsCodeFold(trimmed, sentinel) {
				return "unimplemented JavaScript/TypeScript throw sentinel"
			}
		}
	}
	return ""
}

func sourceCommentBody(extension string, line []byte) ([]byte, bool) {
	if extension == ".py" && len(line) > 0 && line[0] == '#' {
		return line[1:], true
	}
	if len(line) >= 2 && line[0] == '/' && (line[1] == '/' || line[1] == '*') {
		return line[2:], true
	}
	if len(line) >= 4 && line[0] == '<' && line[1] == '!' && line[2] == '-' && line[3] == '-' {
		return line[4:], true
	}
	if len(line) > 0 && line[0] == '*' && (len(line) == 1 || isSpace(line[1]) || line[1] == '/') {
		return line[1:], true
	}
	return nil, false
}

func trimCommentDecoration(comment []byte) []byte {
	comment = bytes.TrimSpace(comment)
	for len(comment) > 0 {
		switch comment[0] {
		case '/', '*', '!':
			comment = bytes.TrimSpace(comment[1:])
		default:
			return comment
		}
	}
	return comment
}

func hasFoldedTokenPrefix(value, prefix []byte) bool {
	if len(value) < len(prefix) || !bytes.EqualFold(value[:len(prefix)], prefix) {
		return false
	}
	if len(value) == len(prefix) {
		return true
	}
	switch value[len(prefix)] {
	case ' ', '\t', ':', '-', '(', '[', '!':
		return true
	default:
		return false
	}
}

func containsCodeFold(value, target []byte) bool {
	if len(target) == 0 {
		return true
	}
	for start := 0; start+len(target) <= len(value); start++ {
		if bytes.EqualFold(value[start:start+len(target)], target) && codePosition(value, start) {
			return true
		}
	}
	return false
}

func codePosition(line []byte, end int) bool {
	var quote byte
	for index := 0; index < end; index++ {
		current := line[index]
		if quote != 0 {
			if current == quote && !escapedAt(line, index) {
				quote = 0
			}
			continue
		}
		if current == '/' && index+1 < end && (line[index+1] == '/' || line[index+1] == '*') {
			return false
		}
		switch current {
		case '"', '`':
			quote = current
		case '\'':
			if hasClosingQuote(line, index+1, end, current) {
				quote = current
			}
		}
	}
	return quote == 0
}

func hasClosingQuote(line []byte, start, end int, quote byte) bool {
	for index := start; index < end; index++ {
		if line[index] == quote && !escapedAt(line, index) {
			return true
		}
	}
	return false
}

func escapedAt(line []byte, index int) bool {
	backslashes := 0
	for index--; index >= 0 && line[index] == '\\'; index-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func isSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}
