package assurance

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
)

type evaluationState struct {
	budget        *scanBudget
	paths         map[string]resolvedPath
	bodies        map[string][]byte
	changedFiles  map[string][]changedFile
	applicability map[string]bool
}

type resolvedPath struct {
	full   string
	exists bool
	mode   os.FileMode
}

type changedFile struct {
	relative string
	full     string
}

type scanBudget struct {
	files map[string]bool
	bytes int64
}

func newEvaluationState() *evaluationState {
	return &evaluationState{
		budget:        newScanBudget(),
		paths:         map[string]resolvedPath{},
		bodies:        map[string][]byte{},
		changedFiles:  map[string][]changedFile{},
		applicability: map[string]bool{},
	}
}

func (state *evaluationState) applies(root string, patterns []string) (bool, error) {
	if len(patterns) == 0 {
		return true, nil
	}
	key := stringSlicesKey(patterns)
	if cached, ok := state.applicability[key]; ok {
		return cached, nil
	}
	globs := []string{}
	for _, pattern := range patterns {
		if strings.ContainsAny(pattern, "*?[{") {
			globs = append(globs, pattern)
			continue
		}
		resolved, err := state.resolve(root, pattern)
		if err != nil {
			return false, err
		}
		if resolved.exists {
			state.applicability[key] = true
			return true, nil
		}
	}
	if len(globs) == 0 {
		state.applicability[key] = false
		return false, nil
	}
	found, err := gateApplies(root, globs)
	if err == nil {
		state.applicability[key] = found
	}
	return found, err
}

func changedFiles(root string, changed, includes, excludes []string, exemptions []policy.AssuranceExemption, state *evaluationState) ([]changedFile, error) {
	cacheKey := changedFilesKey(includes, excludes, exemptions)
	if cached, ok := state.changedFiles[cacheKey]; ok {
		return cached, nil
	}
	files := []changedFile{}
	seen := map[string]bool{}
	for _, raw := range changed {
		relative := filepath.ToSlash(filepath.Clean(raw))
		included, err := matchAny(includes, relative)
		if err != nil {
			return nil, err
		}
		if !included {
			continue
		}
		excluded, err := matchAny(excludes, relative)
		if err != nil {
			return nil, err
		}
		exempt, err := pathExempt(relative, exemptions)
		if err != nil {
			return nil, err
		}
		if excluded || exempt || seen[relative] {
			continue
		}
		resolved, err := state.resolve(root, relative)
		if err != nil {
			return nil, err
		}
		if !resolved.exists || !resolved.mode.IsRegular() {
			continue
		}
		if err := state.budget.observeFile(resolved.full); err != nil {
			return nil, err
		}
		seen[relative] = true
		files = append(files, changedFile{relative: relative, full: resolved.full})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].relative < files[j].relative })
	state.changedFiles[cacheKey] = files
	return files, nil
}

func changedFilesKey(includes, excludes []string, exemptions []policy.AssuranceExemption) string {
	paths := make([]string, len(exemptions))
	for index, exemption := range exemptions {
		paths[index] = exemption.Path
	}
	return stringSlicesKey(includes, excludes, paths)
}

func stringSlicesKey(groups ...[]string) string {
	var builder strings.Builder
	for _, group := range groups {
		builder.WriteString(strconv.Itoa(len(group)))
		builder.WriteByte(':')
		for _, value := range group {
			builder.WriteString(strconv.Itoa(len(value)))
			builder.WriteByte(':')
			builder.WriteString(value)
		}
		builder.WriteByte('|')
	}
	return builder.String()
}

