package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var taskSpecBindingPattern = regexp.MustCompile(`^(docs/spec\.md:L[0-9]+(?:-L?[0-9]+)?)@sha256:([a-f0-9]{64})@([a-z0-9]+(?:\+[a-z0-9]+)+)$`)

var taskSpecGenericTerms = map[string]bool{
	"about": true, "after": true, "again": true, "against": true, "before": true,
	"below": true, "between": true, "both": true, "could": true, "each": true,
	"every": true, "from": true, "have": true, "into": true, "must": true,
	"never": true, "only": true, "other": true, "should": true, "than": true,
	"that": true, "their": true, "them": true, "then": true, "there": true,
	"these": true, "they": true, "this": true, "through": true, "under": true,
	"until": true, "when": true, "where": true, "which": true, "while": true,
	"with": true, "without": true,
	"artemis": true, "code": true, "complete": true, "completion": true,
	"done": true, "feature": true, "implementation": true, "omnimus": true,
	"spec": true, "system": true, "task": true, "work": true,
}

type taskSpecBinding struct {
	ref    string
	digest string
	terms  []string
}

func auditTaskSpecBindings(root string, relative string, content string, info taskDetailInfo, required bool) []string {
	normalizedContent := strings.ReplaceAll(content, "\r\n", "\n")
	scheduling := taskSpecSection(normalizedContent, "## Scheduling")
	values := taskSpecBulletFields(scheduling, "Spec Bindings")
	if len(values) > 1 {
		return []string{fmt.Sprintf("%s: Scheduling must contain exactly one Spec Bindings field", relative)}
	}
	raw := ""
	if len(values) == 1 {
		raw = strings.TrimSpace(values[0])
	}
	if !required && raw == "" {
		return nil
	}
	if info.specLinesRaw == "" {
		return nil
	}
	if info.specLinesRaw == "none" {
		if raw != "none" {
			return []string{fmt.Sprintf("%s: Scheduling Spec Bindings must be none when Spec Lines is none", relative)}
		}
		return nil
	}
	if raw == "" || raw == "none" {
		return []string{fmt.Sprintf("%s: Scheduling Spec Bindings must bind every Spec Lines ref as ref@sha256:<digest>@term1+term2", relative)}
	}

	refs := parseCSVFields(info.specLinesRaw)
	bindings, failures := parseTaskSpecBindings(relative, raw)
	if len(failures) > 0 {
		return failures
	}
	if len(bindings) != len(refs) {
		return []string{fmt.Sprintf("%s: Scheduling Spec Bindings count %d does not match Spec Lines count %d", relative, len(bindings), len(refs))}
	}

	specBytes, err := os.ReadFile(filepath.Join(root, "docs", "spec.md"))
	if err != nil {
		return []string{fmt.Sprintf("%s: read docs/spec.md for Spec Bindings: %v", relative, err)}
	}
	normalizedSpec := strings.ReplaceAll(string(specBytes), "\r\n", "\n")
	specLines := strings.Split(normalizedSpec, "\n")
	specLineCount := lineCount(normalizedSpec)
	taskTerms := taskSpecWordSet(taskSpecSemanticText(normalizedContent, info.completionClaim))
	seenRefs := map[string]bool{}
	for index, binding := range bindings {
		if binding.ref != refs[index] {
			failures = append(failures, fmt.Sprintf("%s: Spec Binding %d ref %s must match Spec Lines ref %s in the same order", relative, index+1, binding.ref, refs[index]))
			continue
		}
		if seenRefs[binding.ref] {
			failures = append(failures, fmt.Sprintf("%s: duplicate Spec Binding ref %s", relative, binding.ref))
			continue
		}
		seenRefs[binding.ref] = true
		start, end, ok := parseSpecLineRef(binding.ref)
		if !ok || start < 1 || end > specLineCount {
			failures = append(failures, fmt.Sprintf("%s: Spec Binding ref %s is outside docs/spec.md", relative, binding.ref))
			continue
		}
		rangeText := strings.Join(specLines[start-1:end], "\n")
		actualDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(rangeText)))
		if binding.digest != actualDigest {
			failures = append(failures, fmt.Sprintf("%s: Spec Binding %s digest drift: got %s, want %s", relative, binding.ref, binding.digest, actualDigest))
		}
		specTerms := taskSpecWordSet(rangeText)
		seenTerms := map[string]bool{}
		for _, term := range binding.terms {
			key := taskSpecTermKey(term)
			if key == "" || taskSpecGenericTerms[key] {
				failures = append(failures, fmt.Sprintf("%s: Spec Binding %s term %q is too generic or malformed", relative, binding.ref, term))
				continue
			}
			if seenTerms[key] {
				failures = append(failures, fmt.Sprintf("%s: Spec Binding %s repeats semantic term %q", relative, binding.ref, term))
				continue
			}
			seenTerms[key] = true
			if !taskTerms[key] {
				failures = append(failures, fmt.Sprintf("%s: Spec Binding %s term %q does not occur in the TASK claim surface", relative, binding.ref, term))
			}
			if !specTerms[key] {
				failures = append(failures, fmt.Sprintf("%s: Spec Binding %s term %q does not occur in the cited spec bytes", relative, binding.ref, term))
			}
		}
	}
	return failures
}

