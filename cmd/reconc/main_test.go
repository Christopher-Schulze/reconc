package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMainVersionSuccess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess", "--", "--version")
	cmd.Env = append(os.Environ(),
		"GO_WANT_MAIN_HELPER=1",
		"RECONC_TEST_VERSION=0.4.0-test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("main --version failed: %v\n%s", err, string(out))
	}
	if got := string(out); got != "reconc 0.4.0-test\n" {
		t.Fatalf("unexpected version output: %q", got)
	}
}

func TestMainErrorExitCodeAndStderr(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess", "--", "definitely-not-a-real-subcommand")
	cmd.Env = append(os.Environ(),
		"GO_WANT_MAIN_HELPER=1",
		"RECONC_TEST_VERSION=0.4.0-test",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected main to exit non-zero on unknown subcommand")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.ExitCode())
	}
	if !strings.Contains(string(out), "run `reconc --help` for the current surface") {
		t.Fatalf("expected stderr to point to current help surface, got %q", string(out))
	}
}

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MAIN_HELPER") != "1" {
		return
	}
	Version = os.Getenv("RECONC_TEST_VERSION")
	args := []string{}
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	os.Args = append([]string{"reconc"}, args...)
	main()
	os.Exit(0)
}
