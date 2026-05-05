package runtime

import (
	"fmt"
	"os/exec"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
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

	var args []string
	var commandStr string
	mode := GitModeStaged
	resolvedHead := head
	if staged {
		args = []string{"diff", "--cached", "--name-only"}
		commandStr = "git diff --cached --name-only"
	} else {
		if resolvedHead == "" {
			resolvedHead = "HEAD"
		}
		mode = GitModeRange
		spec := fmt.Sprintf("%s...%s", base, resolvedHead)
		args = []string{"diff", spec, "--name-only"}
		commandStr = "git diff " + spec + " --name-only"
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		stderrText := ""
		var exitErr *exec.ExitError
		if asExitErr(err, &exitErr) {
			stderrText = strings.TrimSpace(string(exitErr.Stderr))
		}
		msg := commandStr + " failed"
		if stderrText != "" {
			msg += ": " + stderrText
		}
		return nil, GitDiffMetadata{}, &rerrors.GitError{Message: msg, Cause: err}
	}

	// Parse output: one path per line, possibly empty trailing line.
	lines := strings.Split(string(out), "\n")
	paths := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		// git already returns POSIX-style paths.
		paths = append(paths, l)
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
