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
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/testtemp"
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
		if err == nil {
			err = configureCLITestProcess(source, parent, state)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(testingMain.Run())
	}
	os.Exit(testtemp.MainWithSetup(testingMain, func() (func() error, error) {
		return stageCLITestBinary(source, name)
	}))
}

// stageCLITestBinary copies the test binary as name into a fresh directory
// that the package's tests use as staged CLI and state; the returned cleanup
// removes that directory.
func stageCLITestBinary(source, name string) (cleanup func() error, err error) {
	directory, err := os.MkdirTemp("", "reconc-cli-test-*")
	if err != nil {
		return nil, err
	}
	cleanup = func() error { return os.RemoveAll(directory) }
	body, err := os.ReadFile(source)
	if err != nil {
		return cleanup, err
	}
	if err := os.WriteFile(filepath.Join(directory, name), body, 0o755); err != nil {
		return cleanup, err
	}
	return cleanup, configureCLITestProcess(source, directory, directory)
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
// state and the user-global Kimi Code and Codex homes at state, a directory
// owned by this process.
func configureCLITestProcess(source, staged, state string) error {
	stagedCLITestDirectory = staged
	values := map[string]string{
		"RECONC_INSTALL_DIR": staged,
		"RECONC_HOME":        filepath.Join(state, "state"),
		"KIMI_CODE_HOME":     filepath.Join(state, "kimi-code"),
		"CODEX_HOME":         filepath.Join(state, "codex"),
		"PATH":               staged + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	for name, value := range values {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	newHookVerificationChildCommand = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		// Children get an isolated HOME without the Go build cache, so the
		// worker is built here first, where the cache is warm.
		if _, err := stageDSHVerificationWorker(staged); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		childArgs := append([]string{"-test.run=^TestHookVerificationIsolatedChild$", cliTestChildFlag(), "--"}, args...)
		return exec.CommandContext(ctx, source, childArgs...)
	}
	dshVerificationWorker = func() (string, error) {
		return stageDSHVerificationWorker(staged)
	}
	return nil
}

// The generated DSH extension executes its worker directly, which the test
// binary cannot serve, so a real Reconc build is staged once per package run.
var (
	stageDSHVerificationWorkerOnce sync.Once
	stagedDSHVerificationWorkerErr error
)

func stageDSHVerificationWorker(staged string) (string, error) {
	worker := filepath.Join(staged, "reconc-dsh-worker")
	if runtime.GOOS == "windows" {
		worker += ".exe"
	}
	stageDSHVerificationWorkerOnce.Do(func() {
		if info, err := os.Stat(worker); err == nil && info.Mode().IsRegular() {
			return
		}
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			stagedDSHVerificationWorkerErr = fmt.Errorf("resolve the DSH verification worker source")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		build := exec.CommandContext(ctx, "go", "build", "-o", worker, "./cmd/reconc")
		build.Dir = filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
		if output, err := build.CombinedOutput(); err != nil {
			stagedDSHVerificationWorkerErr = fmt.Errorf("build the DSH verification worker: %w: %s", err, output)
		}
	})
	return worker, stagedDSHVerificationWorkerErr
}
