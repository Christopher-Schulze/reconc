package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPGONearestRankPercentile(t *testing.T) {
	python := os.Getenv("PYTHON")
	if python == "" {
		python = "python3"
	}
	script := filepath.Join("..", "pgo_stats.py")
	compile := exec.Command(python, "-m", "py_compile", script)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("py_compile pgo_stats.py: %v\n%s", err, output)
	}
	command := exec.Command(python, script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("pgo_stats.py: %v\n%s", err, output)
	}
}

func TestComparisonRejectsAliasedOutputs(t *testing.T) {
	directory := t.TempDir()
	baselinePath := filepath.Join(directory, "baseline.json")
	resultPath := filepath.Join(directory, "result.json")
	result := syntheticResult()
	baseline, err := refreshBaseline(result)
	if err != nil {
		t.Fatal(err)
	}
	writeTestContract(t, resultPath, result)
	writeTestContract(t, baselinePath, baseline)
	baselineSnap := snapshotFile(t, baselinePath)
	resultSnap := snapshotFile(t, resultPath)

	relBaseline, err := filepath.Rel(directory, baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		output string
		recipe bool
	}{
		{name: "same string", output: baselinePath},
		{name: "relative alias", output: relBaseline},
		{name: "cleaned alias", output: filepath.Join(directory, "nested", "..", "baseline.json")},
		{name: "recipe result alias", output: "result.json", recipe: true},
		{name: "recipe relative", output: "./result.json", recipe: true},
	}
	if err := os.Symlink(baselinePath, filepath.Join(directory, "baseline.link")); err == nil {
		tests = append(tests, struct {
			name   string
			output string
			recipe bool
		}{name: "symlink alias", output: filepath.Join(directory, "baseline.link")})
	}
	if err := os.Link(baselinePath, filepath.Join(directory, "baseline.hard")); err == nil {
		tests = append(tests, struct {
			name   string
			output string
			recipe bool
		}{name: "hardlink alias", output: filepath.Join(directory, "baseline.hard")})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(directory)
			var stdout bytes.Buffer
			var err error
			if test.recipe {
				err = runCompare([]string{
					"--recipe", "--baseline", "baseline.json", "--result", "result.json",
					"--output", test.output, "--suite", suiteVersion, "--current", result.Environment.Commit,
				}, &stdout)
			} else {
				err = runCompare([]string{"--baseline", baselinePath, "--result", resultPath, "--output", test.output}, io.Discard)
			}
			if err == nil || !strings.Contains(err.Error(), "aliases") {
				t.Fatalf("aliased output error = %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("aliased comparison emitted output: %s", stdout.String())
			}
			assertFileSnapshot(t, baselinePath, baselineSnap)
			assertFileSnapshot(t, resultPath, resultSnap)
		})
	}

	output := filepath.Join(directory, "comparison.json")
	if err := os.WriteFile(output, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCompare([]string{"--baseline", baselinePath, "--result", resultPath, "--output", output}, io.Discard); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(output)
	if err != nil || !bytes.Contains(body, []byte(`"passed"`)) {
		t.Fatalf("existing destination was not replaced: %s err=%v", body, err)
	}
	assertFileSnapshot(t, baselinePath, baselineSnap)
	assertFileSnapshot(t, resultPath, resultSnap)
}

func TestComparisonPreservesInputsWhenPublicationFails(t *testing.T) {
	directory := t.TempDir()
	baselinePath := filepath.Join(directory, "baseline.json")
	resultPath := filepath.Join(directory, "result.json")
	result := syntheticResult()
	baseline, err := refreshBaseline(result)
	if err != nil {
		t.Fatal(err)
	}
	writeTestContract(t, baselinePath, baseline)
	writeTestContract(t, resultPath, result)
	baselineSnap := snapshotFile(t, baselinePath)
	resultSnap := snapshotFile(t, resultPath)

	blockedParent := filepath.Join(directory, "blocked")
	if err := os.WriteFile(blockedParent, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(blockedParent, "comparison.json")
	if err := runCompare([]string{"--baseline", baselinePath, "--result", resultPath, "--output", output}, io.Discard); err == nil {
		t.Fatal("publication through a file parent succeeded")
	}
	assertFileSnapshot(t, baselinePath, baselineSnap)
	assertFileSnapshot(t, resultPath, resultSnap)

	t.Cleanup(func() {
		marshalComparison = encodeContract
		publishComparison = publishContract
	})
	marshalComparison = func(any) ([]byte, error) {
		return nil, errors.New("forced encode failure")
	}
	if err := runCompare([]string{"--baseline", baselinePath, "--result", resultPath, "--output", filepath.Join(directory, "encoded.json")}, io.Discard); err == nil || !strings.Contains(err.Error(), "forced encode failure") {
		t.Fatalf("encode failure = %v", err)
	}
	assertFileSnapshot(t, baselinePath, baselineSnap)
	assertFileSnapshot(t, resultPath, resultSnap)
	if _, err := os.Stat(filepath.Join(directory, "encoded.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("encode failure still created output: %v", err)
	}

	marshalComparison = encodeContract
	publishComparison = func(string, []byte, io.Writer) error {
		return errors.New("forced publish failure")
	}
	if err := runCompare([]string{"--baseline", baselinePath, "--result", resultPath, "--output", filepath.Join(directory, "published.json")}, io.Discard); err == nil || !strings.Contains(err.Error(), "forced publish failure") {
		t.Fatalf("publish failure = %v", err)
	}
	assertFileSnapshot(t, baselinePath, baselineSnap)
	assertFileSnapshot(t, resultPath, resultSnap)
}

func snapshotFile(t *testing.T, path string) fileSnapshot {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fileSnapshot{data: data, mode: info.Mode(), size: info.Size(), modTime: info.ModTime()}
}

func assertFileSnapshot(t *testing.T, path string, want fileSnapshot) {
	t.Helper()
	got := snapshotFile(t, path)
	if !bytes.Equal(got.data, want.data) || got.mode != want.mode || got.size != want.size || !got.modTime.Equal(want.modTime) {
		t.Fatalf("%s changed: mode=%v/%v size=%d/%d mtime=%v/%v", path, got.mode, want.mode, got.size, want.size, got.modTime, want.modTime)
	}
}

type fileSnapshot struct {
	data    []byte
	mode    os.FileMode
	size    int64
	modTime time.Time
}
