// Package lockdiff compares two compiled lockfiles and reports semantic rule
// changes, source inventory/provenance moves, and every changed envelope
// field. Intended for PR reviews, release notes, and `reconc diff` agent usage.
//
// The diff is structural (JSON-level) rather than textual: rule ids
// are matched between sides and fields are compared semantically, so
// reordering rules or reformatting whitespace never shows up as a
// "change".
package lockdiff

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
)

const maxLockfileBytes = compiler.MaxLockfileBytes

// Report is the structured result of comparing two lockfiles.
type Report struct {
	PathA           string                 `json:"path_a"`
	PathB           string                 `json:"path_b"`
	FieldClasses    []FieldClassification  `json:"field_classes"`
	EnvelopeChanges []FieldChange          `json:"envelope_changes"`
	Added           []RuleInfo             `json:"added"`
	Removed         []RuleInfo             `json:"removed"`
	Changed         []ChangedRule          `json:"changed"`
	RuleProvenance  []RuleProvenanceChange `json:"rule_provenance_changes"`
	SourceChanges   []SourceChange         `json:"source_changes"`
	SourceOrderDiff bool                   `json:"source_order_changed"`
	SourceOrderA    []string               `json:"source_order_a,omitempty"`
	SourceOrderB    []string               `json:"source_order_b,omitempty"`
	Unchanged       int                    `json:"unchanged"`
	DefaultModeA    string                 `json:"default_mode_a,omitempty"`
	DefaultModeB    string                 `json:"default_mode_b,omitempty"`
	DefaultModeDiff bool                   `json:"default_mode_changed"`
	DigestA         string                 `json:"source_digest_a,omitempty"`
	DigestB         string                 `json:"source_digest_b,omitempty"`
}

// FieldClass explains how a validated lockfile envelope field should be
// reviewed. Unsupported fields are retained in the report rather than being
// silently ignored, which makes schema drift visible to callers.
type FieldClass string

const (
	FieldClassSemantic    FieldClass = "semantic"
	FieldClassProvenance  FieldClass = "provenance"
	FieldClassGenerated   FieldClass = "generated"
	FieldClassUnsupported FieldClass = "unsupported"
)

type FieldClassification struct {
	Field string     `json:"field"`
	Class FieldClass `json:"class"`
}

// FieldChange is a deterministic JSON rendering of one top-level envelope
// field. Before and After contain canonical JSON values, or <absent> when the
// field exists on only one side.
type FieldChange struct {
	Field  string     `json:"field"`
	Class  FieldClass `json:"class"`
	Before string     `json:"before"`
	After  string     `json:"after"`
}

type RuleSourceRef struct {
	Path    string `json:"path,omitempty"`
	BlockID string `json:"block_id,omitempty"`
}

// RuleProvenanceChange reports a rule that kept its semantic fields but moved
// between source locations. It is separate from ChangedRule so a source move
// cannot disappear behind provenance suppression.
type RuleProvenanceChange struct {
	ID            string        `json:"id"`
	Kind          string        `json:"kind"`
	FieldsChanged []string      `json:"fields_changed"`
	Before        RuleSourceRef `json:"before"`
	After         RuleSourceRef `json:"after"`
}

type SourceInfo struct {
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	ContentSHA256 string `json:"content_sha256"`
	BlockID       string `json:"block_id,omitempty"`
	LineStart     int    `json:"line_start,omitempty"`
}

type SourceChange struct {
	Change string      `json:"change"`
	Before *SourceInfo `json:"before,omitempty"`
	After  *SourceInfo `json:"after,omitempty"`
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
	data, err := boundedio.ReadRegularFile(path, maxLockfileBytes)
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
		PathA:           pathA,
		PathB:           pathB,
		FieldClasses:    []FieldClassification{},
		EnvelopeChanges: []FieldChange{},
		Added:           []RuleInfo{},
		Removed:         []RuleInfo{},
		Changed:         []ChangedRule{},
		RuleProvenance:  []RuleProvenanceChange{},
		SourceChanges:   []SourceChange{},
	}
	r.FieldClasses = classifyTopLevelFields(a, b)
	var err error
	if r.SourceChanges, r.SourceOrderDiff, r.SourceOrderA, r.SourceOrderB, err = diffSources(a, b); err != nil {
		return nil, err
	}
	r.EnvelopeChanges = diffEnvelope(a, b)

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
		if provenance := ruleProvenanceChange(id, ra, rb); provenance != nil {
			r.RuleProvenance = append(r.RuleProvenance, *provenance)
		}
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
	sort.Slice(r.RuleProvenance, func(i, j int) bool { return r.RuleProvenance[i].ID < r.RuleProvenance[j].ID })
	sort.Slice(r.SourceChanges, func(i, j int) bool {
		return sourceChangeSortKey(r.SourceChanges[i]) < sourceChangeSortKey(r.SourceChanges[j])
	})

	// Default mode + source digest comparison.
	r.DefaultModeA = stringField(a, "default_mode")
	r.DefaultModeB = stringField(b, "default_mode")
	r.DefaultModeDiff = r.DefaultModeA != r.DefaultModeB
	r.DigestA = stringField(a, "source_digest")
	r.DigestB = stringField(b, "source_digest")

	return r, nil
}

