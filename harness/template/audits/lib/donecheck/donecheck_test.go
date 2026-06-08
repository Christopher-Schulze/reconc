package donecheck

import (
	"strings"
	"testing"
)

const validFRC = `

## Final Reality Check

- Spec Parity: NO_SPEC_SURFACE
- Spec Scope: no spec surface touched
- Reality Check: PASS - workflow tooling proves itself
- Reality Check Loop: PASS - 2 passes, nothing left
- Tests: NO_CODE_CHANGED
- Evidence: donecheck fixture
- Beyond Spec Handling: N/A
`

func detail(name string, state string, subTasks string, frc string) string {
	body := "# " + name + `

## Why

Keep the workflow resumable.

## Status

State: ` + state + `

## Sub-Tasks

` + subTasks + `

## Notes

ctx
`
	body += frc
	return body
}

func contains(failures []string, needle string) bool {
	for _, f := range failures {
		if strings.Contains(f, needle) {
			return true
		}
	}
	return false
}

func TestCheckFinalRealityCheckMissing(t *testing.T) {
	if got := CheckFinalRealityCheck("# X\n", DefaultSchema()); !contains(got, "missing ## Final Reality Check") {
		t.Fatalf("expected missing-FRC failure, got %v", got)
	}
}

func TestCheckFinalRealityCheckHappy(t *testing.T) {
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", validFRC), DefaultSchema())
	if len(got) != 0 {
		t.Fatalf("expected no failures, got %v", got)
	}
}

func TestCheckFinalRealityCheckFieldsRequired(t *testing.T) {
	frc := strings.Replace(validFRC, "- Evidence: donecheck fixture\n", "", 1)
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if !contains(got, "Final Reality Check missing Evidence") {
		t.Fatalf("expected missing-Evidence, got %v", got)
	}
}

func TestCheckFinalRealityCheckRejectsPlaceholder(t *testing.T) {
	frc := strings.Replace(validFRC, "donecheck fixture", "TODO", 1)
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if !contains(got, "Evidence is placeholder content") {
		t.Fatalf("expected placeholder failure, got %v", got)
	}
}

func TestCheckFinalRealityCheckRejectsBadParity(t *testing.T) {
	frc := strings.Replace(validFRC, "Spec Parity: NO_SPEC_SURFACE", "Spec Parity: WHATEVER", 1)
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if !contains(got, "Spec Parity must be") {
		t.Fatalf("expected bad-parity failure, got %v", got)
	}
}

func TestCheckFinalRealityCheckRejectsMissingPassPrefix(t *testing.T) {
	frc := strings.Replace(validFRC, "Reality Check: PASS - workflow", "Reality Check: workflow", 1)
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if !contains(got, `Reality Check must start with "PASS - "`) {
		t.Fatalf("expected pass-prefix failure, got %v", got)
	}
}

func TestCheckDonePromotionRequiresLoopField(t *testing.T) {
	frc := strings.Replace(validFRC, "- Reality Check Loop: PASS - 2 passes, nothing left\n", "", 1)
	got := CheckDonePromotion(detail("TASK-0001-X", "Done", "- [x] done", frc), "TASK-0001-X", DefaultSchema())
	if !contains(got, "missing Reality Check Loop") {
		t.Fatalf("expected promotion to require loop field, got %v", got)
	}
}

func TestCheckFinalRealityCheckDoesNotRequireLoopFieldRetroactively(t *testing.T) {
	frc := strings.Replace(validFRC, "- Reality Check Loop: PASS - 2 passes, nothing left\n", "", 1)
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if contains(got, "Reality Check Loop") {
		t.Fatalf("CheckFinalRealityCheck must not require the loop field (archives lack it), got %v", got)
	}
}

func TestCheckFinalRealityCheckRejectsLoopWithoutPass(t *testing.T) {
	frc := strings.Replace(validFRC, "Reality Check Loop: PASS - 2 passes, nothing left", "Reality Check Loop: ran it once", 1)
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if !contains(got, `Reality Check Loop must start with "PASS"`) {
		t.Fatalf("expected loop-pass-prefix failure, got %v", got)
	}
}

func TestCheckFinalRealityCheckRejectsBadTests(t *testing.T) {
	frc := strings.Replace(validFRC, "Tests: NO_CODE_CHANGED", "Tests: shipped it", 1)
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if !contains(got, "Tests must name test/coverage proof or NO_CODE_CHANGED") {
		t.Fatalf("expected bad-tests failure, got %v", got)
	}
}

func TestCheckFinalRealityCheckExceedsUserAcceptedRequiresJustification(t *testing.T) {
	frc := strings.ReplaceAll(validFRC, "Spec Parity: NO_SPEC_SURFACE", "Spec Parity: EXCEEDS_USER_ACCEPTED_NO_SPEC_EDIT")
	frc = strings.ReplaceAll(frc, "Beyond Spec Handling: N/A", "Beyond Spec Handling: deferred")
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if !contains(got, "user-accepted spec exceedance must mention") {
		t.Fatalf("expected user-accepted-justification failure, got %v", got)
	}
}

func TestCheckFinalRealityCheckExceedsUserAcceptedAllowsFollowUp(t *testing.T) {
	frc := strings.ReplaceAll(validFRC, "Spec Parity: NO_SPEC_SURFACE", "Spec Parity: EXCEEDS_USER_ACCEPTED_NO_SPEC_EDIT")
	frc = strings.ReplaceAll(frc, "Beyond Spec Handling: N/A", "Beyond Spec Handling: queued follow-up TASK created")
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if len(got) != 0 {
		t.Fatalf("expected no failures, got %v", got)
	}
}

