// Package donecheck centralises validation rules for TASK Done state.
//
// The workflow-audit binary and the promote-task-done tool both need to verify
// that a TASK detail file is complete enough to be archived to docs/tasks/done/.
// This package is the single source of truth for those rules so the validator
// (audit) and the mutator (promote-task-done) cannot drift apart and leave
// half-promoted state behind.
//
// All structural facts (required sections, allowed status values, allowed Spec
// Parity values, placeholder markers, ...) live in
// tools/reconc/harness/template/config/workflow/task-schema.yaml and are loaded at startup via
// LoadSchema. DefaultSchema() mirrors the YAML so tests and the failure-safe
// path do not need disk access.
package donecheck

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	stateLine     = "State: "
	stateDone     = "Done"
	subTaskOpen   = "- [ ]"
	subTaskActive = "- [~]"
	headerStatus  = "## Status"
	headerSubs    = "## Sub-Tasks"
	headerFinal   = "## Final Reality Check"
)

var finalFieldRe = regexp.MustCompile(`^- ([A-Za-z ]+):\s*(.+)$`)

// CheckFinalRealityCheck validates the "## Final Reality Check" section of a
// TASK detail file against the supplied Schema. Failures are returned as
// plain strings without any file/path prefix so callers can prepend their
// own context (audit prepends the file path, the tool prepends nothing).
func CheckFinalRealityCheck(content string, schema Schema) []string {
	section := ExtractSection(content, headerFinal)
	if strings.TrimSpace(section) == "" {
		return []string{"done TASK missing " + headerFinal}
	}
	fields := ParseBulletFields(section)
	var failures []string
	for _, field := range schema.FinalRealityCheck.RequiredFields {
		value := strings.TrimSpace(fields[field])
		if value == "" {
			failures = append(failures, fmt.Sprintf("Final Reality Check missing %s", field))
			continue
		}
		if IsPlaceholderValue(value, schema) {
			failures = append(failures, fmt.Sprintf("Final Reality Check %s is placeholder content", field))
		}
	}
	if parity := strings.TrimSpace(fields["Spec Parity"]); parity != "" && !schema.IsValidSpecParity(parity) {
		failures = append(failures, "Spec Parity must be "+strings.Join(schema.FinalRealityCheck.SpecParityValues, ", "))
	}
	prefix := schema.FinalRealityCheck.RealityCheckPrefix
	if reality := strings.TrimSpace(fields["Reality Check"]); reality != "" && !strings.HasPrefix(reality, prefix) {
		failures = append(failures, fmt.Sprintf("Reality Check must start with %q", prefix))
	}
	// The per-TASK Reality-Check loop (docs/task-loop-workflow.md) must be run
	// before Done; its outcome is attested in the "Reality Check Loop" field and
	// must assert completion ("PASS ..."), so the forensic re-review cannot be
	// silently skipped between finishing a TASK and continuing to the next.
	if loop := strings.TrimSpace(fields["Reality Check Loop"]); loop != "" && !strings.HasPrefix(loop, "PASS") {
		failures = append(failures, `Reality Check Loop must start with "PASS" and confirm the per-TASK loop in docs/task-loop-workflow.md ran with nothing left`)
	}
	noCode := schema.FinalRealityCheck.TestsNoCodeMarker
	if tests := strings.TrimSpace(fields["Tests"]); tests != "" && tests != noCode && !HasTestIntent(tests, schema) {
		failures = append(failures, fmt.Sprintf("Tests must name test/coverage proof or %s", noCode))
	}
	if parity := strings.TrimSpace(fields["Spec Parity"]); parity == "EXCEEDS_USER_ACCEPTED_NO_SPEC_EDIT" {
		handling := strings.ToLower(fields["Beyond Spec Handling"])
		matched := false
		for _, kw := range schema.FinalRealityCheck.ExceedsUserAcceptedKeywords {
			if strings.Contains(handling, strings.ToLower(kw)) {
				matched = true
				break
			}
		}
		if !matched {
			failures = append(failures, "user-accepted spec exceedance must mention "+strings.Join(schema.FinalRealityCheck.ExceedsUserAcceptedKeywords, "/"))
		}
	}
	if strings.TrimSpace(fields["Spec Parity"]) == "MATCHES" && HasScopeReductionMarker(section) {
		failures = append(failures, "Spec Parity MATCHES cannot be used when Final Reality Check mentions reduced/deferred/incomplete scope")
	}
	return failures
}