func parseTaskSpecBindings(relative string, raw string) ([]taskSpecBinding, []string) {
	parts := strings.Split(raw, ";")
	bindings := make([]taskSpecBinding, 0, len(parts))
	var failures []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		match := taskSpecBindingPattern.FindStringSubmatch(part)
		if match == nil {
			failures = append(failures, fmt.Sprintf("%s: malformed Spec Binding %q; use ref@sha256:<digest>@term1+term2", relative, part))
			continue
		}
		bindings = append(bindings, taskSpecBinding{ref: match[1], digest: match[2], terms: strings.Split(match[3], "+")})
	}
	return bindings, failures
}

func taskSpecBulletField(section string, name string) string {
	values := taskSpecBulletFields(section, name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func taskSpecBulletFields(section string, name string) []string {
	prefix := "- " + name + ":"
	var values []string
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			values = append(values, strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)))
		}
	}
	return values
}

func taskSpecSemanticText(content string, completionClaim string) string {
	firstLine := content
	if index := strings.IndexByte(content, '\n'); index >= 0 {
		firstLine = content[:index]
	}
	return strings.Join([]string{
		firstLine,
		taskSpecSection(content, "## Why"),
		taskSpecSection(content, "## Technical Plan"),
		taskSpecSection(content, "## Acceptance"),
		taskSpecSection(content, "## Sub-Tasks"),
		completionClaim,
	}, "\n")
}

func taskSpecSection(content string, heading string) string {
	marker := heading + "\n"
	start := strings.Index(content, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	rest := content[start:]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		return rest[:end]
	}
	return rest
}

func taskSpecWordSet(text string) map[string]bool {
	words := map[string]bool{}
	var current []rune
	flush := func() {
		if len(current) == 0 {
			return
		}
		key := taskSpecStem(string(current))
		if len(key) >= 4 {
			words[key] = true
		}
		current = current[:0]
	}
	for _, char := range strings.ToLower(text) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			current = append(current, char)
			continue
		}
		flush()
	}
	flush()
	return words
}

func taskSpecTermKey(term string) string {
	words := taskSpecWordSet(term)
	if len(words) != 1 {
		return ""
	}
	for word := range words {
		return word
	}
	return ""
}

func taskSpecStem(word string) string {
	word = strings.ToLower(word)
	switch {
	case len(word) > 5 && strings.HasSuffix(word, "ies"):
		return strings.TrimSuffix(word, "ies") + "y"
	case len(word) > 6 && strings.HasSuffix(word, "ing"):
		return strings.TrimSuffix(word, "ing")
	case len(word) > 5 && strings.HasSuffix(word, "ed"):
		return strings.TrimSuffix(word, "ed")
	case len(word) > 5 && (strings.HasSuffix(word, "sses") || strings.HasSuffix(word, "shes") || strings.HasSuffix(word, "ches") || strings.HasSuffix(word, "xes") || strings.HasSuffix(word, "zes") || strings.HasSuffix(word, "oes")):
		return strings.TrimSuffix(word, "es")
	case len(word) > 4 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") && !strings.HasSuffix(word, "us"):
		return strings.TrimSuffix(word, "s")
	default:
		return word
	}
}
