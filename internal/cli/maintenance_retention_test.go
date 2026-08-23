package cli

import (
	"bytes"
	"testing"

	"reconc.dev/reconc/internal/retention"
)

func TestWritePruneClassDoesNotRenderUnknownProjectionAsZero(t *testing.T) {
	var output bytes.Buffer
	writePruneClass(&output, "would prune", retention.ClassReport{
		Name:             "run-decisions",
		InspectionStatus: retention.InspectionUnknown,
	})
	if got, want := output.String(), "would prune run-decisions: inspection=unknown\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	output.Reset()
	writePruneClass(&output, "would prune", retention.ClassReport{
		Name:             "run-decisions",
		InspectionStatus: retention.InspectionComplete,
	})
	if got, want := output.String(), "would prune run-decisions: inspection=complete deleted=0 freed=0B kept=0 after=0B\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
