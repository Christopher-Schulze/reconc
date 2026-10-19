package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFuzzRunnerPropagatesDiscoveryAndExecutionFailures(t *testing.T) {
	root := publicSurfaceRoot(t)
	for _, test := range []struct {
		name string
		fail bool
	}{
		{"packages", true},
		{"targets", true},
		{"execution", true},
		{"empty-packages", true},
		{"empty-targets", false},
		{"success", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			tool := filepath.Join(directory, "go-fixture")
			log := filepath.Join(directory, "executions")
			const fixture = `#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == list ]]; then
  [[ "$DISCOVERY_CASE" != packages ]] || exit 17
  [[ "$DISCOVERY_CASE" != empty-packages ]] || exit 0
  printf 'fixture.example/package\n'
elif [[ " $* " == *' -list '* ]]; then
  [[ "$DISCOVERY_CASE" != targets ]] || exit 18
  [[ "$DISCOVERY_CASE" != empty-targets ]] || exit 0
  printf 'FuzzSecond\nFuzzFirst\nok fixture.example/package\n'
else
  [[ "$DISCOVERY_CASE" != execution ]] || exit 19
  printf '%s\n' "$*" >> "$DISCOVERY_LOG"
fi
`
			if err := os.WriteFile(tool, []byte(fixture), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "bash", filepath.ToSlash(filepath.Join(root, "scripts/tests/run-fuzz.sh")))
			command.Env = append(os.Environ(), "GO="+filepath.ToSlash(tool), "DISCOVERY_CASE="+test.name, "DISCOVERY_LOG="+filepath.ToSlash(log),
				"FUZZ_TIME=500x", "FUZZ_MINIMIZE=10x", "FUZZ_PARALLEL=1")
			output, err := command.CombinedOutput()
			if (err != nil) != test.fail || ctx.Err() != nil {
				t.Fatalf("fuzz runner error=%v, want failure=%v; output=%s", err, test.fail, output)
			}
			if test.fail && strings.Contains(string(output), "targets passed") {
				t.Fatalf("failed discovery or execution claimed success: %s", output)
			}
			if test.name == "success" {
				body, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				want := "test -run ^$ -parallel 1 -fuzz ^FuzzFirst$ -fuzztime 500x -fuzzminimizetime 10x fixture.example/package\n" +
					"test -run ^$ -parallel 1 -fuzz ^FuzzSecond$ -fuzztime 500x -fuzzminimizetime 10x fixture.example/package\n"
				if string(body) != want+want || strings.Count(string(output), "2 targets passed") != 2 {
					t.Fatalf("fuzz target execution drifted: log=%s output=%s", body, output)
				}
			}
		})
	}
}
