package agentsession

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/runtime"
)

func encodedStopBlock(t *testing.T, reason string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]string{"decision": "block", "reason": reason})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func violationsOf(count int, rule, action string) []runtime.Violation {
	violations := make([]runtime.Violation, count)
	for index := range violations {
		violations[index] = runtime.Violation{RuleID: fmt.Sprintf("%s-%02d", rule, index), RecommendedAction: action}
	}
	return violations
}

func TestStopResponseEncodedSizeStaysWithinItsBudget(t *testing.T) {
	escaped := strings.Repeat("<", 240)
	longPath := "/tmp/" + strings.Repeat("<report>/", 200) + "report.json"
	tests := map[string]struct {
		violations []runtime.Violation
		reportPath string
	}{
		"escaped actions":       {violationsOf(3, "rule", escaped), "/tmp/report.json"},
		"escaped rule ids":      {violationsOf(3, escaped, escaped), "/tmp/report.json"},
		"multibyte actions":     {violationsOf(3, "rule", strings.Repeat("€", 4000)), "/tmp/report.json"},
		"emoji rule ids":        {violationsOf(3, strings.Repeat("🙂", 4000), strings.Repeat("🙂", 4000)), "/tmp/report.json"},
		"control characters":    {violationsOf(3, "rule", strings.Repeat("\x01", 4000)), "/tmp/report.json"},
		"many violations":       {violationsOf(500, "rule", escaped), "/tmp/report.json"},
		"long report path":      {violationsOf(3, escaped, escaped), strings.Repeat("<", 3000)},
		"path over the budget":  {violationsOf(3, escaped, escaped), longPath},
		"short path, long rule": {violationsOf(1, strings.Repeat("x", 5000), "act"), "/tmp/report.json"},
	}
	for name, test := range tests {
		for _, repeated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/repeated=%t", name, repeated), func(t *testing.T) {
				reason := stopReasonForViolations(test.violations, test.reportPath, "RB-123456789abc", repeated, "Repository run: disabled")
				if !utf8.ValidString(reason) {
					t.Fatalf("reason is not valid UTF-8: %q", reason)
				}
				// The durability warning can join the reason on the real output path.
				if size := len(encodedStopBlock(t, reason+stopBlockDurabilityWarning)); size > stopFeedbackMaxBytes {
					t.Fatalf("encoded response has %d bytes, limit %d", size, stopFeedbackMaxBytes)
				}
				body := encodedStopBlock(t, reason)
				var decoded map[string]string
				if err := json.Unmarshal(body, &decoded); err != nil || decoded["decision"] != "block" {
					t.Fatalf("response must stay a valid block: %v %s", err, body)
				}
				for _, want := range []string{"Feedback: RB-123456789abc", "Repository run: disabled", "Report: "} {
					if !strings.Contains(reason, want) {
						t.Fatalf("bounding lost %q: %s", want, reason)
					}
				}
				if len(test.reportPath) < 1000 && !strings.Contains(reason, test.reportPath) {
					t.Fatalf("a path that fits must stay whole: %s", reason)
				}
				if test.reportPath == longPath && strings.Contains(reason, "/tmp/") {
					t.Fatalf("a path that cannot fit must not appear as a usable shortened path: %s", reason)
				}
			})
		}
	}
}

func TestStopFeedbackCountsOmittedViolationsAfterShrinking(t *testing.T) {
	reason := stopReasonForViolations(violationsOf(40, strings.Repeat("<", 240), strings.Repeat("<", 240)), "/tmp/report.json", "", false, "")
	if !strings.Contains(reason, "37 more violation(s)") {
		t.Fatalf("feedback must count what it omits: %s", reason)
	}
}

