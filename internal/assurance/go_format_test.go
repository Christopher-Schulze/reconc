package assurance

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestGoFormatChecksOnlyChangedMatchingGoFiles(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "src/formatted.go", "package src\n\nfunc formatted() {}\n")
	writeAssuranceFile(t, root, "src/unformatted.go", "package src\nfunc unformatted( ){ }\n")
	writeAssuranceFile(t, root, "src/ignored.txt", "not Go\n")
	gate := policy.AssuranceGate{
		ID: "go-format", Type: policy.AssuranceGoFormat,
		ScanPaths: []string{"src/**"},
	}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths: []string{"src/formatted.go", "src/unformatted.go", "src/ignored.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Paths[0] != "src/unformatted.go" {
		t.Fatalf("expected one unformatted Go finding, got %+v", findings)
	}

	gate.ExcludePaths = []string{"src/unformatted.go"}
	findings, err = Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"src/unformatted.go"}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("excluded Go file should not be checked: findings=%+v err=%v", findings, err)
	}
}

func TestGoFormatFailsClosedOnInvalidSource(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "broken.go", "package broken\nfunc")
	gate := policy.AssuranceGate{ID: "go-format", Type: policy.AssuranceGoFormat, ScanPaths: []string{"**/*.go"}}

	_, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"broken.go"}})
	if err == nil || !strings.Contains(err.Error(), "format Go source broken.go") {
		t.Fatalf("invalid changed Go source must fail closed, got %v", err)
	}
}