func gateApplies(root string, patterns []string) (bool, error) {
	if len(patterns) == 0 {
		return true, nil
	}
	visited := 0
	found := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		visited++
		if visited > maxWalkEntries {
			return fmt.Errorf("applicability walk budget exceeded: %d > %d", visited, maxWalkEntries)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		matched, err := matchAny(patterns, filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		if matched {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found, err
}

func canonicalRoot(root string) (string, error) {
	resolved, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root filesystem identity: %w", err)
	}
	return resolved, nil
}

func safeExistingPath(root, relative string) (string, bool, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("path escapes repository: %q", relative)
	}
	full := filepath.Join(root, clean)
	resolved, err := pathidentity.ResolveProspective(full)
	if err != nil {
		return "", false, err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("path resolves outside repository: %q", relative)
	}
	_, err = os.Lstat(full)
	if os.IsNotExist(err) {
		return resolved, false, nil
	}
	if err != nil {
		return "", false, err
	}
	return resolved, true, nil
}

func (state *evaluationState) resolve(root, relative string) (resolvedPath, error) {
	if cached, ok := state.paths[relative]; ok {
		return cached, nil
	}
	full, exists, err := safeExistingPath(root, relative)
	if err != nil {
		return resolvedPath{}, err
	}
	resolved := resolvedPath{full: full, exists: exists}
	if exists {
		info, err := os.Stat(full)
		if err != nil {
			return resolvedPath{}, err
		}
		resolved.mode = info.Mode()
	}
	state.paths[relative] = resolved
	return resolved, nil
}

func (state *evaluationState) read(path string) ([]byte, error) {
	if cached, ok := state.bodies[path]; ok {
		return cached, nil
	}
	body, err := readBounded(path, state.budget)
	if err != nil {
		return nil, err
	}
	state.bodies[path] = body
	return body, nil
}

func readBounded(path string, budget *scanBudget) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	if info.Size() > maxFileBytes {
		return nil, fmt.Errorf("file exceeds %d-byte assurance limit: %s", maxFileBytes, path)
	}
	if err := budget.observeFile(path); err != nil {
		return nil, err
	}
	if err := budget.observeBytes(info.Size()); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func readDirectoryEntries(path string, limit int) ([]os.DirEntry, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(limit + 1)
	closeErr := directory.Close()
	if readErr != nil && readErr != io.EOF {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > limit {
		return nil, fmt.Errorf("directory entry budget exceeded under %s: %d > %d", path, len(entries), limit)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func directoryHasContent(root string, budget *scanBudget) (bool, error) {
	directories := []string{root}
	visited := 0
	for len(directories) > 0 {
		last := len(directories) - 1
		path := directories[last]
		directories = directories[:last]
		directory, err := os.Open(path)
		if err != nil {
			return false, err
		}
		for {
			entries, readErr := directory.ReadDir(128)
			for _, entry := range entries {
				visited++
				if visited > maxWalkEntries {
					_ = directory.Close()
					return false, fmt.Errorf("reserved-directory walk budget exceeded under %s", root)
				}
				entryPath := filepath.Join(path, entry.Name())
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				if entry.IsDir() {
					directories = append(directories, entryPath)
					continue
				}
				if entry.Name() == ".DS_Store" || entry.Name() == ".gitkeep" || entry.Name() == ".keep" {
					continue
				}
				if err := budget.observeFile(entryPath); err != nil {
					_ = directory.Close()
					return false, err
				}
				if closeErr := directory.Close(); closeErr != nil {
					return false, closeErr
				}
				return true, nil
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = directory.Close()
				return false, readErr
			}
		}
		if err := directory.Close(); err != nil {
			return false, err
		}
	}
	return false, nil
}

func matchAny(patterns []string, relative string) (bool, error) {
	if len(patterns) == 0 {
		return false, nil
	}
	for _, pattern := range patterns {
		matched, err := doublestar.Match(filepath.ToSlash(pattern), filepath.ToSlash(relative))
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func pathExempt(relative string, exemptions []policy.AssuranceExemption) (bool, error) {
	for _, exemption := range exemptions {
		matched, err := doublestar.Match(filepath.ToSlash(exemption.Path), filepath.ToSlash(relative))
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func newScanBudget() *scanBudget {
	return &scanBudget{files: map[string]bool{}}
}

func (budget *scanBudget) observeFile(path string) error {
	if budget.files[path] {
		return nil
	}
	budget.files[path] = true
	if len(budget.files) > maxScannedFiles {
		return fmt.Errorf("assurance file budget exceeded: %d > %d", len(budget.files), maxScannedFiles)
	}
	return nil
}

func (budget *scanBudget) observeBytes(count int64) error {
	budget.bytes += count
	if budget.bytes > maxTotalBytes {
		return fmt.Errorf("assurance read budget exceeded: %d > %d bytes", budget.bytes, maxTotalBytes)
	}
	return nil
}
