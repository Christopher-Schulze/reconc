package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkflowAuditRunnerUsesContentDigestInsteadOfMtime(t *testing.T) {
	testWorkflowAuditRunnerContentDigest(t, "tools/reconc/harness/template")
}

func testWorkflowAuditRunnerContentDigest(t *testing.T, harnessRel string) {
	t.Helper()
	root := t.TempDir()
	runnerBody, err := os.ReadFile("run-workflow-audit")
	if err != nil {
		t.Fatal(err)
	}
	auditsDir := filepath.Join(root, filepath.FromSlash(harnessRel), "audits")
	if err := os.MkdirAll(auditsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provenanceDir := filepath.Join(root, "tools", "reconc", "buildprovenance")
	if err := os.MkdirAll(provenanceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(auditsDir, "run-workflow-audit")
	writeRunnerFixture(t, runner, runnerBody, 0o755)
	writeRunnerFixture(t, filepath.Join(root, filepath.FromSlash(harnessRel), "go.mod"), []byte("module example.test/harness\n\ngo 1.24\n"), 0o644)
	writeRunnerFixture(t, filepath.Join(root, filepath.FromSlash(harnessRel), "go.sum"), nil, 0o644)
	source := filepath.Join(auditsDir, "main.go")
	writeRunnerFixture(t, source, []byte("package main\n\nfunc main() {}\n"), 0o644)
	writeRunnerFixture(t, filepath.Join(provenanceDir, "provenance.go"), []byte("package buildprovenance\n"), 0o644)

	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	countPath := filepath.Join(root, "build-count")
	fakeGo := []byte(`#!/bin/sh
set -eu
count=0
if [ -f "$FAKE_GO_COUNT" ]; then count="$(cat "$FAKE_GO_COUNT")"; fi
count=$((count + 1))
printf '%s\n' "$count" >"$FAKE_GO_COUNT"
out=''
previous=''
for argument in "$@"; do
  if [ "$previous" = '-o' ]; then out="$argument"; break; fi
  previous="$argument"
done
[ -n "$out" ]
printf '#!/bin/sh\nexit 0\n' >"$out"
chmod 755 "$out"
`)
	writeRunnerFixture(t, filepath.Join(fakeBin, "go"), fakeGo, 0o755)

	run := func() {
		t.Helper()
		command := exec.Command("sh", runner, "all")
		command.Dir = root
		command.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_GO_COUNT="+countPath)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("run workflow audit launcher: %v\n%s", err, output)
		}
	}
	buildCount := func() string {
		t.Helper()
		body, err := os.ReadFile(countPath)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(body))
	}

	run()
	if got := buildCount(); got != "1" {
		t.Fatalf("first run built %s times, want 1", got)
	}
	writeRunnerFixture(t, source, []byte("package main\n\nfunc main() { println(\"changed\") }\n"), 0o644)
	old := time.Unix(1, 0)
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}
	run()
	if got := buildCount(); got != "2" {
		t.Fatalf("content change with older mtime built %s times, want 2", got)
	}
	run()
	if got := buildCount(); got != "2" {
		t.Fatalf("unchanged sources rebuilt cache: got %s builds, want 2", got)
	}
}

func writeRunnerFixture(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
}
