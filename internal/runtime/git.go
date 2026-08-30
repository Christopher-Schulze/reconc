package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/gitexec"
)

const (
	gitDiffTimeout        = 15 * time.Second
	maxGitDiffOutputBytes = 4 << 20
)

// GitMode names which kind of diff was requested.
type GitMode string

const (
	GitModeStaged GitMode = "staged"
	GitModeRange  GitMode = "range"
)

// GitDiffMetadata describes which git command produced the write paths.
// Surfaced in the CheckReport so users can audit "this is what we
// looked at".
type GitDiffMetadata struct {
	Mode           GitMode `json:"mode"`
	Base           string  `json:"base,omitempty"`
	Head           string  `json:"head,omitempty"`
	GitCommand     string  `json:"git_command"`
	WritePathCount int     `json:"write_path_count"`
}

// CollectGitWritePaths shells out to git to produce a list of changed
// files relative to the repo root. POSIX-style paths returned (matches
// what the evaluator expects).
//
// Modes:
//   - staged=true:                 git diff --cached --name-only
//   - staged=false, base+head set: git diff <base>...<head> --name-only
//   - both staged and base/head -> error (caller must pick one)
//
// head defaults to "HEAD" when base is set but head is empty.
//
// On any git failure (binary missing, repo not a git repo, ref not
// found), returns *GitError with the underlying stderr.
func CollectGitWritePaths(repoRoot string, staged bool, base, head string) ([]string, GitDiffMetadata, error) {
	if staged && (base != "" || head != "") {
		return nil, GitDiffMetadata{}, &rerrors.GitError{
			Message: "specify either --staged OR --base/--head, not both",
		}
	}
	if !staged && base == "" {
		return nil, GitDiffMetadata{}, &rerrors.GitError{
			Message: "git diff requires either --staged or --base <ref> [--head <ref>]",
		}
	}

	// -z emits verbatim NUL-terminated path bytes. Without it git
	// quotes and octal-escapes non-ASCII or special filenames (per
	// core.quotepath), and those escaped strings silently match no
	// policy glob.
	var args []string
	var commandStr string
	mode := GitModeStaged
	resolvedHead := head
	if staged {
		args = []string{"diff", "--cached", "--name-only", "-z"}
		commandStr = "git diff --cached --name-only -z"
	} else {
		if resolvedHead == "" {
			resolvedHead = "HEAD"
		}
		mode = GitModeRange
		spec := fmt.Sprintf("%s...%s", base, resolvedHead)
		args = []string{"diff", spec, "--name-only", "-z"}
		commandStr = "git diff " + spec + " --name-only -z"
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitDiffTimeout)
	defer cancel()
	cmd := gitexec.CommandContext(ctx, repoRoot, nil, args...)
	stdout, err := boundedexec.NewBuffer(maxGitDiffOutputBytes)
	if err != nil {
		return nil, GitDiffMetadata{}, &rerrors.GitError{Message: "initialize bounded git stdout: " + err.Error(), Cause: err}
	}
	stderr, err := boundedexec.NewBuffer(maxGitDiffOutputBytes)
	if err != nil {
		return nil, GitDiffMetadata{}, &rerrors.GitError{Message: "initialize bounded git stderr: " + err.Error(), Cause: err}
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, GitDiffMetadata{}, &rerrors.GitError{Message: commandStr + " timed out", Cause: ctx.Err()}
	}
	if stdout.Truncated() || stderr.Truncated() {
		return nil, GitDiffMetadata{}, &rerrors.GitError{Message: fmt.Sprintf("%s output exceeds %d bytes", commandStr, maxGitDiffOutputBytes)}
	}
	if err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		msg := commandStr + " failed"
		if stderrText != "" {
			msg += ": " + stderrText
		}
		return nil, GitDiffMetadata{}, &rerrors.GitError{Message: msg, Cause: err}
	}

	// Parse output: NUL-terminated records, possibly one empty
	// trailing record. Path bytes are verbatim (git already returns
	// POSIX-style paths) and must not be trimmed.
	records := strings.Split(stdout.String(), "\x00")
	paths := make([]string, 0, len(records))
	for _, record := range records {
		if record == "" {
			continue
		}
		paths = append(paths, record)
	}

	metadata := GitDiffMetadata{
		Mode:           mode,
		Base:           base,
		Head:           resolvedHead,
		GitCommand:     commandStr,
		WritePathCount: len(paths),
	}
	if staged {
		metadata.Base = ""
		metadata.Head = ""
	}
	return paths, metadata, nil
}
