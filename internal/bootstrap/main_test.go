package bootstrap

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"reconc.dev/reconc/internal/testtemp"
)

// TestMain puts one executable reconc on PATH, as the user CLI that apply
// installs provides. Verification treats a wrapper that resolves no runnable
// binary as degraded; tests of a missing CLI replace PATH themselves.
func TestMain(testingMain *testing.M) {
	os.Exit(testtemp.MainWithSetup(testingMain, stageReconcOnPath))
}

// stageReconcOnPath writes the executable reconc into a fresh directory and
// prepends it to PATH; the returned cleanup removes that directory.
func stageReconcOnPath() (cleanup func() error, err error) {
	directory, err := os.MkdirTemp("", "reconc-bootstrap-test-*")
	if err != nil {
		return nil, err
	}
	cleanup = func() error { return os.RemoveAll(directory) }
	name := "reconc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		return cleanup, err
	}
	return cleanup, os.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}
