package retention

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONLDryRunDistinguishesUnknownFromSuccessfulZero(t *testing.T) {
	t.Run("inspection failure", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "decisions.jsonl")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		report := Report{}
		class := enforceJSONL("run-decisions", path, 1024, 2, true, &report)
		if class.InspectionStatus != InspectionUnknown || class.BytesFreed != 0 || class.FilesDeleted != 0 ||
			class.BytesAfter != 0 || class.FilesKept != 0 {
			t.Fatalf("failed inspection published derived values: %+v", class)
		}
		if len(report.Errors) != 1 || !strings.Contains(report.Errors[0], "inspect run-decisions") {
			t.Fatalf("inspection error = %+v", report.Errors)
		}
	})

	t.Run("legitimate zero", func(t *testing.T) {
		report := Report{}
		class := enforceJSONL("run-decisions", filepath.Join(t.TempDir(), "missing.jsonl"), 1024, 2, true, &report)
		if class.InspectionStatus != InspectionComplete || class.BytesFreed != 0 || class.FilesDeleted != 0 ||
			class.BytesBefore != 0 || class.BytesAfter != 0 || class.FilesKept != 0 || len(report.Errors) != 0 {
			t.Fatalf("successful empty inspection = %+v, errors=%v", class, report.Errors)
		}
	})
}

func TestClassReportInspectionStatusIsBackwardCompatible(t *testing.T) {
	body, err := json.Marshal(ClassReport{Name: "run-decisions", InspectionStatus: InspectionComplete})
	if err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		Name       string `json:"name"`
		BytesFreed int64  `json:"bytes_freed"`
	}
	if err := json.Unmarshal(body, &legacy); err != nil || legacy.Name != "run-decisions" || legacy.BytesFreed != 0 {
		t.Fatalf("legacy decoder = %+v, %v", legacy, err)
	}

	var current ClassReport
	if err := json.Unmarshal([]byte(`{"name":"legacy","files_kept":0,"files_deleted":0,"bytes_before":0,"bytes_after":0,"bytes_freed":0}`), &current); err != nil {
		t.Fatal(err)
	}
	if current.InspectionStatus != "" {
		t.Fatalf("legacy status must remain explicitly absent, got %q", current.InspectionStatus)
	}
}
