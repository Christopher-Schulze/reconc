package runtime

import (
	"path"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/shellcommand"
)

func TestNormalizeCommandSemanticsMatchesLegacyBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		command string
		root    string
	}{
		{name: "empty"},
		{name: "stacked wrappers", command: "rtk rtk go  test ./...&&rtk git status", root: "/repo"},
		{name: "repository anchor", command: "cd /repo//sub/../ && rtk go test", root: "/repo/"},
		{name: "quoted separators", command: `echo "a && rtk b" |& rtk grep x`},
		{name: "nested substitution", command: "echo $(printf %s $(true || rtk git status))"},
		{name: "unterminated quote", command: `echo "a && rtk b`},
		{name: "unterminated substitution", command: "echo $(true && rtk git status"},
		{name: "escaped separators", command: `echo a\ \&\&\ rtk b`},
		{name: "line continuation separator", command: "true &\\\n& rtk go test"},
		{name: "crlf continuation", command: "rtk go \\\r\ntest"},
		{name: "redirect ampersands", command: "go test &>out 2>&1"},
		{name: "unicode edge whitespace", command: "\u2003rtk go test\u2003&&\u2003rtk git status\u2003"},
		{name: "malformed trailing escape", command: "rtk printf x\\"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := legacyNormalizeCommandSemantics(test.command, test.root)
			if got := normalizeCommandSemantics(test.command, test.root); got != want {
				t.Fatalf("normalization drift for %q: got %q want %q", test.command, got, want)
			}
		})
	}
}

type legacyCommandSegment struct {
	sep  string
	body string
}

var legacyCommandSeparators = []string{"&&", "||", "|&", ";", "|", "&"}

func legacyNormalizeCommandSemantics(command, repoRoot string) string {
	command = legacyNormalizeShellWhitespace(command)
	if command == "" {
		return ""
	}
	repoRoot = strings.TrimRight(strings.TrimSpace(repoRoot), "/")
	segments := legacySplitCommandSegments(command)
	for index := range segments {
		segments[index].body = legacyNormalizeSegmentBody(segments[index].body, repoRoot)
	}
	for len(segments) >= 2 && segments[0].body == "cd ." &&
		(segments[1].sep == " && " || segments[1].sep == " ; ") {
		segments = segments[1:]
		segments[0].sep = ""
	}
	var output strings.Builder
	for index, segment := range segments {
		if index > 0 {
			output.WriteString(segment.sep)
		}
		output.WriteString(segment.body)
	}
	return legacyNormalizeShellWhitespace(output.String())
}

func legacySplitCommandSegments(command string) []legacyCommandSegment {
	segments := make([]legacyCommandSegment, 0, 4)
	start := 0
	nextSeparator := ""
	var quote byte
	escaped := false
	substitutionDepth := 0
	for index := 0; index < len(command); index++ {
		current := command[index]
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		if current == '(' && (substitutionDepth > 0 || index > 0 && command[index-1] == '$') {
			substitutionDepth++
			continue
		}
		if current == ')' && substitutionDepth > 0 {
			substitutionDepth--
			continue
		}
		if substitutionDepth > 0 {
			continue
		}
		for _, separator := range legacyCommandSeparators {
			if !strings.HasPrefix(command[index:], separator) {
				continue
			}
			if separator == "&" && ((index > 0 && command[index-1] == '>') ||
				(index+1 < len(command) && command[index+1] == '>')) {
				continue
			}
			segments = append(segments, legacyCommandSegment{sep: nextSeparator, body: command[start:index]})
			nextSeparator = " " + separator + " "
			index += len(separator) - 1
			start = index + 1
			break
		}
	}
	return append(segments, legacyCommandSegment{sep: nextSeparator, body: command[start:]})
}

func legacyNormalizeShellWhitespace(command string) string {
	command = shellcommand.StripLineContinuations(command)
	var normalized strings.Builder
	normalized.Grow(len(command))
	var quote byte
	escaped := false
	pendingSpace := false
	lastUnquotedSeparator := false
	flushSpace := func() {
		if pendingSpace && normalized.Len() > 0 {
			normalized.WriteByte(' ')
		}
		pendingSpace = false
	}
	for index := 0; index < len(command); index++ {
		current := command[index]
		if escaped {
			flushSpace()
			normalized.WriteByte(current)
			lastUnquotedSeparator = false
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			flushSpace()
			normalized.WriteByte(current)
			escaped = true
			continue
		}
		if quote != 0 {
			normalized.WriteByte(current)
			lastUnquotedSeparator = false
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			flushSpace()
			quote = current
			normalized.WriteByte(current)
			lastUnquotedSeparator = false
			continue
		}
		if current == '\r' || current == '\n' {
			flushSpace()
			if normalized.Len() > 0 && !lastUnquotedSeparator {
				normalized.WriteString(" ; ")
			}
			lastUnquotedSeparator = true
			if current == '\r' && index+1 < len(command) && command[index+1] == '\n' {
				index++
			}
			continue
		}
		if current == ' ' || current == '\t' {
			pendingSpace = true
			continue
		}
		flushSpace()
		normalized.WriteByte(current)
		lastUnquotedSeparator = current == ';' || current == '|' || current == '&'
	}
	return normalized.String()
}

func legacyNormalizeSegmentBody(body, repoRoot string) string {
	body = strings.TrimSpace(body)
	maximumWrappers := len(body) / len("rtk ")
	for range maximumWrappers {
		if !strings.HasPrefix(body, "rtk ") {
			break
		}
		body = strings.TrimSpace(body[len("rtk "):])
	}
	if repoRoot == "" || !strings.HasPrefix(body, "cd ") {
		return body
	}
	argument := strings.TrimSpace(body[len("cd "):])
	if strings.HasPrefix(argument, "\"") || strings.HasPrefix(argument, "'") {
		return body
	}
	cleaned := path.Clean(argument)
	cleanedRoot := path.Clean(repoRoot)
	if cleaned == cleanedRoot {
		return "cd ."
	}
	if strings.HasPrefix(cleaned, cleanedRoot+"/") {
		return "cd " + strings.TrimPrefix(cleaned, cleanedRoot+"/")
	}
	return body
}
