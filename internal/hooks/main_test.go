package hooks

import (
	"fmt"
	"os"
	"path/filepath"
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
	exitCode := testingMain.Run()
	if err := os.RemoveAll(directory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
