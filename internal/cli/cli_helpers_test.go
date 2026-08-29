package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTeeToFilePublishesCompleteOutputAtomically(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "nested", "report.txt")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, []byte("previous output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	out, closeOutput, err := teeToFile(&stdout, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("complete output\n")); err != nil {
		t.Fatal(err)
	}
	if err := closeOutput(true); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "complete output\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	body, err := os.ReadFile(outputPath)
	if err != nil || string(body) != "complete output\n" {
		t.Fatalf("published output = %q err=%v", body, err)
	}
	after, err := os.Lstat(outputPath)
	if err != nil || os.SameFile(before, after) {
		t.Fatalf("output was not atomically replaced: before=%v after=%v err=%v", before, after, err)
	}
}

func TestTeeToFilePreservesExistingOutputWhenRenderingFails(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(outputPath, []byte("previous output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	out, closeOutput, err := teeToFile(&stdout, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("partial output")); err != nil {
		t.Fatal(err)
	}
	resultErr := errors.New("render failed")
	joinOutputCloseError(&resultErr, closeOutput)
	if resultErr == nil || resultErr.Error() != "render failed" {
		t.Fatalf("render result = %v", resultErr)
	}
	body, err := os.ReadFile(outputPath)
	if err != nil || string(body) != "previous output\n" {
		t.Fatalf("existing output = %q err=%v", body, err)
	}
}

func TestTeeToFilePreservesExistingOutputWhenStdoutFails(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(outputPath, []byte("previous output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, closeOutput, err := teeToFile(failingOutputWriter{}, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("unavailable output")); err == nil {
		t.Fatal("failing stdout unexpectedly accepted output")
	}
	if err := closeOutput(true); err == nil {
		t.Fatal("failed stdout unexpectedly published output")
	}
	body, err := os.ReadFile(outputPath)
	if err != nil || string(body) != "previous output\n" {
		t.Fatalf("existing output after stdout failure = %q err=%v", body, err)
	}
}

func TestTeeToFileRejectsSymlinkWithoutChangingReferent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires optional Windows privileges")
	}
	directory := t.TempDir()
	referent := filepath.Join(directory, "referent.txt")
	outputPath := filepath.Join(directory, "report.txt")
	if err := os.WriteFile(referent, []byte("protected output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(referent, outputPath); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	out, closeOutput, err := teeToFile(&stdout, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("replacement output\n")); err != nil {
		t.Fatal(err)
	}
	if err := closeOutput(true); err == nil {
		t.Fatal("symlink output unexpectedly published")
	}
	body, err := os.ReadFile(referent)
	if err != nil || string(body) != "protected output\n" {
		t.Fatalf("symlink referent = %q err=%v", body, err)
	}
	info, err := os.Lstat(outputPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("output symlink = %v err=%v", info, err)
	}
}

func TestTeeToFileRejectsDirectoryWithoutChangingIt(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "report")
	if err := os.Mkdir(outputPath, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	out, closeOutput, err := teeToFile(&stdout, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("replacement output\n")); err != nil {
		t.Fatal(err)
	}
	if err := closeOutput(true); err == nil {
		t.Fatal("directory output unexpectedly published")
	}
	info, err := os.Stat(outputPath)
	if err != nil || !info.IsDir() {
		t.Fatalf("output directory = %v err=%v", info, err)
	}
}
