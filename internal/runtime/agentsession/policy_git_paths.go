package agentsession

import (
	"fmt"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
)

// policyRootGitStatus rebases porcelain-v1 paths, which Git always emits from
// its top level, to the policy root. Outside paths stay in the status so an
// unstaged or untracked sibling still invalidates a staged-only candidate;
// dirtyPathsFromStatus excludes them from this policy's own write evidence.
func policyRootGitStatus(root, status string) (string, error) {
	checkout, _, err := enclosingCheckout(root)
	if err != nil {
		return "", err
	}
	if checkout == "" || checkout == root || status == "" {
		return status, nil
	}
	parts := strings.Split(status, "\x00")
	origin := false
	for index, part := range parts {
		if part == "" {
			continue
		}
		prefix, path := "", part
		if !origin && len(part) >= 4 && part[2] == ' ' {
			prefix, path = part[:3], part[3:]
			origin = isRenameOrCopyStatus(part[0], part[1])
		} else {
			origin = false
		}
		if pathidentity.Rooted(path) || pathidentity.EscapesLexically(path) {
			return "", fmt.Errorf("git status returned a non-relative path %q", path)
		}
		relative, err := filepath.Rel(root, filepath.Join(checkout, filepath.FromSlash(path)))
		if err != nil {
			return "", fmt.Errorf("rebase Git status path: %w", err)
		}
		parts[index] = prefix + filepath.ToSlash(relative)
	}
	return strings.Join(parts, "\x00"), nil
}
