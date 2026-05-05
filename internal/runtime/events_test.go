package runtime

import (
	stderrors "errors"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
)

func parseJSON(t *testing.T, text string) ExecutionInputs {
	t.Helper()
	inputs, err := LoadExecutionInputsText(text, "test")
	if err != nil {
		t.Fatalf("LoadExecutionInputsText: %v", err)
	}
	return inputs
}

func TestLoadEmptyPayload(t *testing.T) {
	got := parseJSON(t, "{}")
	if len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 ||
		len(got.Commands) != 0 || len(got.Claims) != 0 ||
		len(got.CommandResults) != 0 {
		t.Errorf("expected all empty, got %+v", got)
	}
}

func TestLoadNullPayload(t *testing.T) {
	_, err := LoadExecutionInputs(nil)
	if err != nil {
		t.Fatalf("nil payload should be empty inputs, got error: %v", err)
	}
}

func TestLoadBulkLists(t *testing.T) {
	got := parseJSON(t, `{
		"read_paths": ["a.md", "b.md"],
		"write_paths": ["src/main.go"],
		"commands": ["pytest"],
		"claims": ["ci-green"]
	}`)
	if len(got.ReadPaths) != 2 {
		t.Errorf("expected 2 read paths, got %d", len(got.ReadPaths))
	}
	if len(got.WritePaths) != 1 || got.WritePaths[0] != "src/main.go" {
		t.Errorf("write_paths wrong: %v", got.WritePaths)
	}
	if got.Claims[0] != "ci-green" {
		t.Errorf("claims wrong: %v", got.Claims)
	}
}

func TestLoadCommandResults(t *testing.T) {
	got := parseJSON(t, `{
		"command_results": [
			{"command": "go test", "outcome": "success"},
			{"command": "lint", "outcome": "failure"}
		]
	}`)
	if len(got.CommandResults) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got.CommandResults))
	}
	if got.CommandResults[0].Outcome != "success" {
		t.Errorf("first outcome wrong: %s", got.CommandResults[0].Outcome)
	}
	if got.CommandResults[1].Outcome != "failure" {
		t.Errorf("second outcome wrong: %s", got.CommandResults[1].Outcome)
	}
}

func TestLoadEventsArray(t *testing.T) {
	got := parseJSON(t, `{
		"events": [
			{"kind": "read", "path": "a.md"},
			{"kind": "write", "path": "src/main.go"},
			{"kind": "command", "command": "pytest"},
			{"kind": "claim", "claim": "ci-green"}
		]
	}`)
	if len(got.ReadPaths) != 1 || got.ReadPaths[0] != "a.md" {
		t.Errorf("read event wrong: %v", got.ReadPaths)
	}
	if len(got.WritePaths) != 1 || got.WritePaths[0] != "src/main.go" {
		t.Errorf("write event wrong: %v", got.WritePaths)
	}
	if len(got.Commands) != 1 || got.Commands[0] != "pytest" {
		t.Errorf("command event wrong: %v", got.Commands)
	}
	if len(got.Claims) != 1 || got.Claims[0] != "ci-green" {
		t.Errorf("claim event wrong: %v", got.Claims)
	}
}

func TestLoadCommandEventWithOutcome(t *testing.T) {
	got := parseJSON(t, `{
		"events": [
			{"kind": "command", "command": "pytest", "outcome": "success"}
		]
	}`)
	if len(got.CommandResults) != 1 {
		t.Fatalf("expected 1 command result from event with outcome, got %d", len(got.CommandResults))
	}
	if got.CommandResults[0].Outcome != "success" {
		t.Errorf("outcome wrong: %s", got.CommandResults[0].Outcome)
	}
}

func TestBulkAndEventsCombined(t *testing.T) {
	got := parseJSON(t, `{
		"write_paths": ["src/a.go"],
		"events": [
			{"kind": "write", "path": "src/b.go"}
		]
	}`)
	if len(got.WritePaths) != 2 {
		t.Errorf("expected 2 write paths (bulk + event), got %d", len(got.WritePaths))
	}
}

func TestRejectsNonStringPath(t *testing.T) {
	_, err := LoadExecutionInputsText(`{"read_paths": [42]}`, "test")
	if err == nil {
		t.Fatal("expected error")
	}
	var ee *rerrors.EvidenceError
	if !stderrors.As(err, &ee) {
		t.Errorf("expected *EvidenceError, got %T", err)
	}
}

func TestRejectsEmptyPath(t *testing.T) {
	_, err := LoadExecutionInputsText(`{"read_paths": ["  "]}`, "test")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestRejectsInvalidOutcome(t *testing.T) {
	_, err := LoadExecutionInputsText(`{
		"command_results": [{"command": "x", "outcome": "maybe"}]
	}`, "test")
	if err == nil {
		t.Fatal("expected error for invalid outcome")
	}
}

func TestRejectsUnknownEventKind(t *testing.T) {
	_, err := LoadExecutionInputsText(`{
		"events": [{"kind": "telepathy", "path": "x"}]
	}`, "test")
	if err == nil {
		t.Fatal("expected error for unknown event kind")
	}
}

func TestRejectsEventMissingDiscriminator(t *testing.T) {
	_, err := LoadExecutionInputsText(`{
		"events": [{"path": "x"}]
	}`, "test")
	if err == nil {
		t.Fatal("expected error for missing kind")
	}
}

func TestRejectsEventsNotArray(t *testing.T) {
	_, err := LoadExecutionInputsText(`{"events": "not-an-array"}`, "test")
	if err == nil {
		t.Fatal("expected error for non-array events")
	}
}

func TestRejectsBadCommandResultShape(t *testing.T) {
	_, err := LoadExecutionInputsText(`{
		"command_results": [{"outcome": "success"}]
	}`, "test")
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestRejectsInvalidJSON(t *testing.T) {
	_, err := LoadExecutionInputsText("not json", "test")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestMergedWithPreservesOrder(t *testing.T) {
	a := ExecutionInputs{
		ReadPaths:  []string{"a"},
		WritePaths: []string{"x"},
	}
	b := ExecutionInputs{
		ReadPaths:  []string{"b"},
		WritePaths: []string{"y"},
	}
	merged := a.MergedWith(b)
	if len(merged.ReadPaths) != 2 || merged.ReadPaths[0] != "a" || merged.ReadPaths[1] != "b" {
		t.Errorf("merge order wrong: %v", merged.ReadPaths)
	}
	if len(merged.WritePaths) != 2 || merged.WritePaths[0] != "x" || merged.WritePaths[1] != "y" {
		t.Errorf("write merge order wrong: %v", merged.WritePaths)
	}
}

func TestEmptyHasNonNilSlices(t *testing.T) {
	e := Empty()
	if e.ReadPaths == nil || e.WritePaths == nil || e.Commands == nil ||
		e.Claims == nil || e.CommandResults == nil {
		t.Error("Empty() must return non-nil slices for JSON omitempty cleanliness")
	}
}