var topLevelFieldClasses = map[string]FieldClass{
	"$schema":           FieldClassSemantic,
	"compiler_version":  FieldClassProvenance,
	"format_version":    FieldClassSemantic,
	"repo_root":         FieldClassGenerated,
	"default_mode":      FieldClassSemantic,
	"rule_count":        FieldClassGenerated,
	"source_count":      FieldClassGenerated,
	"source_digest":     FieldClassProvenance,
	"lock_digest":       FieldClassGenerated,
	"source_precedence": FieldClassSemantic,
	"discovery":         FieldClassProvenance,
	"sources":           FieldClassProvenance,
	"rules":             FieldClassSemantic,
	"actions":           FieldClassSemantic,
	"custom_runtimes":   FieldClassSemantic,
}

func classifyTopLevelFields(a, b map[string]interface{}) []FieldClassification {
	keys := map[string]struct{}{}
	for key := range topLevelFieldClasses {
		keys[key] = struct{}{}
	}
	for key := range a {
		keys[key] = struct{}{}
	}
	for key := range b {
		keys[key] = struct{}{}
	}
	result := make([]FieldClassification, 0, len(keys))
	for key := range keys {
		class, ok := topLevelFieldClasses[key]
		if !ok {
			class = FieldClassUnsupported
		}
		result = append(result, FieldClassification{Field: key, Class: class})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Field < result[j].Field })
	return result
}

func diffEnvelope(a, b map[string]interface{}) []FieldChange {
	keys := map[string]struct{}{}
	for key := range a {
		keys[key] = struct{}{}
	}
	for key := range b {
		keys[key] = struct{}{}
	}
	result := make([]FieldChange, 0, len(keys))
	for key := range keys {
		if key == "rules" || key == "sources" {
			continue
		}
		before := fieldJSONValue(a, key)
		after := fieldJSONValue(b, key)
		if before == after {
			continue
		}
		class, ok := topLevelFieldClasses[key]
		if !ok {
			class = FieldClassUnsupported
		}
		result = append(result, FieldChange{Field: key, Class: class, Before: before, After: after})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Field < result[j].Field })
	return result
}

func fieldJSONValue(payload map[string]interface{}, key string) string {
	value, ok := payload[key]
	if !ok {
		return "<absent>"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<unencodable:%T>", value)
	}
	return string(encoded)
}

func ruleProvenanceChange(id string, a, b map[string]interface{}) *RuleProvenanceChange {
	fields := []string{}
	for _, field := range []string{"source_path", "source_block_id"} {
		if stringField(a, field) != stringField(b, field) {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	sort.Strings(fields)
	return &RuleProvenanceChange{
		ID: id, Kind: stringField(a, "kind"), FieldsChanged: fields,
		Before: RuleSourceRef{Path: stringField(a, "source_path"), BlockID: stringField(a, "source_block_id")},
		After:  RuleSourceRef{Path: stringField(b, "source_path"), BlockID: stringField(b, "source_block_id")},
	}
}

func diffSources(a, b map[string]interface{}) ([]SourceChange, bool, []string, []string, error) {
	sourcesA, err := sourceInventory(a)
	if err != nil {
		return nil, false, nil, nil, fmt.Errorf("source inventory A: %w", err)
	}
	sourcesB, err := sourceInventory(b)
	if err != nil {
		return nil, false, nil, nil, fmt.Errorf("source inventory B: %w", err)
	}
	usedB := make([]bool, len(sourcesB))
	changes := []SourceChange{}
	for _, sourceA := range sourcesA {
		match := -1
		for candidate, sourceB := range sourcesB {
			if usedB[candidate] || sourceLocationKey(sourceA) != sourceLocationKey(sourceB) {
				continue
			}
			match = candidate
			break
		}
		if match >= 0 {
			usedB[match] = true
			if !reflect.DeepEqual(sourceA, sourcesB[match]) {
				changes = append(changes, SourceChange{Change: "changed", Before: sourceCopy(&sourceA), After: sourceCopy(&sourcesB[match])})
			}
			continue
		}
		contentMatch := -1
		for candidate, sourceB := range sourcesB {
			if usedB[candidate] || sourceContentKey(sourceA) != sourceContentKey(sourceB) {
				continue
			}
			contentMatch = candidate
			break
		}
		if contentMatch >= 0 {
			usedB[contentMatch] = true
			changes = append(changes, SourceChange{Change: "moved", Before: sourceCopy(&sourceA), After: sourceCopy(&sourcesB[contentMatch])})
			continue
		}
		changes = append(changes, SourceChange{Change: "removed", Before: sourceCopy(&sourceA)})
	}
	for index, sourceB := range sourcesB {
		if !usedB[index] {
			changes = append(changes, SourceChange{Change: "added", After: sourceCopy(&sourceB)})
		}
	}
	orderA := sourceOrderKeys(sourcesA)
	orderB := sourceOrderKeys(sourcesB)
	orderChanged := !reflect.DeepEqual(orderA, orderB)
	if !orderChanged {
		orderA, orderB = nil, nil
	}
	return changes, orderChanged, orderA, orderB, nil
}

func sourceInventory(payload map[string]interface{}) ([]SourceInfo, error) {
	raw, ok := payload["sources"].([]interface{})
	if !ok {
		return nil, errors.New("sources must contain a list")
	}
	result := make([]SourceInfo, 0, len(raw))
	seen := map[string]struct{}{}
	for index, value := range raw {
		object, ok := value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("sources[%d] must contain an object", index)
		}
		kind, _ := object["kind"].(string)
		pathValue, _ := object["path"].(string)
		digest, _ := object["content_sha256"].(string)
		blockID, _ := object["block_id"].(string)
		lineStart := 0
		if rawLine, present := object["line_start"]; present {
			var valid bool
			lineStart, valid = integerField(rawLine)
			if !valid {
				return nil, fmt.Errorf("sources[%d].line_start must be a non-negative integer", index)
			}
		}
		if strings.TrimSpace(kind) == "" || strings.TrimSpace(pathValue) == "" || strings.TrimSpace(digest) == "" {
			return nil, fmt.Errorf("sources[%d] has incomplete identity", index)
		}
		info := SourceInfo{Kind: kind, Path: pathValue, ContentSHA256: digest, BlockID: blockID, LineStart: lineStart}
		location := sourceLocationKey(info)
		if _, exists := seen[location]; exists {
			return nil, fmt.Errorf("duplicate source location %q", location)
		}
		seen[location] = struct{}{}
		result = append(result, info)
	}
	return result, nil
}

func sourceCopy(value *SourceInfo) *SourceInfo {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func sourceLocationKey(value SourceInfo) string {
	return value.Kind + "\x00" + value.Path + "\x00" + value.BlockID + "\x00" + strconv.Itoa(value.LineStart)
}

func sourceContentKey(value SourceInfo) string {
	return value.Kind + "\x00" + value.ContentSHA256
}

func sourceOrderKeys(values []SourceInfo) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = sourceOrderLabel(value)
	}
	return result
}

