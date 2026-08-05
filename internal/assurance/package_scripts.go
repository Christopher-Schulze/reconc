package assurance

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/bmatcuk/doublestar/v4"
	"reconc.dev/reconc/internal/policy"
)

var packageScriptIgnoredDirectories = map[string]bool{
	".git": true, ".next": true, ".reconc": true, ".svelte-kit": true,
	"build": true, "coverage": true, "dist": true, "generated": true,
	"examples": true, "fixtures": true, "node_modules": true, "out": true,
	"target": true, "testdata": true, "vendor": true, "__fixtures__": true,
}

type packageScriptDocument struct {
	PackageManager string            `json:"packageManager"`
	Scripts        map[string]string `json:"scripts"`
}

type packageScriptCommand struct {
	runner string
	script string
	base   string
}

func evaluatePackageScripts(root string, gate policy.AssuranceGate, successful []string, state *evaluationState) ([]Finding, error) {
	commandsByScript := map[string][]packageScriptCommand{}
	for _, command := range gate.Commands {
		parsed, err := policy.ParsePackageScriptCommand(command)
		if err != nil {
			return nil, err
		}
		entry := packageScriptCommand{runner: parsed.Runner, script: parsed.Script, base: strings.Join(strings.Fields(command), " ")}
		commandsByScript[entry.script] = append(commandsByScript[entry.script], entry)
	}
	manifests, err := state.matchingPackageManifests(root, gate.ManifestPaths, gate.ExcludePaths)
	if err != nil {
		return nil, err
	}
	successSet := stringSetNormalized(successful)
	findings := []Finding{}
	for _, manifest := range manifests {
		document, err := state.packageDocument(manifest.full)
		if err != nil {
			findings = append(findings, Finding{
				GateID: gate.ID, Paths: []string{manifest.relative},
				Message:     fmt.Sprintf("package manifest %s is not valid JSON: %v", manifest.relative, err),
				Remediation: "Fix the malformed package.json or exclude an intentional fixture path from this package_scripts gate.",
			})
			continue
		}
		if len(gate.ManifestMarkers) > 0 {
			matches, err := state.manifestDirectoryHasMarker(filepath.Dir(manifest.full), gate.ManifestMarkers)
			if err != nil {
				return nil, fmt.Errorf("inspect package markers for %s: %w", manifest.relative, err)
			}
			if !matches {
				continue
			}
		}
		managers, err := packageManagersForManifest(root, filepath.Dir(manifest.full), document.PackageManager, state)
		if err != nil {
			return nil, fmt.Errorf("resolve package manager for %s: %w", manifest.relative, err)
		}
		if len(managers) > 1 {
			findings = append(findings, Finding{
				GateID: gate.ID, Paths: []string{manifest.relative},
				Message:     "package manager is ambiguous for " + manifest.relative + ": " + strings.Join(managers, ", "),
				Remediation: "Keep one package-manager declaration or lockfile in this package boundary, then rerun the policy check.",
			})
			continue
		}
		if gate.PackageManager != "" && (len(managers) != 1 || managers[0] != gate.PackageManager) {
			continue
		}
		scriptNames := make([]string, 0, len(commandsByScript))
		for script := range commandsByScript {
			scriptNames = append(scriptNames, script)
		}
		sort.Strings(scriptNames)
		for _, script := range scriptNames {
			body, declared := document.Scripts[script]
			if !declared {
				continue
			}
			if strings.TrimSpace(body) == "" {
				findings = append(findings, Finding{GateID: gate.ID, Paths: []string{manifest.relative}, Message: fmt.Sprintf("declared package script %q is empty in %s", script, manifest.relative), Remediation: "Define a real script command or remove the empty script declaration."})
				continue
			}
			candidates := packageScriptCandidates(root, manifest.full, commandsByScript[script])
			matched := false
			for _, candidate := range candidates {
				if successSet[normalizeCommand(candidate)] {
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			findings = append(findings, Finding{
				GateID: gate.ID, Paths: []string{manifest.relative},
				Message:     fmt.Sprintf("declared package script %q has no current successful evidence: %s", script, strings.Join(candidates, ", ")),
				Remediation: "Run one listed command successfully in the current session, then rerun the policy check.",
			})
		}
	}
	return findings, nil
}

func (state *evaluationState) matchingPackageManifests(root string, patterns, excludePatterns []string) ([]changedFile, error) {
	if err := state.validateGatePatterns(patterns, excludePatterns, nil); err != nil {
		return nil, err
	}
	cacheKey := stringSlicesKey(patterns, excludePatterns)
	if cached, ok := state.packageManifests[cacheKey]; ok {
		return cached, nil
	}
	manifests := []changedFile{}
	visited := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		visited++
		if visited > maxWalkEntries {
			return fmt.Errorf("package manifest walk budget exceeded: %d > %d", visited, maxWalkEntries)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if packageScriptIgnoredDirectories[strings.ToLower(entry.Name())] {
				return fs.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if matchAnyUnvalidated(excludePatterns, relative) {
			return nil
		}
		if !matchAnyUnvalidated(patterns, relative) {
			return nil
		}
		if len(manifests) >= maxScannedFiles {
			return fmt.Errorf("package manifest file budget exceeded: %d", maxScannedFiles)
		}
		manifests = append(manifests, changedFile{relative: relative, full: path})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].relative < manifests[j].relative })
	state.packageManifests[cacheKey] = manifests
	return manifests, nil
}

func (state *evaluationState) manifestDirectoryHasMarker(directory string, patterns []string) (bool, error) {
	key := directory + "\x00" + stringSlicesKey(patterns)
	if cached, ok := state.manifestMarkers[key]; ok {
		return cached, nil
	}
	matched, err := manifestDirectoryHasMarker(directory, patterns)
	if err == nil {
		state.manifestMarkers[key] = matched
	}
	return matched, err
}

func manifestDirectoryHasMarker(directory string, patterns []string) (bool, error) {
	for _, pattern := range patterns {
		if !doublestar.ValidatePattern(filepath.ToSlash(pattern)) {
			return false, doublestar.ErrBadPattern
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if matchAnyUnvalidated(patterns, entry.Name()) {
			return true, nil
		}
	}
	return false, nil
}

func packageManagersForManifest(root, directory, metadata string, state *evaluationState) ([]string, error) {
	managers, err := packageManagerSignals(directory, metadata)
	if err != nil {
		return nil, err
	}
	if len(managers) > 0 {
		return managers, nil
	}
	root = filepath.Clean(root)
	for parent := filepath.Dir(directory); pathWithinRoot(root, parent); parent = filepath.Dir(parent) {
		inheritedMetadata, err := inheritedPackageManagerMetadata(filepath.Join(parent, "package.json"), state)
		if err != nil {
			return nil, err
		}
		managers, err := packageManagerSignals(parent, inheritedMetadata)
		if err != nil {
			return nil, err
		}
		if len(managers) > 0 {
			return managers, nil
		}
		if parent == root {
			break
		}
	}
	return nil, nil
}

func inheritedPackageManagerMetadata(path string, state *evaluationState) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) || err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	document, err := state.packageDocument(path)
	if err != nil {
		return "", nil
	}
	return document.PackageManager, nil
}

