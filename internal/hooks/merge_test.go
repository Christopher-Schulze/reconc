package hooks

import (
	"fmt"
	"slices"
	"testing"
)

// mergeOrderEvents is large enough that Go's randomized map iteration reorders
// an unsorted report on practically every run.
var mergeOrderEvents = []string{"SessionStart", "PreToolUse", "PostToolUse", "Stop", "SessionEnd"}

func reconcHookEntry(route string) interface{} {
	return map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": "${CLAUDE_PROJECT_DIR}/tools/reconc/bin/hook",
				"args":    []interface{}{route, "${CLAUDE_PROJECT_DIR}"},
			},
		},
	}
}

func canonicalAndTamperedHookMaps() (generated, existing map[string]interface{}) {
	generatedHooks := map[string]interface{}{}
	existingHooks := map[string]interface{}{}
	for _, event := range mergeOrderEvents {
		generatedHooks[event] = []interface{}{reconcHookEntry("claude-" + event)}
		existingHooks[event] = []interface{}{reconcHookEntry("claude-" + event + "-tampered")}
	}
	return map[string]interface{}{"hooks": generatedHooks}, map[string]interface{}{"hooks": existingHooks}
}

// TestMergeReconcHooksReportsDeterministicOrder drives the real merge entry
// point rather than the sort helper, so the report stays deterministic only as
// long as the helper is actually wired into the merge. Installing the same
// hooks twice must not produce two different warning texts.
func TestMergeReconcHooksReportsDeterministicOrder(t *testing.T) {
	var baseline []string
	for attempt := range 30 {
		generated, existing := canonicalAndTamperedHookMaps()
		diff := mergeReconcHooks(existing, generated, MergeOptions{})
		if len(diff.Removed) != len(mergeOrderEvents) {
			t.Fatalf("expected one replaced entry per event, got %v", diff.Removed)
		}
		if !slices.IsSorted(diff.Removed) {
			t.Fatalf("merge report is not deterministic on attempt %d: %v", attempt, diff.Removed)
		}
		if baseline == nil {
			baseline = diff.Removed
			continue
		}
		if !slices.Equal(baseline, diff.Removed) {
			t.Fatalf("merge report drifted between runs:\nfirst: %v\nnow:   %v", baseline, diff.Removed)
		}
	}
}

// TestMergeReconcHooksReportsDeterministicKeptOrder covers the second report
// slice, which only fills when the caller opts into keeping user edits.
func TestMergeReconcHooksReportsDeterministicKeptOrder(t *testing.T) {
	var baseline []string
	for attempt := range 30 {
		generated, existing := canonicalAndTamperedHookMaps()
		diff := mergeReconcHooks(existing, generated, MergeOptions{KeepUserEdits: true})
		if len(diff.Kept) != len(mergeOrderEvents) {
			t.Fatalf("expected one kept entry per event, got %v", diff.Kept)
		}
		if !slices.IsSorted(diff.Kept) {
			t.Fatalf("kept report is not deterministic on attempt %d: %v", attempt, diff.Kept)
		}
		if baseline == nil {
			baseline = diff.Kept
			continue
		}
		if !slices.Equal(baseline, diff.Kept) {
			t.Fatalf("kept report drifted between runs:\nfirst: %v\nnow:   %v", baseline, diff.Kept)
		}
	}
}

// TestMergeReconcNestedEventHooksReportsDeterministicOrder covers the nested
// events shape used by the OpenCode-style adapters, the second call site that
// builds a MergeDiff.
func TestMergeReconcNestedEventHooksReportsDeterministicOrder(t *testing.T) {
	build := func() (generated, existing map[string]interface{}) {
		generatedEvents := map[string]interface{}{}
		existingEvents := map[string]interface{}{}
		for i, event := range mergeOrderEvents {
			generatedEvents[event] = []interface{}{reconcHookEntry(fmt.Sprintf("opencode-%d", i))}
			existingEvents[event] = []interface{}{reconcHookEntry(fmt.Sprintf("opencode-%d-tampered", i))}
		}
		return map[string]interface{}{"hooks": map[string]interface{}{"events": generatedEvents}},
			map[string]interface{}{"hooks": map[string]interface{}{"events": existingEvents}}
	}

	var baseline []string
	for attempt := range 30 {
		generated, existing := build()
		diff := mergeReconcNestedEventHooks(existing, generated, MergeOptions{})
		if len(diff.Removed) != len(mergeOrderEvents) {
			t.Fatalf("expected one replaced nested entry per event, got %v", diff.Removed)
		}
		if !slices.IsSorted(diff.Removed) {
			t.Fatalf("nested merge report is not deterministic on attempt %d: %v", attempt, diff.Removed)
		}
		if baseline == nil {
			baseline = diff.Removed
			continue
		}
		if !slices.Equal(baseline, diff.Removed) {
			t.Fatalf("nested merge report drifted between runs:\nfirst: %v\nnow:   %v", baseline, diff.Removed)
		}
	}
}
