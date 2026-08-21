// Package stackdetect provides deterministic, read-only repository stack
// discovery shared by bootstrap and adopt.
package stackdetect

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	maxDepth            = 6
	maxEntries          = 100_000
	maxEvidencePerStack = 8
	maxPackageJSONBytes = 1 << 20
)

var ignoredDirectories = map[string]bool{
	".git": true, ".gradle": true, ".idea": true, ".next": true,
	".reconc": true, ".svelte-kit": true, ".venv": true, ".zig-cache": true,
	"_build": true, "build": true, "coverage": true, "deps": true,
	"dist": true, "generated": true, "node_modules": true, "obj": true,
	"out": true, "target": true, "vendor": true, "venv": true,
	"zig-out": true,
}

// Result contains sorted stack names and bounded, sorted repository-relative
// evidence for each stack.
type Result struct {
	Stacks            []string            `json:"stacks"`
	Evidence          map[string][]string `json:"evidence"`
	PackageManagers   map[string][]string `json:"package_managers"`
	RepositoryMarkers []string            `json:"repository_markers"`
	Ambiguities       []string            `json:"ambiguities"`
}

// Detect scans conventional manifests and source extensions without following
// symlinks or entering dependency/build trees. The bounded depth covers normal
// monorepo layouts while preventing repository inspection from becoming an
// unbounded filesystem crawl.
func Detect(root string) (Result, error) {
	info, err := os.Stat(root)
	if err != nil {
		return Result{}, fmt.Errorf("inspect stack root: %w", err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("inspect stack root is not a directory: %s", root)
	}
	root, err = pathidentity.ResolveExisting(root)
	if err != nil {
		return Result{}, fmt.Errorf("resolve stack root filesystem identity: %w", err)
	}
	root = filepath.Clean(root)

	evidence := map[string][]string{}
	packageManagers := map[string][]string{}
	repositoryMarkers := []string{}
	inspectionWarnings := []string{}
	entries := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > maxEntries {
			return fmt.Errorf("stack detection entry budget exceeded: %d", maxEntries)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("resolve stack evidence path %s: %w", path, err)
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		depth := pathDepth(relative)
		if isRepositoryMarker(relative) && entry.Type()&os.ModeSymlink == 0 {
			repositoryMarkers = append(repositoryMarkers, relative)
		}
		if entry.IsDir() {
			if ignoredDirectories[strings.ToLower(entry.Name())] || depth >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if depth > maxDepth || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		stacks, err := stacksForFile(path, entry)
		if err != nil {
			inspectionWarnings = append(inspectionWarnings, relative+": "+err.Error())
		}
		for _, stack := range stacks {
			appendBoundedEvidence(evidence, stack, relative)
		}
		if manager := packageManagerForFile(path, entry); manager != "" {
			appendBoundedEvidence(packageManagers, manager, relative)
		}
		if strings.EqualFold(entry.Name(), "package.json") {
			for _, manager := range []string{"bun", "npm", "pnpm", "yarn"} {
				if containsStack(stacks, manager) {
					appendBoundedEvidence(packageManagers, manager, relative)
				}
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("detect repository stacks: %w", err)
	}

	stacks := make([]string, 0, len(evidence))
	for stack := range evidence {
		stacks = append(stacks, stack)
		sort.Strings(evidence[stack])
	}
	for manager := range packageManagers {
		sort.Strings(packageManagers[manager])
	}
	sort.Strings(stacks)
	sort.Strings(repositoryMarkers)
	sort.Strings(inspectionWarnings)
	ambiguities := append(packageManagerAmbiguities(packageManagers), inspectionWarnings...)
	return Result{
		Stacks: stacks, Evidence: evidence, PackageManagers: packageManagers,
		RepositoryMarkers: repositoryMarkers, Ambiguities: ambiguities,
	}, nil
}

func appendBoundedEvidence(target map[string][]string, name, relative string) {
	if len(target[name]) >= maxEvidencePerStack {
		return
	}
	for _, existing := range target[name] {
		if existing == relative {
			return
		}
	}
	target[name] = append(target[name], relative)
}

func packageManagerForFile(path string, entry fs.DirEntry) string {
	switch strings.ToLower(entry.Name()) {
	case "bun.lock", "bun.lockb":
		if regularFile(filepath.Join(filepath.Dir(path), "package.json")) {
			return "bun"
		}
	case "package-lock.json", "npm-shrinkwrap.json":
		return "npm"
	case "pnpm-lock.yaml":
		return "pnpm"
	case "yarn.lock":
		return "yarn"
	case "uv.lock":
		return "uv"
	case "poetry.lock":
		return "poetry"
	case "pipfile.lock":
		return "pipenv"
	case "requirements.txt":
		return "pip"
	case "cargo.lock":
		return "cargo"
	case "go.mod":
		return "go-modules"
	case "composer.lock":
		return "composer"
	case "gradlew", "gradlew.bat":
		return "gradle"
	case "mvnw", "mvnw.cmd":
		return "maven"
	case "mix.lock":
		return "mix"
	case "build.zig.zon":
		return "zig"
	}
	return ""
}

func isRepositoryMarker(relative string) bool {
	switch strings.ToLower(filepath.ToSlash(relative)) {
	case ".git", ".reconc", ".reconc.yml", "agents.md", "claude.md", "start.md",
		"docs/tasks.md", "docs/documentation.md":
		return true
	default:
		return false
	}
}

func packageManagerAmbiguities(managers map[string][]string) []string {
	nodeManagers := map[string]bool{"bun": true, "npm": true, "pnpm": true, "yarn": true}
	byDirectory := map[string]map[string]bool{}
	for manager, paths := range managers {
		if !nodeManagers[manager] {
			continue
		}
		for _, relative := range paths {
			directory := "."
			if index := strings.LastIndex(relative, "/"); index >= 0 {
				directory = relative[:index]
			}
			if byDirectory[directory] == nil {
				byDirectory[directory] = map[string]bool{}
			}
			byDirectory[directory][manager] = true
		}
	}
	directories := make([]string, 0, len(byDirectory))
	for directory := range byDirectory {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	ambiguities := []string{}
	for _, directory := range directories {
		if len(byDirectory[directory]) < 2 {
			continue
		}
		names := make([]string, 0, len(byDirectory[directory]))
		for name := range byDirectory[directory] {
			names = append(names, name)
		}
		sort.Strings(names)
		ambiguities = append(ambiguities, "multiple JavaScript package managers at "+directory+": "+strings.Join(names, ", "))
	}
	return ambiguities
}

func stacksForFile(path string, entry fs.DirEntry) ([]string, error) {
	name := entry.Name()
	lowerName := strings.ToLower(name)
	extension := strings.ToLower(filepath.Ext(name))
	stacks := []string{}
	add := func(stack string) {
		for _, existing := range stacks {
			if existing == stack {
				return
			}
		}
		stacks = append(stacks, stack)
	}

	switch lowerName {
	case "go.mod":
		add("go")
	case "cargo.toml":
		add("rust")
	case "pyproject.toml", "requirements.txt", "setup.cfg", "setup.py":
		add("python")
	case ".shellcheckrc":
		add("shell")
	case "cmakelists.txt", "meson.build":
		add("cpp")
	case "pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts":
		add("java")
	case "composer.json", "phpunit.xml", "phpunit.xml.dist":
		add("php")
	case "build.zig", "build.zig.zon":
		add("zig")
	case "mix.exs", "mix.lock":
		add("elixir")
	case "bun.lock", "bun.lockb":
		if regularFile(filepath.Join(filepath.Dir(path), "package.json")) {
			add("bun")
		}
	case "package-lock.json", "npm-shrinkwrap.json":
		if regularFile(filepath.Join(filepath.Dir(path), "package.json")) {
			add("npm")
		}
	case "pnpm-lock.yaml":
		if regularFile(filepath.Join(filepath.Dir(path), "package.json")) {
			add("pnpm")
		}
	case "yarn.lock":
		if regularFile(filepath.Join(filepath.Dir(path), "package.json")) {
			add("yarn")
		}
	case "package.json":
		add("javascript")
		frameworks, err := packageFrameworks(path, entry)
		if err != nil {
			return stacks, err
		}
		for _, framework := range frameworks {
			add(framework)
		}
	default:
		if lowerName == "tsconfig.json" || (strings.HasPrefix(lowerName, "tsconfig.") && strings.HasSuffix(lowerName, ".json")) {
			add("typescript")
		}
	}

	switch extension {
	case ".sh", ".bash", ".zsh", ".ksh":
		add("shell")
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx":
		add("cpp")
	case ".java":
		add("java")
	case ".php":
		add("php")
	case ".cs", ".csproj":
		add("csharp")
	case ".zig":
		add("zig")
	case ".ex", ".exs":
		add("elixir")
	case ".ps1", ".psm1", ".psd1":
		add("powershell")
	}
	sort.Strings(stacks)
	return stacks, nil
}

type packageManifest struct {
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PackageManager       string            `json:"packageManager"`
}

func packageFrameworks(path string, entry fs.DirEntry) ([]string, error) {
	info, err := entry.Info()
	if err != nil {
		return nil, fmt.Errorf("inspect package manifest %s: %w", path, err)
	}
	if info.Size() > maxPackageJSONBytes {
		return nil, fmt.Errorf("package manifest %s exceeds %d bytes", path, maxPackageJSONBytes)
	}
	body, err := boundedio.ReadRegularFile(path, maxPackageJSONBytes)
	if err != nil {
		return nil, fmt.Errorf("read package manifest %s: %w", path, err)
	}
	var manifest packageManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("parse package manifest %s: %w", path, err)
	}
	frameworks := []string{}
	for _, dependencies := range []map[string]string{
		manifest.Dependencies,
		manifest.DevDependencies,
		manifest.PeerDependencies,
		manifest.OptionalDependencies,
	} {
		if _, ok := dependencies["next"]; ok {
			frameworks = appendUnique(frameworks, "nextjs")
		}
		if _, ok := dependencies["svelte"]; ok {
			frameworks = appendUnique(frameworks, "svelte")
		}
		if _, ok := dependencies["@sveltejs/kit"]; ok {
			frameworks = appendUnique(frameworks, "svelte")
		}
	}
	manager := strings.ToLower(strings.TrimSpace(manifest.PackageManager))
	if separator := strings.IndexByte(manager, '@'); separator >= 0 {
		manager = manager[:separator]
	}
	switch manager {
	case "bun", "npm", "pnpm", "yarn":
		frameworks = appendUnique(frameworks, manager)
	}
	sort.Strings(frameworks)
	return frameworks, nil
}

func containsStack(stacks []string, target string) bool {
	for _, stack := range stacks {
		if stack == target {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func pathDepth(relative string) int {
	// Detect converts repository-relative paths to slash form before applying
	// the platform-independent scan bound.
	return strings.Count(relative, "/") + 1
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}
