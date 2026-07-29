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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/compiler"
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
	return diffMaps(pathA, pathB, a, b)
}

func loadLockfile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if out == nil {
		return nil, fmt.Errorf("lockfile must contain a JSON object")
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("lockfile contains more than one JSON value")
		}
		return nil, fmt.Errorf("read trailing JSON: %w", err)
	}
	migrated, _, err := compiler.MigrateLockfile(out)
	if err != nil {
		return nil, err
	}
	if err := compiler.ValidateLockfileEnvelope(migrated); err != nil {
		return nil, err
	}
	if _, err := indexRules(migrated); err != nil {
		return nil, err
	}
	return migrated, nil
}

func diffMaps(pathA, pathB string, a, b map[string]interface{}) (*Report, error) {
	r := &Report{
		PathA:   pathA,
		PathB:   pathB,
		Added:   []RuleInfo{},
		Removed: []RuleInfo{},
		Changed: []ChangedRule{},
	}

	// Index both sides' rules by id for match + compare.
	rulesA, err := indexRules(a)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pathA, err)
	}
	rulesB, err := indexRules(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pathB, err)
	}

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

	return r, nil
}

func indexRules(payload map[string]interface{}) (map[string]map[string]interface{}, error) {
	out := map[string]map[string]interface{}{}
	rules, ok := payload["rules"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("rules must contain a list")
	}
	for index, r := range rules {
		m, ok := r.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("rules[%d] must contain an object", index)
		}
		id, _ := m["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("rules[%d].id must contain a non-empty string", index)
		}
		kind, _ := m["kind"].(string)
		if strings.TrimSpace(kind) == "" {
			return nil, fmt.Errorf("rules[%d].kind must contain a non-empty string", index)
		}
		if _, exists := out[id]; exists {
			return nil, fmt.Errorf("duplicate rule id %q", id)
		}
		out[id] = m
	}
	if count, ok := integerField(payload["rule_count"]); !ok || count != len(rules) {
		return nil, fmt.Errorf("rule_count must equal rules length")
	}
	sources, ok := payload["sources"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("sources must contain a list")
	}
	if count, ok := integerField(payload["source_count"]); !ok || count != len(sources) {
		return nil, fmt.Errorf("source_count must equal sources length")
	}
	if mode, _ := payload["default_mode"].(string); mode != "warn" && mode != "block" {
		return nil, fmt.Errorf("default_mode must be warn or block")
	}
	return out, nil
}

func integerField(value interface{}) (int, bool) {
	switch number := value.(type) {
	case json.Number:
		n, err := number.Int64()
		return int(n), err == nil && n >= 0 && int64(int(n)) == n
	case float64:
		n := int(number)
		return n, number >= 0 && float64(n) == number
	default:
		return 0, false
	}
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
		if !reflect.DeepEqual(canonicalValue(k, a[k]), canonicalValue(k, b[k])) {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return changed
}

var orderInsensitiveRuleFields = map[string]struct{}{
	"before_paths": {},
	"claims":       {},
	"commands":     {},
	"must_contain": {},
	"paths":        {},
	"scope_paths":  {},
	"when_paths":   {},
}

// canonicalValue sorts pure string lists only for fields whose evaluator
// semantics are explicitly set-like. Argument and nested lists retain order.
func canonicalValue(field string, v interface{}) interface{} {
	if _, ok := orderInsensitiveRuleFields[field]; !ok {
		return v
	}
	list, ok := v.([]interface{})
	if !ok {
		return v
	}
	sorted := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return v
		}
		sorted = append(sorted, s)
	}
	sort.Strings(sorted)
	out := make([]interface{}, len(sorted))
	for i, s := range sorted {
		out[i] = s
	}
	return out
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