func TestStopBlockOutputOnTheRealPathStaysWithinBudget(t *testing.T) {
	repo := setupStopBenchmarkRepo(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	state, err := InitializeSessionState(repo, "budget")
	if err != nil {
		t.Fatal(err)
	}
	violations := violationsOf(50, strings.Repeat("<", 240), strings.Repeat("&", 240))
	for attempt, repeated := range []string{"first", "repeated"} {
		output, recordErr := stopBlockJSONOutput(root, state.SessionID, nil, violations)
		if recordErr != nil {
			t.Fatalf("%s: %v", repeated, recordErr)
		}
		if len(output) > stopFeedbackMaxBytes {
			t.Fatalf("%s (attempt %d): output has %d bytes, limit %d", repeated, attempt, len(output), stopFeedbackMaxBytes)
		}
		var decoded map[string]string
		if err := json.Unmarshal([]byte(output), &decoded); err != nil || decoded["decision"] != "block" || !strings.Contains(decoded["reason"], "Report: ") {
			t.Fatalf("%s: %v %s", repeated, err, output)
		}
	}
}

func TestStopFeedbackLeadsWithIdentifiersForHostsThatShortenIt(t *testing.T) {
	reason := stopReasonForViolations(violationsOf(3, "rule", strings.Repeat("action ", 100)), "/tmp/report.json", "RB-123456789abc", false, "Repository run: disabled")
	bounded := boundHostReason(reason)
	if len(bounded) > maxHostReasonBytes {
		t.Fatalf("host reason has %d bytes", len(bounded))
	}
	for _, want := range []string{"Report: /tmp/report.json", "Feedback: RB-123456789abc", "Repository run: disabled", "[rule-00]"} {
		if !strings.Contains(bounded, want) {
			t.Fatalf("host reason lost %q: %s", want, bounded)
		}
	}
}

func TestLockfileBlockMessageBoundsItsDetailAndKeepsTheRepair(t *testing.T) {
	message := lockfileBlockMessage("pre", fmt.Errorf("%s", strings.Repeat("€", 4000)))
	if !utf8.ValidString(message) || len(message) > 2*stopFailureDetailMaxBytes {
		t.Fatalf("message has %d bytes, valid UTF-8=%t", len(message), utf8.ValidString(message))
	}
	if !strings.Contains(message, lockfileRepairHint) {
		t.Fatalf("bounding lost the repair: %s", message)
	}
}

func TestStopDiagnosticsStayValidAndBounded(t *testing.T) {
	policyErr := &rerrors.PolicySourceError{Message: strings.Repeat("€", 4000)}
	for name, message := range map[string]string{
		"policy source": policySourceBlockMessage("stop", policyErr),
		"pre failure":   preCheckFailureMessage("pre", "command check", fmt.Errorf("%s", strings.Repeat("€", 4000))),
		"stop result":   boundStopResult(Result{ExitCode: 2, Stderr: strings.Repeat("€", 4000)}).Stderr,
	} {
		if !utf8.ValidString(message) {
			t.Fatalf("%s: invalid UTF-8", name)
		}
		if len(message) > stopFeedbackMaxBytes {
			t.Fatalf("%s: %d bytes", name, len(message))
		}
	}
}

func TestTruncateBytesKeepsWholeCharacters(t *testing.T) {
	value := strings.Repeat("€", 2000)
	for limit := 1020; limit <= 1026; limit++ {
		got := truncateBytes(value, limit)
		if !utf8.ValidString(got) || len(got) > limit || len(got) < limit-2 {
			t.Fatalf("limit %d: %d bytes, valid=%t", limit, len(got), utf8.ValidString(got))
		}
	}
	if got := truncateBytes("plain", 3); got != "pla" {
		t.Fatalf("ASCII truncation = %q", got)
	}
	if got := truncateBytes("short", 10); got != "short" {
		t.Fatalf("short value changed: %q", got)
	}
}

func TestStopWithMovingInputsDoesNotClaimAnotherWriter(t *testing.T) {
	repo := unstableStopRepo(t, "cause")
	first := RunStop(repo, stopPayload(t, "cause", nil))
	if first.ExitCode != 2 || strings.Contains(first.Stderr, "another writer") || !strings.Contains(first.Stderr, "rewrites its own inputs") {
		t.Fatalf("the message must describe the observed instability without an unproven cause: %+v", first)
	}
}

func TestUncertifiedReleaseIsVisibleInTheRunLog(t *testing.T) {
	repo := unstableStopRepo(t, "logged")
	if err := saveRepositoryRunState(repo, repositoryRunState{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if first := RunStop(repo, stopPayload(t, "logged", nil)); first.ExitCode != 2 {
		t.Fatalf("first Stop must hold: %+v", first)
	}
	second := RunStop(repo, stopPayload(t, "logged", map[string]interface{}{"stop_hook_active": true}))
	if second.ExitCode != 0 || !strings.Contains(second.Stderr, "uncertified") {
		t.Fatalf("second Stop must release: %+v", second)
	}
	decisions, err := ReadRunDecisions(repo, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decision := range decisions {
		if decision.Event == "stop" && decision.Branch == "unverifiable_released_uncertified" && decision.SessionID == "logged" && decision.EnabledAfter {
			found = true
		}
	}
	if !found {
		t.Fatalf("run log does not record the uncertified release: %+v", decisions)
	}
}