func TestCheckFinalRealityCheckRejectsMatchesWithReducedScope(t *testing.T) {
	frc := strings.ReplaceAll(validFRC, "Spec Parity: NO_SPEC_SURFACE", "Spec Parity: MATCHES")
	frc = strings.ReplaceAll(frc, "Beyond Spec Handling: N/A", "Beyond Spec Handling: partial implementation, follow-up task required")
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if !contains(got, "Spec Parity MATCHES cannot be used") {
		t.Fatalf("expected reduced-scope failure, got %v", got)
	}
}

func TestCheckFinalRealityCheckAllowsMatchesWithNegatedFollowUp(t *testing.T) {
	frc := strings.ReplaceAll(validFRC, "Spec Parity: NO_SPEC_SURFACE", "Spec Parity: MATCHES")
	frc = strings.ReplaceAll(frc, "Beyond Spec Handling: N/A", "Beyond Spec Handling: no follow-up TASK required")
	got := CheckFinalRealityCheck(detail("X", "Done", "- [x] done", frc), DefaultSchema())
	if len(got) != 0 {
		t.Fatalf("expected no failures, got %v", got)
	}
}

func TestCheckDonePromotionHappy(t *testing.T) {
	got := CheckDonePromotion(detail("TASK-0001-X", "Done", "- [x] One done", validFRC), "TASK-0001-X", DefaultSchema())
	if len(got) != 0 {
		t.Fatalf("expected no failures, got %v", got)
	}
}

func TestCheckDonePromotionRejectsActiveState(t *testing.T) {
	got := CheckDonePromotion(detail("TASK-0001-X", "Active", "- [~] working", validFRC), "TASK-0001-X", DefaultSchema())
	if !contains(got, "Status must be 'State: Done'") || !contains(got, "Sub-Task still active") {
		t.Fatalf("expected state+active subtask failures, got %v", got)
	}
}

func TestCheckDonePromotionRejectsBadH1(t *testing.T) {
	got := CheckDonePromotion(detail("TASK-0001-Other", "Done", "- [x] done", validFRC), "TASK-0001-X", DefaultSchema())
	if !contains(got, "H1 must be '# TASK-0001-X'") {
		t.Fatalf("expected H1 failure, got %v", got)
	}
}

func TestCheckDonePromotionListsEachOpenSubTask(t *testing.T) {
	got := CheckDonePromotion(detail("TASK-0001-X", "Done", "- [x] one done\n- [ ] still pending\n- [ ] also pending", validFRC), "TASK-0001-X", DefaultSchema())
	count := 0
	for _, f := range got {
		if strings.Contains(f, "Sub-Task still open") {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 open-subtask failures, got %d (%v)", count, got)
	}
}

func TestCheckDonePromotionRequiresAtLeastOneDoneSubTask(t *testing.T) {
	got := CheckDonePromotion(detail("TASK-0001-X", "Done", "no items here", validFRC), "TASK-0001-X", DefaultSchema())
	if !contains(got, "needs at least one [x] item") {
		t.Fatalf("expected at-least-one-done failure, got %v", got)
	}
}

func TestExtractSectionHandlesEOF(t *testing.T) {
	body := "## Status\n\nState: Done\n"
	got := ExtractSection(body, "## Status")
	if !strings.Contains(got, "State: Done") {
		t.Fatalf("expected State line, got %q", got)
	}
}

func TestExtractSectionUnknownReturnsEmpty(t *testing.T) {
	if got := ExtractSection("# X\n", "## Missing"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestParseBulletFieldsIgnoresNonField(t *testing.T) {
	fields := ParseBulletFields("- Foo: bar\nrandom\n- Baz: qux\n")
	if fields["Foo"] != "bar" || fields["Baz"] != "qux" || len(fields) != 2 {
		t.Fatalf("unexpected fields: %v", fields)
	}
}

func TestIsPlaceholderValue(t *testing.T) {
	schema := DefaultSchema()
	cases := map[string]bool{
		"TODO":              true,
		"tbd":               true,
		"placeholder":       true,
		"unknown":           true,
		"add todo here":     true,
		"production-grade":  false,
		"shipped with test": false,
	}
	for input, want := range cases {
		if got := IsPlaceholderValue(input, schema); got != want {
			t.Fatalf("IsPlaceholderValue(%q): got %v want %v", input, got, want)
		}
	}
}

func TestHasTestIntent(t *testing.T) {
	schema := DefaultSchema()
	if !HasTestIntent("go test ./...", schema) || !HasTestIntent("coverage proven", schema) || !HasTestIntent("bun test --coverage", schema) {
		t.Fatal("expected test intent")
	}
	if HasTestIntent("shipped it", schema) {
		t.Fatal("did not expect test intent")
	}
}

func TestHasSubTaskWithIcon(t *testing.T) {
	body := "- [x] done\n- [ ] open\n"
	if !HasSubTaskWithIcon(body, "x") || !HasSubTaskWithIcon(body, " ") {
		t.Fatal("expected both icons present")
	}
	if HasSubTaskWithIcon(body, "~") {
		t.Fatal("did not expect ~ icon")
	}
}

func TestParseStateMissing(t *testing.T) {
	if got := ParseState("# X\n"); got != "" {
		t.Fatalf("expected empty state, got %q", got)
	}
}
