package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMain isolates hook status from the developer's host settings: the
// managed Claude Code policy directory, Claude and Copilot user settings, and
// the OMP config root all point at an empty temporary tree.
func TestMain(testingMain *testing.M) {
	directory, err := os.MkdirTemp("", "reconc-hooks-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	claudeManagedSettingsDir = func() string { return filepath.Join(directory, "claude-managed") }
	for name, value := range map[string]string{
		"CLAUDE_CONFIG_DIR": filepath.Join(directory, "claude"),
		"COPILOT_HOME":      filepath.Join(directory, "copilot"),
		"PI_CONFIG_DIR":     filepath.Base(directory) + "-omp",
		"OMP_PROFILE":       "",
	} {
		if err := os.Setenv(name, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	// Status treats a wrapper that resolves no runnable binary as degraded, so
	// installed fixtures see one executable reconc on PATH, as an installed CLI
	// provides; tests of an unresolvable wrapper replace PATH.
	binDirectory := filepath.Join(directory, "bin")
	name := "reconc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.MkdirAll(binDirectory, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(binDirectory, name), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("PATH", binDirectory+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exitCode := testingMain.Run()
	if err := os.RemoveAll(directory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
