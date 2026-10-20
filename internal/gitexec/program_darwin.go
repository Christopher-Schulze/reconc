//go:build darwin

package gitexec

import (
	"os"
	"os/exec"
	"path/filepath"
)

// gitProgram returns the program for a Git command. On macOS /usr/bin/git is
// the xcrun shim, which resolves the active developer directory on every call
// and costs about 10 ms per process. When git resolves to that shim and its
// target is certain, the target runs directly; otherwise the shim runs.
func gitProgram() string {
	if resolved, err := exec.LookPath("git"); err != nil || resolved != "/usr/bin/git" || os.Getenv("TOOLCHAINS") != "" {
		return "git"
	}
	selected, _ := os.Readlink("/var/db/xcode_select_link")
	layout := developerLayout{
		environment: os.Getenv("DEVELOPER_DIR"),
		selected:    selected,
		xcode:       "/Applications/Xcode.app/Contents/Developer",
		tools:       "/Library/Developer/CommandLineTools",
	}
	if program := layout.git(); program != "" {
		return program
	}
	return "git"
}

// developerLayout holds the inputs xcrun uses to choose the active developer
// directory: DEVELOPER_DIR, the `xcode-select --switch` link, and the default
// Xcode and Command Line Tools locations.
type developerLayout struct {
	environment string
	selected    string
	xcode       string
	tools       string
}

// git returns the git the shim would execute, searching the default toolchain
// before the developer directory as xcrun does, or "" when that is uncertain.
func (layout developerLayout) git() string {
	directory, ok := layout.developerDirectory()
	if !ok {
		return ""
	}
	for _, candidate := range []string{
		filepath.Join(directory, "Toolchains", "XcodeDefault.xctoolchain", "usr", "bin", "git"),
		filepath.Join(directory, "usr", "bin", "git"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

// developerDirectory leaves an installed Xcode without an explicit selection
// to the shim, whose choice between it and the Command Line Tools is not
// reproduced here.
func (layout developerLayout) developerDirectory() (string, bool) {
	switch {
	case layout.environment != "":
		if !filepath.IsAbs(layout.environment) {
			return "", false
		}
		// DEVELOPER_DIR may name the Xcode application instead of its
		// Developer content directory.
		if contents := filepath.Join(layout.environment, "Contents", "Developer"); directoryExists(contents) {
			return contents, true
		}
		return layout.environment, true
	case layout.selected != "":
		return layout.selected, filepath.IsAbs(layout.selected)
	case directoryExists(layout.xcode):
		return "", false
	case directoryExists(layout.tools):
		return layout.tools, true
	default:
		return "", false
	}
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
