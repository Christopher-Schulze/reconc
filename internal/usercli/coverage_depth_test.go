package usercli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInspectCurrentDistinguishesInstalledExecutableCurrentAndPATHReady(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("RECONC_INSTALL_DIR", directory)
	t.Setenv("PATH", t.TempDir())
	missing, err := InspectCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	if missing.Installed || missing.Executable || missing.Current || missing.PathVisible || missing.Ready ||
		!strings.Contains(missing.NextAction, "install-cli") {
		t.Fatalf("missing status = %+v", missing)
	}

	body, err := readBoundedBinary(missing.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(missing.TargetPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	nonExecutable, err := InspectCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	if !nonExecutable.Installed || !nonExecutable.Current {
		t.Fatalf("non-executable status lost installed/current truth: %+v", nonExecutable)
	}
	if runtime.GOOS != "windows" && nonExecutable.Executable {
		t.Fatalf("non-executable POSIX target reported executable: %+v", nonExecutable)
	}

	if err := os.Chmod(missing.TargetPath, 0o755); err != nil {
		t.Fatal(err)
	}
	otherDirectory := t.TempDir()
	other := filepath.Join(otherDirectory, executableName())
	if err := os.WriteFile(other, []byte("other"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", otherDirectory)
	wrongPath, err := InspectCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	if !wrongPath.Installed || !wrongPath.Executable || !wrongPath.Current || !wrongPath.PathVisible || wrongPath.Ready ||
		!strings.Contains(wrongPath.NextAction, "before") {
		t.Fatalf("wrong-PATH status = %+v", wrongPath)
	}
}

func TestInstallDirectoryResolutionAndFilesystemGuards(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "nested")
	resolved, err := resolveInstallDirectory("  " + explicit + "  ")
	if err != nil || resolved != filepath.Clean(explicit) {
		t.Fatalf("explicit directory = %q, %v", resolved, err)
	}
	envDirectory := filepath.Join(t.TempDir(), "bin")
	t.Setenv("RECONC_INSTALL_DIR", envDirectory)
	if resolved, err := resolveInstallDirectory(""); err != nil || resolved != envDirectory {
		t.Fatalf("environment directory = %q, %v", resolved, err)
	}
	t.Setenv("RECONC_INSTALL_DIR", "")
	if resolved := defaultInstallDirectory(); resolved == "" || !filepath.IsAbs(resolved) {
		t.Fatalf("default directory = %q", resolved)
	}

	created := filepath.Join(t.TempDir(), "one", "two")
	if err := ensureRealDirectory(created); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(created); err != nil || !info.IsDir() {
		t.Fatalf("created directory = %v, %v", info, err)
	}
	if err := ensureRealDirectory(created); err != nil {
		t.Fatalf("existing directory rejected: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureRealDirectory(file); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("file directory error = %v", err)
	}
}

func TestUserCLIBoundedReadersAndNextActionsFailPrecisely(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := readBoundedBinary(missing); err == nil || !strings.Contains(err.Error(), "open running Reconc binary") {
		t.Fatalf("missing binary read error = %v", err)
	}
	if _, err := fileSHA256(missing); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("missing checksum error = %v", err)
	}
	directory := filepath.Join(t.TempDir(), "custom")
	status := &Status{SourcePath: "/source/reconc", TargetPath: filepath.Join(directory, executableName())}
	if got := nextAction(status, directory); !strings.Contains(got, "--install-dir") || !strings.Contains(got, "install-cli") {
		t.Fatalf("install next action = %q", got)
	}
	status.Installed = true
	status.Executable = true
	status.Current = true
	if got := nextAction(status, directory); !strings.Contains(got, "PATH") {
		t.Fatalf("PATH next action = %q", got)
	}
	status.PathVisible = true
	status.ResolvedPath = filepath.Join(t.TempDir(), executableName())
	if got := nextAction(status, directory); !strings.Contains(got, "before") {
		t.Fatalf("precedence next action = %q", got)
	}
	status.Ready = true
	if got := nextAction(status, directory); !strings.Contains(got, "current") {
		t.Fatalf("ready next action = %q", got)
	}
	if got := quote("a'b"); got != `'a'\''b'` {
		t.Fatalf("quote = %q", got)
	}
}
