// Package agentguide embeds the canonical reconc agent integration
// guide in the binary so that `reconc agent-intro` can print it on
// demand without relying on any filesystem state.
//
// The default guide is a compact machine-facing workflow. Full exit-code,
// rule-kind, host, and token-efficiency references remain embedded and are
// fetched on demand by stable section ID.
package agentguide

import (
	_ "embed"
	"strings"
	"unicode"
)

const (
	lazyReferenceMarker = "<!-- RECONC LAZY REFERENCES -->"
	// CoreByteBudget is the maximum default intro size exposed to agents.
	CoreByteBudget = 4096
)

// SectionInfo is a stable, lazily fetchable guide section descriptor.
type SectionInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

//go:embed guide.md
var guideMD string

// Markdown returns the compact core workflow. Specialized material remains
// available through Section and SectionCatalog.
func Markdown() string {
	marker := strings.Index(guideMD, lazyReferenceMarker)
	if marker < 0 {
		return guideMD
	}
	return strings.TrimSuffix(guideMD[:marker], "\n") + "\n"
}

// FullMarkdown returns the complete embedded guide, including lazy references.
func FullMarkdown() string {
	return guideMD
}

// Section returns a single heading section by its (case-insensitive) title or
// stable id. Legacy substring matching remains a fallback for callers that
// used a human heading fragment. Returns empty string if no heading matches.
//
// Useful for `reconc agent-intro --section exit-codes` style partial
// fetches that save tokens when the agent only needs one slice.
func Section(name string) string {
	if strings.TrimSpace(name) == "" {
		return guideMD
	}
	lines := strings.Split(guideMD, "\n")
	needle := strings.ToLower(strings.TrimSpace(name))

	startIdx := -1
	for i, line := range lines {
		level, ok := markdownHeadingLevel(line)
		if !ok || level > 3 {
			continue
		}
		title := strings.TrimSpace(line[level+1:])
		id := sectionID(title)
		if strings.EqualFold(title, strings.TrimSpace(name)) || id == needle || strings.HasPrefix(id, needle+"-") {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		// Preserve the pre-existing human-friendly substring lookup.
		for i, l := range lines {
			if !strings.HasPrefix(l, "## ") && !strings.HasPrefix(l, "### ") {
				continue
			}
			if strings.Contains(strings.ToLower(l), needle) {
				startIdx = i
				break
			}
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

// SectionCatalog returns the top-level lazy references in document order.
// IDs are derived from headings and remain stable when surrounding prose
// changes.
func SectionCatalog() []SectionInfo {
	lines := strings.Split(guideMD, "\n")
	marker := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == lazyReferenceMarker {
			marker = i
			break
		}
	}
	if marker < 0 {
		return nil
	}
	sections := make([]SectionInfo, 0)
	seen := make(map[string]struct{})
	for _, line := range lines[marker+1:] {
		level, ok := markdownHeadingLevel(line)
		if !ok || level != 2 {
			continue
		}
		title := strings.TrimSpace(line[level+1:])
		id := sectionID(title)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		sections = append(sections, SectionInfo{ID: id, Title: title})
	}
	return sections
}

// sectionID converts a heading into the stable id accepted by Section.
func sectionID(title string) string {
	title = strings.TrimSpace(title)
	if strings.HasPrefix(strings.ToLower(title), "reference:") {
		title = strings.TrimSpace(title[len("reference:"):])
	}
	var builder strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			dash = false
			continue
		}
		if builder.Len() > 0 && !dash {
			builder.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func markdownHeadingLevel(line string) (int, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	return level, level > 0 && level <= 6 && level < len(line) && line[level] == ' '
}

// Sections returns all `## ` top-level headings in document order. It remains
// available for compatibility; SectionCatalog is the stable lazy inventory.
func Sections() []string {
	var out []string
	for _, l := range strings.Split(guideMD, "\n") {
		if strings.HasPrefix(l, "## ") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(l, "## ")))
		}
	}
	return out
}
