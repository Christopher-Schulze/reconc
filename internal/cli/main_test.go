package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// The -reconc-cli-test-directory flag names the directory the package run
// staged. Every re-executed test child gets it through cliTestChildFlag,
// reuses that binary instead of staging its own copy, and keeps its state
// under that directory, which the parent removes even when a child exits
// early or is killed. A flag, unlike an environment variable, also reaches
// children whose environment is rebuilt from an allowlist.
var cliTestDirectoryFlag = flag.String("reconc-cli-test-directory", "", "staged directory of the parent test process")

// stagedCLITestDirectory is the staged directory this process uses.
var stagedCLITestDirectory string

// cliTestChildFlag is the argument a re-executed test child needs to reuse
// the staged binary.
func cliTestChildFlag() string {
	return "-reconc-cli-test-directory=" + stagedCLITestDirectory
}

func TestMain(testingMain *testing.M) {
	flag.Parse()
	// Package tests act as the operator; TestOperatorAdministrationRequires*
	// restore and exercise the real interactive confirmation.
	confirmOperatorPresence = func(string, io.Writer) error { return nil }
	source, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := "reconc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if parent := stagedParentDirectory(name); parent != "" {
		state, err := os.MkdirTemp(parent, "child-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		configureCLITestProcess(source, parent, state)
		os.Exit(testingMain.Run())
	}
	directory, err := os.MkdirTemp("", "reconc-cli-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	body, err := os.ReadFile(source)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(directory, name), body, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	configureCLITestProcess(source, directory, directory)
	exitCode := testingMain.Run()
	if err := os.RemoveAll(directory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

// stagedParentDirectory returns the parent's staged directory when this
// process is a re-executed child and that binary still exists.
func stagedParentDirectory(name string) string {
	parent := *cliTestDirectoryFlag
	if parent == "" {
		return ""
	}
	if info, err := os.Stat(filepath.Join(parent, name)); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return parent
}

// configureCLITestProcess puts the staged binary on PATH and points Reconc
// and host state at state, a directory owned by this process.
func configureCLITestProcess(source, staged, state string) {
	stagedCLITestDirectory = staged
	values := map[string]string{
		"RECONC_INSTALL_DIR": staged,
		"RECONC_HOME":        filepath.Join(state, "state"),
		"PATH":               staged + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	for name, value := range values {
		if err := os.Setenv(name, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	newHookVerificationChildCommand = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		childArgs := append([]string{"-test.run=^TestHookVerificationIsolatedChild$", cliTestChildFlag(), "--"}, args...)
		return exec.CommandContext(ctx, source, childArgs...)
	}
}
