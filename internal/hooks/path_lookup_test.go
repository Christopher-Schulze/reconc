package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The PATH fallback selects what `command -v reconc` selects, the first
// executable regular file on PATH, without forking a command-substitution
// subshell on every hook event.
func TestHookLaunchersResolveReconcFromPathWithoutSubshell(t *testing.T) {
	for name, content := range map[string]string{
		"wrapper":    GenerateWrapper().Content,
		"pre-commit": generateGitPreCommit().Content,
	} {
		if strings.Contains(content, "$(command -v") {
			t.Errorf("the %s forks a subshell for its PATH lookup", name)
		}
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("generated POSIX launcher execution requires a POSIX host")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required to run the generated pre-commit: %v", err)
	}
	repo := t.TempDir()
	if output, err := exec.Command(git, "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	preCommit := filepath.Join(repo, ".git", "hooks", "pre-commit")
	for path, content := range map[string]string{wrapper: GenerateWrapper().Content, preCommit: generateGitPreCommit().Content} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeReconc := func(mode os.FileMode) string {
		directory := t.TempDir()
		script := "#!/bin/sh\nprintf 'trusted|%s\\n' \"$*\"\n"
		if err := os.WriteFile(filepath.Join(directory, "reconc"), []byte(script), mode); err != nil {
			t.Fatal(err)
		}
		return directory
	}
	empty := t.TempDir()
	notExecutable := writeReconc(0o644)
	trusted := writeReconc(0o755)
	linked := t.TempDir()
	if err := os.Symlink(filepath.Join(trusted, "reconc"), filepath.Join(linked, "reconc")); err != nil {
		t.Fatal(err)
	}
	launch := func(argv []string, path ...string) (string, error) {
		command := exec.Command("sh", argv...)
		command.Dir = repo
		command.Env = []string{"PATH=" + strings.Join(append(path, filepath.Dir(git), "/usr/bin", "/bin"), ":")}
		output, err := command.CombinedOutput()
		return string(output), err
	}
	for name, launcher := range map[string]struct {
		argv []string
		want string
	}{
		"wrapper":    {argv: []string{wrapper, "claude-pre-tool-use", repo}, want: "trusted|hook runtime claude-pre-tool-use " + repo},
		"pre-commit": {argv: []string{preCommit}, want: "trusted|ci "},
	} {
		output, err := launch(launcher.argv, empty, notExecutable, trusted, linked)
		if err != nil || !strings.HasPrefix(output, launcher.want) {
			t.Fatalf("%s with an executable reconc on PATH: err=%v output=%q, want prefix %q", name, err, output, launcher.want)
		}
		// The first executable match decides; a symlinked one fails closed
		// instead of falling through to a later PATH entry.
		output, err = launch(launcher.argv, linked, trusted)
		if err == nil || !strings.Contains(output, "no executable Reconc binary found") {
			t.Fatalf("%s with a symlinked first PATH match: err=%v output=%q", name, err, output)
		}
	}
}
