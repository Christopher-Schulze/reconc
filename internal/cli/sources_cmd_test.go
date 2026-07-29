package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSourcesReportsDigestsWithoutBodies(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"sources", repo, "--json"}, "0.9.1-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var report sourceInspection
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Count == 0 || len(report.Sources) != report.Count {
		t.Fatalf("source report = %+v", report)
	}
	if strings.Contains(stdout.String(), "rules: []") {
		t.Fatalf("source body leaked into report: %s", stdout.String())
	}
	for _, source := range report.Sources {
		if len(source.ContentSHA256) != 64 || source.Path == "" || source.Kind == "" {
			t.Fatalf("invalid source provenance: %+v", source)
		}
	}
}

func TestSourcesRejectsExtraOperands(t *testing.T) {
	var stdout bytes.Buffer
	if err := runSources([]string{".", "."}, &stdout); err == nil {
		t.Fatal("extra repository operand must fail")
	}
}
