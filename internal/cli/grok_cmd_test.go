package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestHiddenGrokCompatibilityArgumentContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runGrok([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"Usage: reconc grok", "same ACP session", "Ctrl-C"} {
		if !strings.Contains(stdout.String(), token) {
			t.Fatalf("hidden Grok compatibility help omitted %q: %s", token, stdout.String())
		}
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "prompt value", args: []string{"--prompt"}, want: "--prompt requires text"},
		{name: "duplicate prompt", args: []string{"--prompt", "one", "--prompt", "two"}, want: "more than once"},
		{name: "model value", args: []string{"--model"}, want: "--model requires an ID"},
		{name: "binary value", args: []string{"--grok-binary"}, want: "--grok-binary requires a path"},
		{name: "continuation value", args: []string{"--max-continuations"}, want: "requires an integer"},
		{name: "continuation lower bound", args: []string{"--max-continuations", "0", "--prompt", "work"}, want: "at least 1"},
		{name: "continuation type", args: []string{"--max-continuations", "many", "--prompt", "work"}, want: "at least 1"},
		{name: "conflicting prompt forms", args: []string{"--prompt", "one", "--", "two"}, want: "use either"},
		{name: "unknown flag", args: []string{"--unknown"}, want: "unknown flag"},
		{name: "extra repository", args: []string{"one", "two", "--prompt", "work"}, want: "at most one repo"},
		{name: "missing prompt", args: nil, want: "missing prompt"},
		{name: "blank trailing prompt", args: []string{"--", "   "}, want: "missing prompt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			err := runGrok(test.args, &stdout, &stderr)
			if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runGrok(%v) error = %v, want exit 1 containing %q", test.args, err, test.want)
			}
		})
	}
}

func TestHiddenGrokCompatibilityWrapsRuntimeFailure(t *testing.T) {
	missingRepo := filepath.Join(t.TempDir(), "missing")
	var stdout, stderr bytes.Buffer
	err := runGrok([]string{
		missingRepo,
		"--model", "grok-test",
		"--grok-binary", "missing-grok",
		"--max-continuations", "2",
		"--prompt", "inspect the repository",
	}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "reconc grok:") {
		t.Fatalf("runtime failure was not wrapped as an exit-1 CLI error: %v", err)
	}
}
