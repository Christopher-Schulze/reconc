//go:build !windows

package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeCommandDoesNotSourceLoginProfileBeforeDecisionJSON(t *testing.T) {
	repo := t.TempDir()
	wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nprintf '%s\\n' '{\"decision\":\"allow\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("printf '%s\\n' 'profile-output-must-not-run'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	commandText := runtimeCommand(repo, "cursor-pre-tool-use")
	if !strings.HasPrefix(commandText, "sh -c '") || strings.Contains(commandText, "sh -lc") {
		t.Fatalf("runtime command is not an explicit non-login shell: %s", commandText)
	}
	command := exec.Command("sh", "-c", commandText)
	command.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "{\"decision\":\"allow\"}\n" {
		t.Fatalf("login profile polluted decision output: %q", output)
	}
}