func packageManagerSignals(directory, metadata string) ([]string, error) {
	managers := map[string]bool{}
	name := strings.ToLower(strings.TrimSpace(metadata))
	if separator := strings.IndexByte(name, '@'); separator >= 0 {
		name = name[:separator]
	}
	if name == "bun" || name == "npm" || name == "pnpm" || name == "yarn" {
		managers[name] = true
	}
	locks := map[string]string{
		"bun.lock": "bun", "bun.lockb": "bun", "package-lock.json": "npm",
		"npm-shrinkwrap.json": "npm", "pnpm-lock.yaml": "pnpm", "yarn.lock": "yarn",
	}
	for filename, manager := range locks {
		info, err := os.Lstat(filepath.Join(directory, filename))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			managers[manager] = true
		}
	}
	out := make([]string, 0, len(managers))
	for manager := range managers {
		out = append(out, manager)
	}
	sort.Strings(out)
	return out, nil
}

func pathWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func packageScriptCandidates(root, manifest string, commands []packageScriptCommand) []string {
	directory := filepath.Dir(manifest)
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == "." {
		out := make([]string, 0, len(commands))
		for _, command := range commands {
			out = append(out, command.base)
		}
		return out
	}
	path := shellQuotePackagePath(filepath.ToSlash(relative))
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		flag := "--cwd"
		if command.runner == "npm" {
			flag = "--prefix"
		} else if command.runner == "pnpm" {
			flag = "--dir"
		}
		out = append(out, fmt.Sprintf("%s %s %s run %s", command.runner, flag, path, command.script))
	}
	return out
}

func shellQuotePackagePath(path string) string {
	for _, character := range path {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || strings.ContainsRune("._/-", character) {
			continue
		}
		return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	}
	return path
}