func sourceOrderLabel(value SourceInfo) string {
	result := value.Kind + ":" + value.Path
	if value.BlockID != "" {
		result += "#" + value.BlockID
	}
	if value.LineStart != 0 {
		result += "@" + strconv.Itoa(value.LineStart)
	}
	return result
}

func sourceChangeSortKey(change SourceChange) string {
	before, after := "", ""
	if change.Before != nil {
		before = sourceLocationKey(*change.Before)
	}
	if change.After != nil {
		after = sourceLocationKey(*change.After)
	}
	return change.Change + "\x00" + before + "\x00" + after
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
	case int:
		return number, number >= 0
	case int64:
		return int(number), number >= 0 && int64(int(number)) == number
	case uint:
		return int(number), uint(int(number)) == number
	case uint64:
		return int(number), uint64(int(number)) == number
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

// canonicalValue recursively normalizes a value for comparison. String lists
// whose field name is in orderInsensitiveRuleFields are sorted so reordering
// is not reported as a change. Nested maps and lists are canonicalized
// recursively so set-like fields inside checks, evidence, and assurance
// entries (for example must_contain) are also compared order-insensitively.
func canonicalValue(field string, v interface{}) interface{} {
	switch val := v.(type) {
	case []interface{}:
		out := make([]interface{}, len(val))
		allStrings := true
		for i, elem := range val {
			canonical := canonicalValue(field, elem)
			out[i] = canonical
			if _, isStr := canonical.(string); !isStr {
				allStrings = false
			}
		}
		if _, orderInsensitive := orderInsensitiveRuleFields[field]; orderInsensitive && allStrings {
			sorted := make([]string, len(out))
			for i, elem := range out {
				sorted[i] = elem.(string)
			}
			sort.Strings(sorted)
			for i, s := range sorted {
				out[i] = s
			}
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for key, elem := range val {
			out[key] = canonicalValue(key, elem)
		}
		return out
	default:
		return v
	}
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

// IsEmpty reports whether the diff found no semantic, provenance, or
// envelope-level change. Generated lock digests are not used as a proxy for
// change detection; every non-empty result has a typed explanation above.
func (r *Report) IsEmpty() bool {
	return len(r.Added) == 0 && len(r.Removed) == 0 && len(r.Changed) == 0 &&
		len(r.RuleProvenance) == 0 && len(r.SourceChanges) == 0 && !r.SourceOrderDiff &&
		!hasReviewableEnvelopeChanges(r.EnvelopeChanges)
}

func hasReviewableEnvelopeChanges(changes []FieldChange) bool {
	for _, change := range changes {
		if change.Class != FieldClassGenerated {
			return true
		}
	}
	return false
}
