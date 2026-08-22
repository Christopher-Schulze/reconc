//go:build !windows

package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRenderBootstrapCommandPreservesLiteralArgvThroughPOSIXShell(t *testing.T) {
	bin := t.TempDir()
	program := filepath.Join(bin, "reconc")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf '<%s>' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	value := "quotes ' and \"; backslash \\; dollars $HOME $(touch nope); space\nnewline/"
	command := renderBootstrapCommand("reconc", "check", value)
	process := exec.Command("sh", "-c", command)
	process.Dir = bin
	process.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := process.CombinedOutput()
	if err != nil {
		t.Fatalf("execute rendered command %q: %v: %s", command, err, output)
	}
	want := "<check><" + value + ">"
	if string(output) != want {
		t.Fatalf("rendered argv = %q, want %q (command %q)", output, want, command)
	}
	if _, err := os.Stat(filepath.Join(bin, "nope")); !os.IsNotExist(err) {
		t.Fatalf("command substitution escaped literal argv: %v", err)
	}
}