// CheckDonePromotion is the strict superset used by promote-task-done before
// it mutates the repo. It validates H1, State == Done, no open or active
// Sub-Tasks, at least one done Sub-Task, and a complete Final Reality Check.
// Each open or active Sub-Task line is reported individually so the user gets
// a precise punch list instead of "still open".
func CheckDonePromotion(content string, taskName string, schema Schema) []string {
	var failures []string
	if !strings.HasPrefix(content, "# "+taskName+"\n") {
		failures = append(failures, fmt.Sprintf("H1 must be '# %s'", taskName))
	}
	state := ParseState(content)
	if state != stateDone {
		failures = append(failures, fmt.Sprintf("Status must be 'State: Done' before promotion (got %q)", state))
	}
	subSection := ExtractSection(content, headerSubs)
	for _, line := range strings.Split(subSection, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, subTaskOpen):
			failures = append(failures, fmt.Sprintf("Sub-Task still open: %s", trimmed))
		case strings.HasPrefix(trimmed, subTaskActive):
			failures = append(failures, fmt.Sprintf("Sub-Task still active: %s", trimmed))
		}
	}
	if !HasSubTaskWithIcon(subSection, "x") {
		failures = append(failures, "Sub-Tasks section needs at least one [x] item")
	}
	failures = append(failures, CheckFinalRealityCheck(content, schema)...)
	// Promotion is the per-TASK Done boundary, so the Reality-Check loop
	// attestation is required here rather than in the schema-wide
	// required_fields (which a full-history sweep would retroactively apply to
	// already-archived tasks). The "PASS" prefix is enforced by
	// CheckFinalRealityCheck whenever the field is present.
	if strings.TrimSpace(ParseBulletFields(ExtractSection(content, headerFinal))["Reality Check Loop"]) == "" {
		failures = append(failures, "done TASK Final Reality Check missing Reality Check Loop (run the per-TASK loop in docs/task-loop-workflow.md and record e.g. 'PASS - 2 passes, nothing left')")
	}
	return failures
}

// ExtractSection returns the body of an H2 section identified by its header
// line (e.g. "## Status"). The body stops at the next H2 header. Returns ""
// when the header is not found.
func ExtractSection(content string, header string) string {
	lines := strings.Split(content, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// ParseBulletFields parses bullet-list `- Field: value` lines into a map.
func ParseBulletFields(section string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(section, "\n") {
		match := finalFieldRe.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		fields[match[1]] = strings.TrimSpace(match[2])
	}
	return fields
}

// ParseState reads the State value from the ## Status section.
func ParseState(content string) string {
	section := ExtractSection(content, headerStatus)
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, stateLine) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, stateLine))
		}
	}
	return ""
}

// HasSubTaskWithIcon reports whether section contains any "- [<icon>]" line.
func HasSubTaskWithIcon(section string, icon string) bool {
	prefix := "- [" + icon + "]"
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}

// IsPlaceholderValue reports whether a Final Reality Check field value is one
// of the schema's placeholder markers (case-insensitive). Values containing
// "todo" up to 40 chars long also count as placeholder.
func IsPlaceholderValue(value string, schema Schema) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, placeholder := range schema.PlaceholderValues {
		if normalized == strings.ToLower(placeholder) {
			return true
		}
	}
	if len(normalized) <= 40 && strings.Contains(normalized, "todo") {
		return true
	}
	return false
}

// HasTestIntent reports whether free-form text references real test/coverage
// evidence using one of the schema's test_intent_keywords.
func HasTestIntent(content string, schema Schema) bool {
	normalized := strings.ToLower(content)
	for _, kw := range schema.TestIntentKeywords {
		if strings.Contains(normalized, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func HasScopeReductionMarker(content string) bool {
	normalized := strings.ToLower(content)
	for _, safe := range []string{
		"no deferred",
		"not deferred",
		"no follow-up task",
		"no follow up task",
		"no remaining work",
		"no partial implementation",
		"no scope reduced",
	} {
		normalized = strings.ReplaceAll(normalized, safe, "")
	}
	markers := []string{
		"deferred to",
		"follow-up task",
		"follow up task",
		"not implemented",
		"not complete",
		"partial implementation",
		"producer-only",
		"scope reduced",
		"remaining work",
	}
	for _, marker := range markers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
