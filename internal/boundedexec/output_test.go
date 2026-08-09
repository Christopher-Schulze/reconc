package boundedexec

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCombinedOutputCapsChildOutput(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestBoundedExecHelper$")
	command.Env = append(os.Environ(), "RECONC_BOUNDED_EXEC_HELPER=combined")
	output, err := CombinedOutput(command, 1024)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("expected output-limit error, got %v", err)
	}
	if len(output) != 1024 {
		t.Fatalf("combined output length=%d, want 1024", len(output))
	}
}

func TestOutputReturnsExactStdout(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestBoundedExecHelper$")
	command.Env = append(os.Environ(), "RECONC_BOUNDED_EXEC_HELPER=stdout")
	output, err := Output(command, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "stdout" {
		t.Fatalf("output=%q", output)
	}
}

func TestBoundedExecHelper(t *testing.T) {
	switch os.Getenv("RECONC_BOUNDED_EXEC_HELPER") {
	case "combined":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 768))
		_, _ = os.Stderr.WriteString(strings.Repeat("y", 768))
		os.Exit(0)
	case "stdout":
		_, _ = os.Stdout.WriteString("stdout")
		_, _ = os.Stderr.WriteString("stderr")
		os.Exit(0)
	}
}
