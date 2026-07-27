package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestBlockPrintsDiagnosticAndExitsTwo(t *testing.T) {
	if os.Getenv("RECONC_TEST_BLOCK_HELPER") == "1" {
		block("blocked %d", 7)
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestBlockPrintsDiagnosticAndExitsTwo$")
	command.Env = append(os.Environ(), "RECONC_TEST_BLOCK_HELPER=1")
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("block exit = %v, output=%q", err, output)
	}
	if !strings.Contains(string(output), "blocked 7") {
		t.Fatalf("block output = %q", output)
	}
}
