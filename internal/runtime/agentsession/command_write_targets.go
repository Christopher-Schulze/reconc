package agentsession

import (
	"os"
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
// repository and dynamic targets keep their after-execution enforcement. A
// copy-like write inside a destination counts unless that destination exists
// and is not a directory: a regular file destination is written itself, and no
// path below it can exist. Symlinks count as what they point to.
func literalCommandWritePaths(root string, payload *HookPayload) []string {
	if payload == nil || !payload.IsCommandTool() {
		return nil
	}
	targets := shellcommand.LiteralWriteTargets(payload.Command(), maxCommandWriteTargetDepth)
	base := commandWorkingDirectory(root, payload)
	absolute := func(target string) (string, bool) {
		if filepath.IsAbs(target) {
			return target, true
		}
		return filepath.Join(base, target), !targets.ChangesDirectory
	}
	candidates := make([]string, 0, len(targets.Paths)+len(targets.DirectoryEntries))
	for _, target := range targets.Paths {
		if path, ok := absolute(target); ok {
			candidates = append(candidates, path)
		}
	}
	for _, entry := range targets.DirectoryEntries {
		directory, ok := absolute(entry.Directory)
		if !ok {
			continue
		}
		if info, err := os.Stat(directory); err == nil && !info.IsDir() {
			continue
		}
		if path, ok := absolute(entry.Path); ok {
			candidates = append(candidates, path)
		}
	}
	paths := make([]string, 0, len(candidates))
	for _, target := range candidates {
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
