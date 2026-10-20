package agentsession

import (
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/shellcommand"
)

const maxCommandWriteTargetDepth = 16

// literalCommandWritePaths returns the repository files a shell command
// writes through literal targets, as absolute paths, so write policy judges
// them before the command runs. A relative target is resolved against the
// call's working directory and dropped after a `cd`; targets outside the
// repository and dynamic targets keep their after-execution enforcement.
func literalCommandWritePaths(root string, payload *HookPayload) []string {
	if payload == nil || !payload.IsCommandTool() {
		return nil
	}
	targets := shellcommand.LiteralWriteTargets(payload.Command(), maxCommandWriteTargetDepth)
	base := commandWorkingDirectory(root, payload)
	paths := make([]string, 0, len(targets.Paths))
	for _, target := range targets.Paths {
		if !filepath.IsAbs(target) {
			if targets.ChangesDirectory {
				continue
			}
			target = filepath.Join(base, target)
		}
		target = canonicalWriteTarget(target)
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		paths = append(paths, target)
	}
	sort.Strings(paths)
	return paths
}

// commandWorkingDirectory is the absolute directory a command call names
// inside the repository, or the repository root.
func commandWorkingDirectory(root string, payload *HookPayload) string {
	candidates := []string{}
	if payload.ToolInput != nil {
		for _, key := range []string{"workdir", "cwd"} {
			if value, _ := payload.ToolInput[key].(string); value != "" {
				candidates = append(candidates, value)
			}
		}
	}
	if value, _ := payload.Raw["cwd"].(string); value != "" {
		candidates = append(candidates, value)
	}
	for _, candidate := range candidates {
		if !filepath.IsAbs(candidate) {
			continue
		}
		directory := canonicalWriteTarget(candidate)
		relative, err := filepath.Rel(root, directory)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return directory
		}
	}
	return root
}

// canonicalWriteTarget resolves symlinks in the existing part of path, so a
// target spelled through an alias of the repository root is still inside it.
func canonicalWriteTarget(path string) string {
	path = filepath.Clean(path)
	if resolved, err := pathidentity.ResolveExisting(path); err == nil {
		return resolved
	}
	if parent, err := pathidentity.ResolveExisting(filepath.Dir(path)); err == nil {
		return filepath.Join(parent, filepath.Base(path))
	}
	return path
}
