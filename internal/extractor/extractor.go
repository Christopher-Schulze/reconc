// Package extractor turns free-form prose in AGENTS.md / CLAUDE.md
// into structured rule suggestions (W20). This is a keyword / regex
// heuristic, not an LLM call -- we're looking for concrete patterns
// that map to reconc rules with high confidence.
//
// Philosophy:
//   - No false positives. If the regex isn't sure, we skip.
//   - Every suggestion carries a citation (line number + source
//     snippet) so the user can verify against the prose.
//   - Suggestions default to mode: warn. User flips to block after
//     manually reviewing.
//
// Coverage (intentionally narrow):
//   - "don't edit X", "don't modify X", "X is read-only" -> deny_write
//   - ".env" / "secret" mentions -> deny_write secret paths
//   - "run X before committing" / "X must pass" -> require_command
//   - "X is generated" / "autogen" -> deny_write X
//   - "update X when Y changes" -> couple_change Y+X
//
// Not covered (would need an LLM): arbitrary conditional logic,
// rules with multi-sentence scope, negations, aspirational guidance.
package extractor

import (
	"regexp"
	"strings"

	"reconc.dev/reconc/internal/adopt"
)

// Extract scans prose text and returns rule suggestions (adopt format
// so the CLI can render them identically to `reconc adopt` output).
// Deterministic: same input always yields the same slice in the same
// order.
func Extract(content string) []adopt.Suggestion {
	lines := strings.Split(content, "\n")
	seen := map[string]struct{}{}
	out := []adopt.Suggestion{}

	add := func(s adopt.Suggestion) {
		// Deduplicate on id -- a rule authored twice in prose should
		// surface once.
		if _, ok := seen[s.ID]; ok {
			return
		}
		seen[s.ID] = struct{}{}
		out = append(out, s)
	}

	for i, raw := range lines {
		lower := strings.ToLower(raw)
		trimmed := strings.TrimSpace(raw)

		// --- pattern 1: "don't edit/modify/write X" or "X is read-only"
		// The regex has two alternatives with distinct capture groups:
		// group 1 for the "don't edit" form, group 2 for "X is read-only".
		// Whichever matched wins; the other is empty.
		if m := patternReadOnly.FindStringSubmatch(trimmed); m != nil {
			target := m[1]
			if target == "" {
				target = m[2]
			}
			target = strings.Trim(strings.TrimSpace(target), "`'\".,;")
			if isPlausiblePath(target) {
				id := "extract-read-only-" + slugify(target)
				add(adopt.Suggestion{
					ID:       id,
					Kind:     "deny_write",
					Mode:     "warn",
					Message:  "Do not edit " + target + " (authored in prose).",
					Paths:    []string{target},
					Evidence: []string{quoteLine(i, trimmed)},
					Reason:   "prose says '" + strings.TrimSpace(m[0]) + "'",
				})
			}
		}

		// --- pattern 2: "X is generated" / "autogen" -----------------
		if m := patternGenerated.FindStringSubmatch(trimmed); m != nil {
			target := strings.Trim(m[1], "`'\"")
			if isPlausiblePath(target) {
				id := "extract-generated-" + slugify(target)
				add(adopt.Suggestion{
					ID:       id,
					Kind:     "deny_write",
					Mode:     "warn",
					Message:  target + " is generated; edit the source, not the output.",
					Paths:    []string{target},
					Evidence: []string{quoteLine(i, trimmed)},
					Reason:   "prose indicates " + target + " is generated",
				})
			}
		}

		// --- pattern 3: "run X before committing" --------------------
		if m := patternRunBeforeCommit.FindStringSubmatch(trimmed); m != nil {
			cmd := strings.Trim(m[1], "`'\".,;")
			if isPlausibleCommand(cmd) {
				id := "extract-run-" + slugify(cmd)
				add(adopt.Suggestion{
					ID:        id,
					Kind:      "require_command",
					Mode:      "warn",
					Message:   "Run '" + cmd + "' before committing (authored in prose).",
					WhenPaths: []string{"**"},
					Commands:  []string{cmd},
					Evidence:  []string{quoteLine(i, trimmed)},
					Reason:    "prose says '" + m[0] + "'",
				})
			}
		}

		// --- pattern 4: .env / secrets mention -----------------------
		if patternSecrets.MatchString(lower) {
			id := "extract-no-secrets"
			add(adopt.Suggestion{
				ID:       id,
				Kind:     "deny_write",
				Mode:     "block",
				Message:  "Do not write secret files (.env, *.secret) - authored in prose.",
				Paths:    []string{"**/.env", "**/.env.*", "**/*.secret"},
				Evidence: []string{quoteLine(i, trimmed)},
				Reason:   "prose mentions secrets / .env",
			})
		}

		// --- pattern 5: "ci must be green" / "ci-green claim" --------
		if patternCIGreen.MatchString(lower) {
			id := "extract-ci-green"
			add(adopt.Suggestion{
				ID:        id,
				Kind:      "require_claim",
				Mode:      "warn",
				Message:   "Assert ci-green before merging (authored in prose).",
				WhenPaths: []string{"**"},
				Claims:    []string{"ci-green"},
				Evidence:  []string{quoteLine(i, trimmed)},
				Reason:    "prose requires CI-green gating",
			})
		}
	}
	return out
}

