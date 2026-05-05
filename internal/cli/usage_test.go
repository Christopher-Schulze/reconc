package cli

import (
	"strings"
	"testing"
)

func TestTopLevelHelpReflectsCurrentSurface(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder

	if err := Run([]string{"--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("--help: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "version          print the build version") {
		t.Fatalf("expected help to list version subcommand, got:\n%s", out)
	}
	if strings.Contains(out, "The Go port is in progress") {
		t.Fatalf("expected stale porting text to be removed, got:\n%s", out)
	}
	if !strings.Contains(out, "reconc is the standalone Go implementation in this repository") {
		t.Fatalf("expected current implementation note, got:\n%s", out)
	}
}
