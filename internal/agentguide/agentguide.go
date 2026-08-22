// Package agentguide embeds the canonical reconc agent integration
// guide in the binary so that `reconc agent-intro` can print it on
// demand without relying on any filesystem state.
//
// The guide is a ~150-line markdown doc tuned for machine consumption:
// exit-code contract, rule-kind table, decision-loop templates, and
// token-efficiency tips. It is the authoritative reference an agent
// should read before making any policy-sensitive decision in a repo.
package agentguide

import (
	_ "embed"
	"strings"
)

//go:embed guide.md
var guideMD string

// Markdown returns the embedded agent guide as-is.
func Markdown() string {
	return guideMD
}

// Section returns a single ## section of the guide by its (case-
// insensitive, substring) heading match. Returns empty string if no
// heading matches.
//
// Useful for `reconc agent-intro --section exit-codes` style partial
// fetches that save tokens when the agent only needs one slice.
func Section(name string) string {
	if name == "" {
		return guideMD
	}
	lines := strings.Split(guideMD, "\n")
	needle := strings.ToLower(name)

	startIdx := -1
	for i, l := range lines {
		if !strings.HasPrefix(l, "## ") && !strings.HasPrefix(l, "### ") {
			continue
		}
		if strings.Contains(strings.ToLower(l), needle) {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return ""
	}
	startLevel, _ := markdownHeadingLevel(lines[startIdx])
	endIdx := len(lines)
	for j := startIdx + 1; j < len(lines); j++ {
		if level, ok := markdownHeadingLevel(lines[j]); ok && level <= startLevel {
			endIdx = j
			break
		}
	}
	return strings.Join(lines[startIdx:endIdx], "\n")
}

func markdownHeadingLevel(line string) (int, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	return level, level > 0 && level <= 6 && level < len(line) && line[level] == ' '
}

// Sections returns all `## ` top-level headings in document order.
// Useful for `reconc agent-intro --list-sections`.
func Sections() []string {
	var out []string
	for _, l := range strings.Split(guideMD, "\n") {
		if strings.HasPrefix(l, "## ") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(l, "## ")))
		}
	}
	return out
}