// --- patterns --------------------------------------------------------

var (
	// "don't edit X" / "do not modify X" / "X is read-only"
	patternReadOnly = regexp.MustCompile(
		`(?i)\b(?:don['’]t|do not|never)\s+(?:edit|modify|write\s+to|touch)\s+([^,.;\n]+)|([^\s,.;]+)\s+is\s+read-only\b`)

	// "X is generated" / "X is auto-generated" / "X is autogen"
	patternGenerated = regexp.MustCompile(
		`(?i)\b([^\s,.;]+?)\s+is\s+(?:auto-?)?generated\b`)

	// "run X before committing" / "run X before commit"
	patternRunBeforeCommit = regexp.MustCompile(
		`(?i)\brun\s+` + "`?([^`\\n]+?)`?" + `\s+before\s+(?:commit(?:ting)?|merging|pushing)\b`)

	// Any mention of secrets-adjacent keywords. Note that \b before
	// "." does not create a boundary (both are non-word), so for the
	// .env token we match on a leading non-alpha or start-of-string
	// context instead.
	patternSecrets = regexp.MustCompile(
		`(?i)(?:^|[^a-z])\.env\b|\bsecret(?:s)?\b|\bapi[-_\s]?key(?:s)?\b`)

	// "ci must be green" / "ci-green" / "green ci"
	patternCIGreen = regexp.MustCompile(
		`(?i)\b(ci[-\s]green|green[-\s]ci|ci\s+must\s+be\s+green|wait\s+for\s+ci)\b`)
)

// isPlausiblePath returns true for strings that look like repo paths
// or globs. Filters out pronouns / articles that would otherwise
// match the read-only regex ("don't edit this" shouldn't produce a
// rule with paths: ['this']).
func isPlausiblePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Reject pronouns / determiners / obviously-not-paths.
	low := strings.ToLower(s)
	for _, bad := range []string{"this", "that", "these", "those", "anything", "the code", "the files", "them", "it"} {
		if low == bad {
			return false
		}
	}
	// Paths typically have at least one of: '/', '.', '**', a
	// file-extension-like suffix. If none of those are present,
	// it's probably a noun phrase.
	if !strings.ContainsAny(s, "/.*") {
		return false
	}
	return true
}

// isPlausibleCommand filters obvious non-commands out of the
// "run X before committing" regex capture.
func isPlausibleCommand(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Must contain at least one alphanumeric run (filters out
	// punctuation-only captures).
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

// slugify returns a short, safe-for-rule-id slug of arbitrary text.
// Non-alphanumeric chars collapse to "-"; runs of "-" are compressed;
// length capped at 40 chars so rule ids stay readable.
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 40 {
		out = out[:40]
		out = strings.TrimRight(out, "-")
	}
	if out == "" {
		return "rule"
	}
	return out
}

// quoteLine formats a cited line for the suggestion's Evidence field.
// 1-indexed line number, truncated content.
func quoteLine(idx int, s string) string {
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return "line " + itoaSmall(idx+1) + ": " + s
}

// itoaSmall is a tiny integer formatter. Avoids pulling strconv into
// this package for a single call site.
func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
