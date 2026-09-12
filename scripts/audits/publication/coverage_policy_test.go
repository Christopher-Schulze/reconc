package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverageReviewOnlyClauses(t *testing.T) {
	c := "coverage"
	tests := []struct {
		name, text string
		reject     bool
	}{
		{"measurement", "Historical whole-module " + c + ": 82.3484% root and 84.0628% template.", false},
		{"negated-prefix", "No minimum " + c + " is required and measured " + c + " was 82.3484%.", false},
		{"negated-suffix", c + " is not required to reach 80%; the measurement was 82.3484%.", false},
		{"negated-amount", "Minimum " + c + " of 80% is not required; measured " + c + " was 82.3484%.", false},
		{"negated-verb", "We do not require " + c + " to reach 80%.", false},
		{"unrelated-gate", "The integration gate failed; measured " + c + " was 82.3484%.", false},
		{"positive", c + " must reach 80%.", true},
		{"double-negation", c + " must not fall below 80%.", true},
		{"mixed-clause", "No minimum " + c + " is required and " + c + " must reach 80%.", true},
		{"mixed-amount", "Minimum " + c + " of 80% is not required and " + c + " must reach 85%.", true},
		{"numeric-before", "Require at least 80% " + c + ".", true},
		{"config", "No minimum " + c + " is required; " + strings.ToUpper(c) + "_MIN=80", true},
		{"nested-config", c + ": {minimum: 80}", true},
		{"comparison", c + " >= 80", true},
		{"shell-comparison", "if " + c + " -lt 80; then exit 1; fi", true},
		{"invalid-bytes", "\xe0\x01 " + c + " must reach 80%.", true},
		{"nul", "\x00 " + c + " must reach 80%.", true},
		{"crlf", strings.ToUpper(c) + " MUST REACH 80 PERCENT.\r\n", true},
		{"byte-distance-within", c + " must " + strings.Repeat("é", 38) + "80%", true},
		{"byte-distance-outside", c + " must " + strings.Repeat("é", 42) + "80%", false},
		{"do-not-then-positive", "We do not require " + c + " to reach 80% but " + c + " must reach 85%", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := coverageRequirementLine([]byte(test.text)); got != test.reject {
				t.Fatalf("rejection=%v, want %v: %q", got, test.reject, test.text)
			}
		})
	}
}

func TestCoverageReviewOnlyLongJSONLine(t *testing.T) {
	// Exercise the long single-record shape that triggered the native awk error,
	// without retaining private source-cache contents in the regression fixture.
	c := "coverage"
	measurement := `{"source":"` + strings.Repeat("node ", 7200) + " " + c + ` after the integration gate: 82.3484%."}`
	if err := scanCoverageReader(context.Background(), "cache.json", strings.NewReader(measurement)); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, 64 << 10, 1 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			text := strings.Repeat("x", size) + "\n" + c + " must reach 80%"
			var finding *coveragePolicyError
			err := scanCoverageReader(context.Background(), "long.md", strings.NewReader(text))
			if !errors.As(err, &finding) || finding.Line != 2 {
				t.Fatalf("finding=%v", err)
			}
		})
	}
}

func TestCoverageReviewOnlyTreeAndCLI(t *testing.T) {
	root := t.TempDir()
	c := "coverage"
	for _, path := range []string{".git/private.md", ".build/generated.go", ".reconc/state.json", "dist/report.md", "scripts/tests/release-trust.sh", "ignored.txt"} {
		writeAuditFixture(t, root, path, c+" must reach 80%")
	}
	writeAuditFixture(t, root, "README.md", "Measured "+c+": 82.3484%\n")
	writeAuditFixture(t, root, "graphify-out/cache/ast/result.json", `{"source":"public"}`)
	var output bytes.Buffer
	if err := runCLI([]string{"--coverage-review-only", "--root", root}, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "ok (2 files)") {
		t.Fatal(output.String())
	}
	for _, path := range []string{"graphify-out/cache/ast/result.json", "nested/.build/control.md", "Makefile", "source.go", "config.yml", "config.yaml", "config.toml", "run.sh"} {
		t.Run(path, func(t *testing.T) {
			fixture := t.TempDir()
			writeAuditFixture(t, fixture, path, c+" must reach 80%")
			output.Reset()
			err := runCLI([]string{"--coverage-review-only", "--root", fixture}, &output, io.Discard)
			var policy *coveragePolicyError
			if !errors.As(err, &policy) || auditExitCode(err) != 1 || output.Len() != 0 {
				t.Fatalf("err=%v output=%s", err, &output)
			}
		})
	}
}

func TestCoverageReviewOnlyFailures(t *testing.T) {
	root := t.TempDir()
	writeAuditFixture(t, root, "file.md", "review evidence")
	for _, args := range [][]string{
		{"--coverage-review-only", "--", filepath.Join(root, "missing.md")},
		{"--coverage-review-only", "--root", filepath.Join(root, "absent")},
		{"--coverage-review-only", "--", root},
		{"--coverage-review-only", "--root", filepath.Join(root, "file.md")},
	} {
		err := runCLI(args, io.Discard, io.Discard)
		var scan *coverageScanError
		if !errors.As(err, &scan) || auditExitCode(err) != 2 {
			t.Fatalf("args=%q err=%v", args, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanCoverageTree(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if err := scanCoverageReader(ctx, "canceled", strings.NewReader("")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	failure := errors.New("injected reader failure")
	if err := scanCoverageReader(context.Background(), "broken", coverageFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatalf("read failure=%v", err)
	}
	if err := runCoverageAudit(context.Background(), root, nil, coverageFailingWriter{failure}); !errors.Is(err, failure) || auditExitCode(err) != 2 {
		t.Fatalf("write failure=%v", err)
	}
	// A failed directory read must propagate instead of looking like an empty tree.
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o700); err != nil {
			t.Error(err)
		}
	})
	if _, probe := os.ReadDir(locked); probe != nil {
		if _, err := scanCoverageTree(context.Background(), root); err == nil {
			t.Fatal("directory read error hidden")
		}
	}
}

type coverageFailingReader struct{ err error }

func (r coverageFailingReader) Read([]byte) (int, error) { return 0, r.err }

type coverageFailingWriter struct{ err error }

func (w coverageFailingWriter) Write([]byte) (int, error) { return 0, w.err }
