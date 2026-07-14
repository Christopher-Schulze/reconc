package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHookWrapperDevBinarySkipsPlatformDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper execution is covered on POSIX hosts")
	}
	repo := t.TempDir()
	wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte(GenerateWrapper().Content), 0o755); err != nil {
		t.Fatal(err)
	}
	devBinary := filepath.Join(repo, ".build", "bin", "reconc")
	if err := os.MkdirAll(filepath.Dir(devBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(devBinary, []byte("#!/bin/sh\nprintf 'dev|%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "uname-called")
	binDir := t.TempDir()
	uname := filepath.Join(binDir, "uname")
	if err := os.WriteFile(uname, []byte("#!/bin/sh\nprintf called > \"$UNAME_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", wrapper, "codex-stop", repo)
	command.Env = append(os.Environ(), "PATH="+binDir, "UNAME_MARKER="+marker)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("wrapper: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "dev|hook runtime codex-stop") {
		t.Fatalf("development binary did not win: %s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("development hotpath must not invoke uname: %v", err)
	}
}
