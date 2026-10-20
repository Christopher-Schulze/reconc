// Package gitexec creates hermetic Git inspection processes.
package gitexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
)

const gitCancellationWait = 250 * time.Millisecond

// ObjectDirectories permits the repository-sync snapshot writer to isolate
// new objects while reading existing repository objects through an alternate.
type ObjectDirectories struct {
	ObjectDirectory            string
	AlternateObjectDirectories string
}

// CommandContext returns a Git command whose repository authority, config,
// hooks, prompts, locale, and lock behavior cannot be redirected by ambient
// Git environment variables. directory may be empty for repository-independent
// commands. ObjectDirectories must be nil unless the caller explicitly owns an
// isolated object store.
func CommandContext(
	ctx context.Context,
	directory string,
	objects *ObjectDirectories,
	args ...string,
) *exec.Cmd {
	gitArgs := make([]string, 0, len(args)+7)
	gitArgs = append(gitArgs,
		"--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false",
		"-c", "core.hooksPath=",
	)
	gitArgs = append(gitArgs, args...)
	command := exec.CommandContext(ctx, gitProgram(), gitArgs...)
	command.Dir = directory
	command.Env = hermeticEnvironment(directory, objects)
	configureGitCommand(command)
	return command
}

// ConfigAwareCommandContext returns a bounded Git command that retains the
// effective repository configuration. It is reserved for commands whose
// purpose is to inspect that configuration itself.
func ConfigAwareCommandContext(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, gitProgram(), args...)
	configureGitCommand(command)
	return command
}

func hermeticEnvironment(directory string, objects *ObjectDirectories) []string {
	environment := make([]string, 0, len(os.Environ())+9)
	for _, entry := range os.Environ() {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") || strings.EqualFold(key, "LC_ALL") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"LC_ALL=C",
	)
	if index, ok := commitIndexFile(directory); ok {
		environment = append(environment, "GIT_INDEX_FILE="+index)
	}
	if objects == nil {
		return environment
	}
	overrides := map[string]string{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": objects.AlternateObjectDirectories,
		"GIT_OBJECT_DIRECTORY":             objects.ObjectDirectory,
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+overrides[key])
	}
	return environment
}

// commitIndexFile keeps the one ambient redirection Git itself applies while
// running hooks: `git commit -a`, `-i`, `-o` and pathspec commits stage into a
// temporary index named by GIT_INDEX_FILE, and that index, not .git/index, is
// what the commit records. Only a regular file named index, index.lock or
// next-index-*.lock directly inside the Git directory of the checkout that
// contains directory is accepted, so the variable cannot point a gate at an
// arbitrary index. Git runs hooks at the checkout root, so a relative value
// is resolved there, also for a policy root below it.
func commitIndexFile(directory string) (string, bool) {
	value := strings.TrimSpace(os.Getenv("GIT_INDEX_FILE"))
	if value == "" || directory == "" {
		return "", false
	}
	name := filepath.Base(value)
	if name != "index" && name != "index.lock" && !(strings.HasPrefix(name, "next-index-") && strings.HasSuffix(name, ".lock")) {
		return "", false
	}
	checkout, gitDir, ok := enclosingRepositoryGitDir(directory)
	if !ok {
		return "", false
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(checkout, value)
	}
	info, err := os.Lstat(value)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(value))
	if err != nil || parent != gitDir {
		return "", false
	}
	return filepath.Join(parent, name), true
}

// enclosingRepositoryGitDir finds the nearest directory at or above directory
// that holds a .git entry and resolves its Git directory.
func enclosingRepositoryGitDir(directory string) (string, string, bool) {
	current, err := filepath.Abs(directory)
	if err != nil {
		return "", "", false
	}
	for {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			gitDir, ok := repositoryGitDir(current)
			return current, gitDir, ok
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", false
		}
		current = parent
	}
}

// repositoryGitDir resolves directory/.git as a directory or a `gitdir:` file.
func repositoryGitDir(directory string) (string, bool) {
	dotGit := filepath.Join(directory, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return "", false
	}
	target := dotGit
	if info.Mode().IsRegular() {
		body, readErr := boundedio.ReadRegularFile(dotGit, 4096)
		if readErr != nil {
			return "", false
		}
		pointer, found := strings.CutPrefix(strings.TrimSpace(string(body)), "gitdir: ")
		if !found {
			return "", false
		}
		target = pointer
		if !filepath.IsAbs(target) {
			target = filepath.Join(directory, target)
		}
	} else if !info.IsDir() {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", false
	}
	return resolved, true
}
