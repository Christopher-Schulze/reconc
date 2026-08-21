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
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
)

type evaluationState struct {
	budget           *scanBudget
	paths            map[string]resolvedPath
	facts            map[string]*fileFacts
	changedPaths     []normalizedChangedPath
	changedFiles     map[string][]changedFile
	applicability    map[string]bool
	validatedGlobs   map[string]bool
	patternMatches   map[string]*patternMatchBits
	packageManifests map[string][]changedFile
	manifestMarkers  map[string]bool
	observations     map[string]string
	analysisWorkers  int
	stats            analysisCounters
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

type normalizedChangedPath struct {
	relative  string
	extension string
}

type patternMatchBits struct {
	known   []uint64
	matched []uint64
}

type scanBudget struct {
	files     map[string]bool
	byteFiles map[string]bool
	bytes     int64
}

func newEvaluationState(changed []string, workerLimit int) *evaluationState {
	if workerLimit < 1 {
		workerLimit = 1
	}
	if workerLimit > maxAnalysisWorkers {
		workerLimit = maxAnalysisWorkers
	}
	state := &evaluationState{
		budget:           newScanBudget(),
		paths:            map[string]resolvedPath{},
		facts:            map[string]*fileFacts{},
		changedFiles:     map[string][]changedFile{},
		applicability:    map[string]bool{},
		validatedGlobs:   map[string]bool{},
		patternMatches:   map[string]*patternMatchBits{},
		packageManifests: map[string][]changedFile{},
		manifestMarkers:  map[string]bool{},
		observations:     map[string]string{},
		analysisWorkers:  workerLimit,
	}
	seen := make(map[string]bool, len(changed))
	for _, raw := range changed {
		relative := filepath.ToSlash(filepath.Clean(raw))
		if seen[relative] {
			continue
		}
		seen[relative] = true
		state.changedPaths = append(state.changedPaths, normalizedChangedPath{
			relative:  relative,
			extension: strings.ToLower(filepath.Ext(relative)),
		})
	}
	sort.Slice(state.changedPaths, func(i, j int) bool {
		return state.changedPaths[i].relative < state.changedPaths[j].relative
	})
	return state
}

func (state *evaluationState) recordObservation(key, value string) {
	state.observations[key] = value
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

func changedFiles(root string, includes, excludes []string, exemptions []policy.AssuranceExemption, state *evaluationState) ([]changedFile, error) {
	return changedFilesByExtension(root, includes, excludes, exemptions, "", state)
}

func changedGoFiles(root string, includes, excludes []string, exemptions []policy.AssuranceExemption, state *evaluationState) ([]changedFile, error) {
	baseKey := changedFilesKey(includes, excludes, exemptions, "")
	cacheKey := changedFilesKey(includes, excludes, exemptions, ".go")
	if cached, ok := state.changedFiles[cacheKey]; ok {
		return cached, nil
	}
	if base, ok := state.changedFiles[baseKey]; ok {
		files := make([]changedFile, 0, len(base))
		for _, file := range base {
			if strings.EqualFold(filepath.Ext(file.relative), ".go") {
				files = append(files, file)
			}
		}
		state.changedFiles[cacheKey] = files
		return files, nil
	}
	return changedFilesByExtension(root, includes, excludes, exemptions, ".go", state)
}

func changedFilesByExtension(root string, includes, excludes []string, exemptions []policy.AssuranceExemption, requiredExtension string, state *evaluationState) ([]changedFile, error) {
	if err := state.validateGatePatterns(includes, excludes, exemptions); err != nil {
		return nil, err
	}
	cacheKey := changedFilesKey(includes, excludes, exemptions, requiredExtension)
	if cached, ok := state.changedFiles[cacheKey]; ok {
		return cached, nil
	}
	files := []changedFile{}
	for changedIndex, changed := range state.changedPaths {
		if requiredExtension != "" && changed.extension != requiredExtension {
			continue
		}
		relative := changed.relative
		included := state.matchChanged(includes, changedIndex)
		if !included {
			continue
		}
		excluded := state.matchChanged(excludes, changedIndex)
		exempt := state.changedPathExempt(changedIndex, exemptions)
		if excluded || exempt {
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
		files = append(files, changedFile{relative: relative, full: resolved.full})
	}
	state.changedFiles[cacheKey] = files
	return files, nil
}

func changedFilesKey(includes, excludes []string, exemptions []policy.AssuranceExemption, requiredExtension string) string {
	paths := make([]string, len(exemptions))
	for index, exemption := range exemptions {
		paths[index] = exemption.Path
	}
	return stringSlicesKey(includes, excludes, paths, []string{requiredExtension})
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
	for _, pattern := range patterns {
		if !doublestar.ValidatePattern(filepath.ToSlash(pattern)) {
			return false, doublestar.ErrBadPattern
		}
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
		if matchAnyUnvalidated(patterns, filepath.ToSlash(relative)) {
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
	state.stats.pathResolutions.Add(1)
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
	return state.fact(path).body(state)
}

func readBounded(path string, budget *scanBudget) ([]byte, error) {
	body, info, err := boundedio.ReadRegularFileSnapshot(path, maxFileBytes)
	if err != nil {
		return nil, err
	}
	if info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	if err := budget.observeFile(path); err != nil {
		return nil, err
	}
	if err := budget.observeBytesForFile(path, info.Size()); err != nil {
		return nil, err
	}
	return body, nil
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

func matchAnyUnvalidated(patterns []string, relative string) bool {
	relative = filepath.ToSlash(relative)
	for _, pattern := range patterns {
		if doublestar.MatchUnvalidated(filepath.ToSlash(pattern), relative) {
			return true
		}
	}
	return false
}

func (state *evaluationState) validateGatePatterns(includes, excludes []string, exemptions []policy.AssuranceExemption) error {
	patterns := make([]string, 0, len(includes))
	patterns = append(patterns, includes...)
	patterns = append(patterns, excludes...)
	for _, exemption := range exemptions {
		patterns = append(patterns, exemption.Path)
	}
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(pattern)
		if valid, known := state.validatedGlobs[pattern]; known {
			if !valid {
				return doublestar.ErrBadPattern
			}
			continue
		}
		valid := doublestar.ValidatePattern(pattern)
		state.validatedGlobs[pattern] = valid
		if !valid {
			return doublestar.ErrBadPattern
		}
	}
	return nil
}

func (state *evaluationState) matchChanged(patterns []string, changedIndex int) bool {
	relative := state.changedPaths[changedIndex].relative
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(pattern)
		bits := state.patternMatches[pattern]
		if bits == nil {
			wordCount := (len(state.changedPaths) + 63) / 64
			bits = &patternMatchBits{known: make([]uint64, wordCount), matched: make([]uint64, wordCount)}
			state.patternMatches[pattern] = bits
		}
		word := changedIndex / 64
		mask := uint64(1) << uint(changedIndex%64)
		if bits.known[word]&mask == 0 {
			state.stats.pathMatches.Add(1)
			bits.known[word] |= mask
			if doublestar.MatchUnvalidated(pattern, relative) {
				bits.matched[word] |= mask
			}
		}
		if bits.matched[word]&mask != 0 {
			return true
		}
	}
	return false
}

func (state *evaluationState) changedPathExempt(changedIndex int, exemptions []policy.AssuranceExemption) bool {
	for _, exemption := range exemptions {
		if state.matchChanged([]string{exemption.Path}, changedIndex) {
			return true
		}
	}
	return false
}

func newScanBudget() *scanBudget {
	return &scanBudget{files: map[string]bool{}, byteFiles: map[string]bool{}}
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

func (budget *scanBudget) observeBytesForFile(path string, count int64) error {
	if budget.byteFiles[path] {
		return nil
	}
	budget.byteFiles[path] = true
	return budget.observeBytes(count)
}
