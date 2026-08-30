// Package gitexec creates hermetic Git inspection processes.
package gitexec

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
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
	command := exec.CommandContext(ctx, "git", gitArgs...)
	command.Dir = directory
	command.Env = hermeticEnvironment(objects)
	configureGitCommand(command)
	return command
}

// ConfigAwareCommandContext returns a bounded Git command that retains the
// effective repository configuration. It is reserved for commands whose
// purpose is to inspect that configuration itself.
func ConfigAwareCommandContext(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "git", args...)
	configureGitCommand(command)
	return command
}

func hermeticEnvironment(objects *ObjectDirectories) []string {
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
