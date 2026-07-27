package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintPlanRendersEmptyAndPopulatedClaimSets(t *testing.T) {
	empty := captureStdout(t, func() {
		printPlan("TASK-0001-Empty", nil)
	})
	if !strings.Contains(empty, "task: TASK-0001-Empty") ||
		!strings.Contains(empty, "claims: (none -- this TASK has no auto-claim binding)") {
		t.Fatalf("empty printPlan output:\n%s", empty)
	}

	populated := captureStdout(t, func() {
		printPlan("TASK-0002-Bound", []string{"ci-green", "release-ready"})
	})
	for _, want := range []string{"task: TASK-0002-Bound", "claims:", "  - ci-green", "  - release-ready"} {
		if !strings.Contains(populated, want) {
			t.Fatalf("populated printPlan output missing %q:\n%s", want, populated)
		}
	}
}

func captureStdout(t *testing.T, run func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	original := os.Stdout
	os.Stdout = writer
	run()
	os.Stdout = original
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return string(body)
}
