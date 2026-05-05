package compiler

import (
	"sort"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

// Conflict describes a detected static inconsistency between two
// compiled rules. Detection is deliberately conservative: only pairs
// with exact-match targeting (sorted, normalized paths) are flagged.
// Glob-overlap heuristics can be added later; for now we catch the
// obvious redundancies and contradictions without false positives.
type Conflict struct {
	// Kind is one of the ConflictKind* constants below.
	Kind string `json:"kind"`
	// RuleIDA is the earlier-declared rule in the pair (sort-stable).
	RuleIDA string `json:"rule_id_a"`
	// RuleIDB is the later-declared rule.
	RuleIDB string `json:"rule_id_b"`
	// Description is a one-line human summary fit for `doctor` output.
	Description string `json:"description"`
	// Paths is the shared/overlapping path list that triggered the
	// conflict (populated for path-based conflicts).
	Paths []string `json:"paths,omitempty"`
}

// Conflict kinds. Exported so test fixtures and CLI formatters can use
// them by name.
const (
	ConflictDuplicateDeny          = "duplicate_deny_write"
	ConflictDuplicateRequireRead   = "duplicate_require_read"
	ConflictDenyVsRequireRead      = "deny_vs_require_read"
	ConflictDuplicateRequireCmd    = "duplicate_require_command"
	ConflictDuplicateRequireClaim  = "duplicate_require_claim"
	ConflictForbidVsRequireCommand = "forbid_vs_require_command"
)

// DetectConflicts runs the full static-analysis pass over a parsed
// rule set. Pure function, deterministic ordering (conflicts are
// returned sorted by (RuleIDA, RuleIDB, Kind)).
func DetectConflicts(rules []policy.Rule) []Conflict {
	var out []Conflict

	// Index rules by kind for targeted scans. Composite rules
	// (all_of / any_of / not) are intentionally skipped here; they
	// need their own recursive check which is out of v1 scope.
	byKind := map[policy.Kind][]policy.Rule{}
	for _, r := range rules {
		byKind[r.Kind] = append(byKind[r.Kind], r)
	}

	// --- Exact-match duplicates within the same kind -----------------
	out = append(out, findExactDuplicates(byKind[policy.KindDenyWrite], "paths", ConflictDuplicateDeny)...)
	out = append(out, findExactDuplicates(byKind[policy.KindRequireRead], "when_paths", ConflictDuplicateRequireRead)...)
	out = append(out, findExactDuplicates(byKind[policy.KindRequireCommand], "commands", ConflictDuplicateRequireCmd)...)
	out = append(out, findExactDuplicates(byKind[policy.KindRequireClaim], "claims", ConflictDuplicateRequireClaim)...)

	// --- Cross-kind contradictions -----------------------------------
	// deny_write X + require_read when_paths=X => writing X is forbidden
	// but something else requires reading it first, which is fine. The
	// impossible-to-satisfy case is deny_write X + require_read paths=X
	// (when_paths on require_read is the trigger, paths is what must be
	// read). If require_read's REQUIRED path is deny-written elsewhere,
	// that's not a contradiction either. The TRUE contradiction is
	// deny_write paths=X and require_read when_paths=X (X is both
	// writable-never and writing-triggers-reads). Let's surface that as
	// a heads-up rather than an error: it's legal but almost always
	// indicates a rule-authoring mistake.
	out = append(out, findDenyVsRequireRead(byKind[policy.KindDenyWrite], byKind[policy.KindRequireRead])...)

	// forbid_command X + require_command X (same command forbidden AND
	// required) => impossible to satisfy.
	out = append(out, findForbidVsRequireCommand(byKind[policy.KindForbidCommand], byKind[policy.KindRequireCommand])...)

	// Stable ordering.
	sort.Slice(out, func(i, j int) bool {
		if out[i].RuleIDA != out[j].RuleIDA {
			return out[i].RuleIDA < out[j].RuleIDA
		}
		if out[i].RuleIDB != out[j].RuleIDB {
			return out[i].RuleIDB < out[j].RuleIDB
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// findExactDuplicates emits a Conflict for every pair of rules of the
// same kind whose target slice (selected by `field`) matches exactly
// after sorting. A pair is only reported once (smaller id first).
func findExactDuplicates(rules []policy.Rule, field string, kind string) []Conflict {
	var out []Conflict
	for i := 0; i < len(rules); i++ {
		for j := i + 1; j < len(rules); j++ {
			a := rules[i]
			b := rules[j]
			if !slicesEqualSorted(selectField(a, field), selectField(b, field)) {
				continue
			}
			idA, idB := a.ID, b.ID
			if idB < idA {
				idA, idB = idB, idA
			}
			out = append(out, Conflict{
				Kind:        kind,
				RuleIDA:     idA,
				RuleIDB:     idB,
				Description: "rules '" + idA + "' and '" + idB + "' have identical " + field + " and are redundant",
				Paths:       append([]string(nil), selectField(a, field)...),
			})
		}
	}
	return out
}

func findDenyVsRequireRead(denies, reads []policy.Rule) []Conflict {
	var out []Conflict
	for _, d := range denies {
		denySet := map[string]struct{}{}
		for _, p := range d.Paths {
			denySet[p] = struct{}{}
		}
		for _, r := range reads {
			for _, w := range r.WhenPaths {
				if _, ok := denySet[w]; ok {
					idA, idB := d.ID, r.ID
					if idB < idA {
						idA, idB = idB, idA
					}
					out = append(out, Conflict{
						Kind:        ConflictDenyVsRequireRead,
						RuleIDA:     idA,
						RuleIDB:     idB,
						Description: "rule '" + d.ID + "' denies writes to '" + w + "' while '" + r.ID + "' triggers on the same path (unreachable)",
						Paths:       []string{w},
					})
					break // don't double-report for the same pair
				}
			}
		}
	}
	return out
}

func findForbidVsRequireCommand(forbids, requires []policy.Rule) []Conflict {
	var out []Conflict
	for _, f := range forbids {
		forbidSet := map[string]struct{}{}
		for _, c := range f.Commands {
			forbidSet[c] = struct{}{}
		}
		for _, r := range requires {
			for _, c := range r.Commands {
				if _, ok := forbidSet[c]; ok {
					idA, idB := f.ID, r.ID
					if idB < idA {
						idA, idB = idB, idA
					}
					out = append(out, Conflict{
						Kind:        ConflictForbidVsRequireCommand,
						RuleIDA:     idA,
						RuleIDB:     idB,
						Description: "command '" + c + "' is both forbidden by '" + f.ID + "' and required by '" + r.ID + "'",
						Paths:       []string{c},
					})
					break
				}
			}
		}
	}
	return out
}

// selectField picks the target slice for duplicate detection. Kept
// trivial so we don't need reflection.
func selectField(r policy.Rule, field string) []string {
	switch field {
	case "paths":
		return r.Paths
	case "when_paths":
		return r.WhenPaths
	case "commands":
		return r.Commands
	case "claims":
		return r.Claims
	}
	return nil
}

// slicesEqualSorted returns true when sorted copies of a and b contain
// the same strings. Comparing sorted copies keeps the check order-
// insensitive so ['a','b'] matches ['b','a'].
func slicesEqualSorted(a, b []string) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if strings.TrimSpace(aa[i]) != strings.TrimSpace(bb[i]) {
			return false
		}
	}
	return true
}
