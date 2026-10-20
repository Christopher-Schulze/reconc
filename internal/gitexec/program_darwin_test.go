//go:build darwin

package gitexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeDeveloperGit(t *testing.T, directory string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{directory}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDeveloperLayoutResolvesTheGitTheShimWouldRun(t *testing.T) {
	tools := t.TempDir()
	toolsGit := writeDeveloperGit(t, tools, "usr", "bin", "git")
	xcodeApp := t.TempDir()
	xcode := filepath.Join(xcodeApp, "Contents", "Developer")
	xcodeGit := writeDeveloperGit(t, xcode, "usr", "bin", "git")
	selected := t.TempDir()
	toolchainGit := writeDeveloperGit(t, selected, "Toolchains", "XcodeDefault.xctoolchain", "usr", "bin", "git")
	writeDeveloperGit(t, selected, "usr", "bin", "git")
	missing := filepath.Join(t.TempDir(), "missing")
	for _, test := range []struct {
		name   string
		layout developerLayout
		want   string
	}{
		{"only command line tools", developerLayout{xcode: missing, tools: tools}, toolsGit},
		{"environment names the developer directory", developerLayout{environment: xcode, xcode: missing, tools: tools}, xcodeGit},
		{"environment names the Xcode application", developerLayout{environment: xcodeApp, xcode: missing, tools: tools}, xcodeGit},
		{"xcode-select selection with a default toolchain", developerLayout{selected: selected, xcode: xcode, tools: tools}, toolchainGit},
		{"environment wins over the selection", developerLayout{environment: xcode, selected: selected, tools: tools}, xcodeGit},
		{"unselected Xcode is left to the shim", developerLayout{xcode: xcode, tools: tools}, ""},
		{"relative environment is left to the shim", developerLayout{environment: "Developer", tools: tools}, ""},
		{"relative selection is left to the shim", developerLayout{selected: "Developer", tools: tools}, ""},
		{"selected directory without git", developerLayout{selected: missing, tools: tools}, ""},
		{"no developer directory", developerLayout{xcode: missing, tools: missing}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.layout.git(); got != test.want {
				t.Fatalf("git() = %q, want %q", got, test.want)
			}
		})
	}
	if err := os.Chmod(toolsGit, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (developerLayout{xcode: missing, tools: tools}).git(); got != "" {
		t.Fatalf("non-executable developer git = %q", got)
	}
}

// When git resolves to the /usr/bin/git shim, commands run the program the
// shim would execute, which answers exactly like the shim.
func TestCommandContextBypassesTheXcrunShim(t *testing.T) {
	if resolved, err := exec.LookPath("git"); err != nil || resolved != "/usr/bin/git" {
		t.Skipf("git resolves to %q, not the xcrun shim", resolved)
	}
	command := CommandContext(context.Background(), "", nil, "--version")
	if command.Path == "/usr/bin/git" || !filepath.IsAbs(command.Path) {
		t.Fatalf("command path = %q; want the developer-directory git", command.Path)
	}
	direct, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	shim, err := exec.Command("/usr/bin/git", "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(direct) != string(shim) {
		t.Fatalf("direct git reports %q, the shim %q", direct, shim)
	}
	t.Setenv("TOOLCHAINS", "com.example.toolchain")
	if command := CommandContext(context.Background(), "", nil, "--version"); command.Path != "/usr/bin/git" {
		t.Fatalf("a TOOLCHAINS selection must stay with the shim, got %q", command.Path)
	}
}
