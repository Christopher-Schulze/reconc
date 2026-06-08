package donecheck

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Schema is the canonical machine-readable description of the TASK detail
// structure. It is loaded from tools/reconc/harness/template/config/workflow/task-schema.yaml and
// shared by the workflow audit and the promote-task-done tool so both binaries
// see exactly the same rules.
type Schema struct {
	RequiredSections   []string          `yaml:"required_sections"`
	StatusStates       []string          `yaml:"status_states"`
	Priorities         []string          `yaml:"priorities"`
	ScopeTypes         []string          `yaml:"scope_types"`
	SubTaskIcons       []string          `yaml:"sub_task_icons"`
	FinalRealityCheck  FinalRealityCheck `yaml:"final_reality_check"`
	PlaceholderValues  []string          `yaml:"placeholder_values"`
	TestIntentKeywords []string          `yaml:"test_intent_keywords"`
}

// FinalRealityCheck describes the rules for the "## Final Reality Check"
// section that every Done TASK must contain.
type FinalRealityCheck struct {
	RequiredFields              []string `yaml:"required_fields"`
	SpecParityValues            []string `yaml:"spec_parity_values"`
	RealityCheckPrefix          string   `yaml:"reality_check_prefix"`
	TestsNoCodeMarker           string   `yaml:"tests_no_code_marker"`
	ExceedsUserAcceptedKeywords []string `yaml:"exceeds_user_accepted_keywords"`
}

// LoadSchema reads and validates the canonical task-schema.yaml file.
func LoadSchema(path string) (Schema, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return Schema{}, fmt.Errorf("read %s: %w", path, err)
	}
	var schema Schema
	if err := yaml.Unmarshal(bytes, &schema); err != nil {
		return Schema{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := schema.validate(); err != nil {
		return Schema{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return schema, nil
}

// DefaultSchema returns a self-contained schema mirroring task-schema.yaml.
// It is used by tests that do not need disk access and as the safety net when
// the YAML cannot be loaded so audit/tool failure modes degrade visibly
// (DefaultSchema fails closed for known-invalid markers like empty fields).
func DefaultSchema() Schema {
	return Schema{
		RequiredSections: []string{"## Why", "## Status", "## Scheduling", "## Technical Plan", "## Acceptance", "## Sub-Tasks", "## Notes", "## Deviations"},
		StatusStates:     []string{"Active", "Queued", "Blocked", "Paused", "Done"},
		Priorities:       []string{"P0", "P1", "P2", "P3"},
		ScopeTypes:       []string{"Slice", "Complete Feature", "Coverage Index", "Audit Repair"},
		SubTaskIcons:     []string{" ", "~", "x"},
		FinalRealityCheck: FinalRealityCheck{
			RequiredFields:              []string{"Spec Parity", "Spec Scope", "Reality Check", "Tests", "Evidence", "Beyond Spec Handling"},
			SpecParityValues:            []string{"MATCHES", "SCOPE_MATCHES_WITH_SPEC_GAPS_QUEUED", "EXCEEDS_SPEC_UPDATED", "EXCEEDS_USER_ACCEPTED_NO_SPEC_EDIT", "NO_SPEC_SURFACE"},
			RealityCheckPrefix:          "PASS - ",
			TestsNoCodeMarker:           "NO_CODE_CHANGED",
			ExceedsUserAcceptedKeywords: []string{"user", "follow-up", "queue"},
		},
		PlaceholderValues:  []string{"todo", "tbd", "placeholder", "n/a?", "unknown", "none yet", "open"},
		TestIntentKeywords: []string{"test", "coverage", "go test", "bun test"},
	}
}

func (s Schema) validate() error {
	if len(s.RequiredSections) == 0 {
		return fmt.Errorf("required_sections must not be empty")
	}
	if len(s.StatusStates) == 0 {
		return fmt.Errorf("status_states must not be empty")
	}
	if len(s.Priorities) == 0 {
		return fmt.Errorf("priorities must not be empty")
	}
	if len(s.ScopeTypes) == 0 {
		return fmt.Errorf("scope_types must not be empty")
	}
	if len(s.SubTaskIcons) == 0 {
		return fmt.Errorf("sub_task_icons must not be empty")
	}
	if len(s.FinalRealityCheck.RequiredFields) == 0 {
		return fmt.Errorf("final_reality_check.required_fields must not be empty")
	}
	if len(s.FinalRealityCheck.SpecParityValues) == 0 {
		return fmt.Errorf("final_reality_check.spec_parity_values must not be empty")
	}
	if s.FinalRealityCheck.RealityCheckPrefix == "" {
		return fmt.Errorf("final_reality_check.reality_check_prefix must not be empty")
	}
	if s.FinalRealityCheck.TestsNoCodeMarker == "" {
		return fmt.Errorf("final_reality_check.tests_no_code_marker must not be empty")
	}
	return nil
}

// IsValidStatusState reports whether state is one of the schema's allowed
// State values.
func (s Schema) IsValidStatusState(state string) bool {
	for _, v := range s.StatusStates {
		if v == state {
			return true
		}
	}
	return false
}

// IsValidPriority reports whether priority is one of the schema's allowed
// Priority values.
func (s Schema) IsValidPriority(priority string) bool {
	for _, v := range s.Priorities {
		if v == priority {
			return true
		}
	}
	return false
}

// IsValidScopeType reports whether scopeType is one of the schema's allowed
// task scope declarations.
func (s Schema) IsValidScopeType(scopeType string) bool {
	for _, v := range s.ScopeTypes {
		if v == scopeType {
			return true
		}
	}
	return false
}

// IsValidSpecParity reports whether parity is an accepted Spec Parity value.
func (s Schema) IsValidSpecParity(parity string) bool {
	for _, v := range s.FinalRealityCheck.SpecParityValues {
		if v == parity {
			return true
		}
	}
	return false
}

// MirroredValues returns every literal string value that AGENTS.md prose must
// contain so the narrative cannot silently drift from the machine schema.
// Section headers like "## Why" are returned without the leading "## " because
// AGENTS.md references them in prose as bare words, not as actual H2 headers.
func (s Schema) MirroredValues() []string {
	var values []string
	for _, section := range s.RequiredSections {
		// Strip the "## " prefix so we look for the bare section name in
		// AGENTS.md prose (which references them as text, not as headers).
		bare := section
		if len(bare) > 3 && bare[:3] == "## " {
			bare = bare[3:]
		}
		values = append(values, bare)
	}
	values = append(values, s.StatusStates...)
	values = append(values, s.Priorities...)
	values = append(values, s.ScopeTypes...)
	values = append(values, s.FinalRealityCheck.RequiredFields...)
	values = append(values, s.FinalRealityCheck.SpecParityValues...)
	values = append(values, s.FinalRealityCheck.TestsNoCodeMarker)
	return values
}
