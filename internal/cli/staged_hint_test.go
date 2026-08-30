package cli

import (
	"strings"
	"testing"
	"unicode/utf8"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

// TestAnnotateStagedCommandViolationsNamesTheProofCommand proves the staged
// gate explains its evidence contract: require_command_success violations gain
// the exact index-bound proof command, and every other violation kind stays
// untouched.
func TestAnnotateStagedCommandViolationsNamesTheProofCommand(t *testing.T) {
	report := &runtime.CheckReport{
		Violations: []runtime.Violation{
			{
				RuleID:            "root-gate",
				Kind:              policy.KindRequireCommandSuccess,
				RecommendedAction: "Run one of the required commands successfully before finishing: x.",
				RequiredCommands:  []string{"codebase/scripts/tests/run-root-tests.sh build"},
			},
			{
				RuleID:            "claims",
				Kind:              policy.KindRequireClaim,
				RecommendedAction: "Record one of the required claims before finishing: c.",
			},
		},
	}
	annotateStagedCommandViolations(report, "/workspace/repo")

	commandAction := report.Violations[0].RecommendedAction
	if !strings.Contains(commandAction, "reconc exec /workspace/repo --staged --shell -- \"codebase/scripts/tests/run-root-tests.sh build\"") {
		t.Fatalf("staged hint must name the exact proof command, got %q", commandAction)
	}
	if !strings.Contains(commandAction, "index-bound") {
		t.Fatalf("staged hint must explain the evidence contract, got %q", commandAction)
	}
	if strings.Contains(report.Violations[1].RecommendedAction, "reconc exec") {
		t.Fatalf("non-command violations must stay untouched, got %q", report.Violations[1].RecommendedAction)
	}

	report.Violations[0].RecommendedAction = strings.Repeat("界", runtime.MaxViolationTextBytes)
	report.Violations[0].RequiredCommands = []string{strings.Repeat("界", runtime.MaxViolationTextBytes)}
	report.NextAction = report.Violations[0].RecommendedAction
	annotateStagedCommandViolations(report, "/workspace/repo")
	commandAction = report.Violations[0].RecommendedAction
	if len(commandAction) > runtime.MaxViolationTextBytes || !utf8.ValidString(commandAction) ||
		!strings.Contains(commandAction, "index-bound") || commandAction != report.Actions[0] || commandAction != report.NextAction {
		t.Fatalf("bounded staged hint = %d bytes, valid=%t, actions=%t, next=%t", len(commandAction), utf8.ValidString(commandAction), commandAction == report.Actions[0], commandAction == report.NextAction)
	}

	annotateStagedCommandViolations(nil, "/workspace/repo")
}
