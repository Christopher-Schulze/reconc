// Package lockdiff compares two compiled lockfiles and reports what
// changed at the rule level (W5). Intended for PR reviews, release
// notes, and `reconc diff` agent usage.
//
// The diff is structural (JSON-level) rather than textual: rule ids
// are matched between sides and fields are compared semantically, so
// reordering rules or reformatting whitespace never shows up as a
// "change".
package lockdiff

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
)

// Report is the structured result of comparing two lockfiles.
type Report struct {
	PathA           string        `json:"path_a"`
	PathB           string        `json:"path_b"`
	Added           []RuleInfo    `json:"added"`
	Removed         []RuleInfo    `json:"removed"`
	Changed         []ChangedRule `json:"changed"`
	Unchanged       int           `json:"unchanged"`
	DefaultModeA    string        `json:"default_mode_a,omitempty"`
	DefaultModeB    string        `json:"default_mode_b,omitempty"`
	DefaultModeDiff bool          `json:"default_mode_changed"`
	DigestA         string        `json:"source_digest_a,omitempty"`
	DigestB         string        `json:"source_digest_b,omitempty"`
}

// RuleInfo is a compact identification of one rule.
type RuleInfo struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Mode string `json:"mode,omitempty"`
}

// ChangedRule describes a rule present in both sides with differing
// fields. FieldsChanged lists the json-level keys whose values differ.
type ChangedRule struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	FieldsChanged []string `json:"fields_changed"`
}

// Diff compares two lockfile paths and returns a deterministic
// Report. Missing files produce a descriptive error so CLI callers
// can surface it verbatim.
func Diff(pathA, pathB string) (*Report, error) {
	a, err := loadLockfile(pathA)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", pathA, err)
	}
	b, err := loadLockfile(pathB)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", pathB, err)
	}
	return diffMaps(pathA, pathB, a, b), nil
}

func loadLockfile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	return out, nil
}

func diffMaps(pathA, pathB string, a, b map[string]interface{}) *Report {
	r := &Report{
		PathA:   pathA,
		PathB:   pathB,
		Added:   []RuleInfo{},
		Removed: []RuleInfo{},
		Changed: []ChangedRule{},
	}

	// Index both sides' rules by id for match + compare.
	rulesA := indexRules(a)
	rulesB := indexRules(b)

	for id, ra := range rulesA {
		rb, ok := rulesB[id]
		if !ok {
			r.Removed = append(r.Removed, ruleInfo(id, ra))
			continue
		}
		fields := ruleFieldsChanged(ra, rb)
		if len(fields) == 0 {
			r.Unchanged++
			continue
		}
		r.Changed = append(r.Changed, ChangedRule{
			ID:            id,
			Kind:          stringField(ra, "kind"),
			FieldsChanged: fields,
		})
	}
	for id, rb := range rulesB {
		if _, ok := rulesA[id]; !ok {
			r.Added = append(r.Added, ruleInfo(id, rb))
		}
	}

	// Deterministic order: by rule id ascending.
	sortRuleInfos(r.Added)
	sortRuleInfos(r.Removed)
	sort.Slice(r.Changed, func(i, j int) bool { return r.Changed[i].ID < r.Changed[j].ID })

	// Default mode + source digest comparison.
	r.DefaultModeA = stringField(a, "default_mode")
	r.DefaultModeB = stringField(b, "default_mode")
	r.DefaultModeDiff = r.DefaultModeA != r.DefaultModeB
	r.DigestA = stringField(a, "source_digest")
	r.DigestB = stringField(b, "source_digest")

	return r
}

func indexRules(payload map[string]interface{}) map[string]map[string]interface{} {
	out := map[string]map[string]interface{}{}
	rules, _ := payload["rules"].([]interface{})
	for _, r := range rules {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		if id != "" {
			out[id] = m
		}
	}
	return out
}

// ruleFieldsChanged returns the sorted list of keys whose values
// differ between a and b. Provenance fields (source_path,
// source_block_id) are ignored -- a rule that only moved between
// files isn't a semantic change.
var provenanceFields = map[string]struct{}{
	"source_path":     {},
	"source_block_id": {},
}

func ruleFieldsChanged(a, b map[string]interface{}) []string {
	keys := map[string]struct{}{}
	for k := range a {
		if _, skip := provenanceFields[k]; !skip {
			keys[k] = struct{}{}
		}
	}
	for k := range b {
		if _, skip := provenanceFields[k]; !skip {
			keys[k] = struct{}{}
		}
	}
	var changed []string
	for k := range keys {
		if !reflect.DeepEqual(a[k], b[k]) {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return changed
}

func ruleInfo(id string, m map[string]interface{}) RuleInfo {
	return RuleInfo{
		ID:   id,
		Kind: stringField(m, "kind"),
		Mode: stringField(m, "mode"),
	}
}

func stringField(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func sortRuleInfos(xs []RuleInfo) {
	sort.Slice(xs, func(i, j int) bool { return xs[i].ID < xs[j].ID })
}

// IsEmpty reports whether the diff found no changes at all.
func (r *Report) IsEmpty() bool {
	return len(r.Added) == 0 && len(r.Removed) == 0 && len(r.Changed) == 0 && !r.DefaultModeDiff
}
