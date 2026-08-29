package agentsession

import (
	"errors"
	"strings"
	"testing"
)

var errSyntheticEncodingFailure = errors.New("synthetic encoding failure")

func TestHookControlEncodingFailureIsExplicit(t *testing.T) {
	result := resultWithHookJSON(Result{}, map[string]interface{}{"unencodable": make(chan int)})
	if result.Err == nil || result.ExitCode != 2 || result.Stdout != "" {
		t.Fatalf("encoding failure was not fail-closed: %+v", result)
	}
	if !strings.Contains(result.Stderr, "encoding failure") {
		t.Fatalf("encoding diagnostic missing: %q", result.Stderr)
	}
}

func TestMaterialEventIdentityEncodingFailurePropagates(t *testing.T) {
	payload := &HookPayload{
		ToolName:  "custom-tool",
		ToolInput: map[string]interface{}{"unencodable": make(chan int)},
	}
	if _, err := materialEventSignature(payload, "success"); err == nil {
		t.Fatal("material-event identity accepted an unencodable input")
	}
}

func TestAntigravityPendingIdentityEncodingFailurePropagates(t *testing.T) {
	payload := &HookPayload{
		ToolName:  "custom-tool",
		ToolInput: map[string]interface{}{"unencodable": make(chan int)},
	}
	if _, err := antigravityPendingKey(payload); err == nil {
		t.Fatal("Antigravity pending identity accepted an unencodable input")
	}
}

func TestCursorAdapterPreservesEncodingFailureAndDenies(t *testing.T) {
	result := AdaptCursorResult("cursor-pre-tool-use", Result{Err: errSyntheticEncodingFailure})
	if result.Err != errSyntheticEncodingFailure || result.Stdout == "" {
		t.Fatalf("adapter lost encoding failure or response: %+v", result)
	}
	if !strings.Contains(result.Stdout, `"permission":"deny"`) {
		t.Fatalf("adapter did not fail closed: %s", result.Stdout)
	}
}
