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

	"github.com/bmatcuk/doublestar/v4"
	"github.com/pelletier/go-toml/v2"
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
	Modules           []Module            `json:"modules"`
}

// Module identifies one bounded manifest-owned project scope. Root and
// Manifest are repository-relative slash paths; WorkspaceRoot is set when a
// workspace manifest is proven to cover the module.
type Module struct {
	Root          string `json:"root"`
	Manifest      string `json:"manifest"`
	Stack         string `json:"stack"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
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
	packageManagerMembership := map[string]map[string]bool{}
	repositoryMarkers := []string{}
	inspectionWarnings := []string{}
	moduleCandidates := map[string]moduleCandidate{}
	goWorkRoots := map[string]string{}
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
		if stack, ok := moduleManifestStack(entry.Name()); ok {
			moduleCandidates[relative] = moduleCandidate{manifest: relative, root: filepath.ToSlash(filepath.Dir(relative)), stack: stack}
		}
		if strings.EqualFold(entry.Name(), "go.work") {
			goWorkRoots[filepath.ToSlash(filepath.Dir(relative))] = relative
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
			recordPackageManagerMembership(packageManagerMembership, manager, relative)
		}
		if strings.EqualFold(entry.Name(), "package.json") {
			for _, manager := range []string{"bun", "npm", "pnpm", "yarn"} {
				if containsStack(stacks, manager) {
					appendBoundedEvidence(packageManagers, manager, relative)
					recordPackageManagerMembership(packageManagerMembership, manager, relative)
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
	modules, moduleWarnings := buildModules(root, moduleCandidates, goWorkRoots)
	ambiguities := append(packageManagerAmbiguities(packageManagerMembership), inspectionWarnings...)
	ambiguities = append(ambiguities, moduleWarnings...)
	sort.Strings(ambiguities)
	return Result{
		Stacks: stacks, Evidence: evidence, PackageManagers: packageManagers,
		RepositoryMarkers: repositoryMarkers, Ambiguities: ambiguities, Modules: modules,
	}, nil
}

type moduleCandidate struct {
	manifest string
	root     string
	stack    string
}

type cargoWorkspace struct {
	Workspace struct {
		Members []string `toml:"members"`
		Exclude []string `toml:"exclude"`
	} `toml:"workspace"`
}

func moduleManifestStack(name string) (string, bool) {
	switch strings.ToLower(name) {
	case "go.mod":
		return "go", true
	case "cargo.toml":
		return "rust", true
	case "pyproject.toml", "requirements.txt", "setup.cfg", "setup.py":
		return "python", true
	case "package.json":
		return "javascript", true
	default:
		return "", false
	}
}

func buildModules(root string, candidates map[string]moduleCandidate, goWorkRoots map[string]string) ([]Module, []string) {
	ordered := make([]moduleCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].root != ordered[j].root {
			return ordered[i].root < ordered[j].root
		}
		if ordered[i].stack != ordered[j].stack {
			return ordered[i].stack < ordered[j].stack
		}
		return ordered[i].manifest < ordered[j].manifest
	})

	// Keep one deterministic manifest for a root/stack pair. Python projects
	// commonly carry both pyproject.toml and requirements.txt; pyproject is
	// the stronger project identity while the other files remain evidence.
	selected := make([]moduleCandidate, 0, len(ordered))
	seenRootStack := map[string]bool{}
	for _, candidate := range ordered {
		key := candidate.root + "\x00" + candidate.stack
		if seenRootStack[key] {
			continue
		}
		seenRootStack[key] = true
		selected = append(selected, candidate)
	}

	workspaceRoots := map[string]string{}
	cargoWorkspaces := map[string]cargoWorkspace{}
	warnings := []string{}
	for _, candidate := range selected {
		if candidate.stack != "rust" || filepath.Base(candidate.manifest) != "Cargo.toml" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(candidate.manifest))
		body, err := boundedio.ReadRegularFile(path, maxPackageJSONBytes)
		if err != nil {
			warnings = append(warnings, "module manifest "+candidate.manifest+" could not be inspected: "+err.Error())
			continue
		}
		var document cargoWorkspace
		if err := toml.Unmarshal(body, &document); err != nil {
			warnings = append(warnings, "module manifest "+candidate.manifest+" could not be inspected: "+err.Error())
			continue
		}
		if len(document.Workspace.Members) > 0 {
			cargoWorkspaces[candidate.root] = document
		}
	}
	for workspaceRoot, workspace := range cargoWorkspaces {
		for _, candidate := range selected {
			if candidate.stack == "rust" && cargoWorkspaceOwnsModule(candidate.root, workspaceRoot, workspace.Workspace.Members, workspace.Workspace.Exclude) {
				workspaceRoots[candidate.root+"\x00rust"] = workspaceRoot
			}
		}
	}
	for workspaceRoot, manifest := range goWorkRoots {
		members, err := readGoWorkspaceMembers(root, manifest, workspaceRoot)
		if err != nil {
			warnings = append(warnings, "workspace manifest "+manifest+" could not be inspected: "+err.Error())
			continue
		}
		for _, candidate := range selected {
			if candidate.stack == "go" && goWorkspaceOwnsModule(candidate.root, workspaceRoot, members) {
				workspaceRoots[candidate.root+"\x00go"] = workspaceRoot
			}
		}
	}

	modules := make([]Module, 0, len(selected))
	for _, candidate := range selected {
		workspaceRoot := workspaceRoots[candidate.root+"\x00"+candidate.stack]
		if workspaceRoot == candidate.root {
			workspaceRoot = candidate.root
		}
		modules = append(modules, Module{
			Root:          normalizedModuleRoot(candidate.root),
			Manifest:      candidate.manifest,
			Stack:         candidate.stack,
			WorkspaceRoot: normalizedWorkspaceRoot(workspaceRoot),
		})
	}
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].Root != modules[j].Root {
			return modules[i].Root < modules[j].Root
		}
		if modules[i].Stack != modules[j].Stack {
			return modules[i].Stack < modules[j].Stack
		}
		return modules[i].Manifest < modules[j].Manifest
	})
	sort.Strings(warnings)
	return modules, warnings
}

func cargoWorkspaceOwnsModule(moduleRoot, workspaceRoot string, members, excludes []string) bool {
	if moduleRoot == workspaceRoot {
		return true
	}
	if workspaceRoot != "." && !strings.HasPrefix(moduleRoot, workspaceRoot+"/") {
		return false
	}
	relative := strings.TrimPrefix(moduleRoot, workspaceRoot+"/")
	if workspaceRoot == "." {
		relative = moduleRoot
	}
	for _, pattern := range excludes {
		if doublestar.ValidatePattern(filepath.ToSlash(pattern)) && doublestar.MatchUnvalidated(filepath.ToSlash(pattern), relative) {
			return false
		}
	}
	for _, pattern := range members {
		if doublestar.ValidatePattern(filepath.ToSlash(pattern)) && doublestar.MatchUnvalidated(filepath.ToSlash(pattern), relative) {
			return true
		}
	}
	return false
}

func goWorkspaceOwnsModule(moduleRoot, workspaceRoot string, members []string) bool {
	for _, member := range members {
		if moduleRoot == member {
			return true
		}
	}
	return false
}

func readGoWorkspaceMembers(root, manifest, workspaceRoot string) ([]string, error) {
	body, err := boundedio.ReadRegularFile(filepath.Join(root, filepath.FromSlash(manifest)), maxPackageJSONBytes)
	if err != nil {
		return nil, err
	}
	members := []string{}
	inBlock := false
	for _, rawLine := range strings.Split(string(body), "\n") {
		line := rawLine
		if comment := strings.Index(line, "//"); comment >= 0 {
			line = line[:comment]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "use (" {
			inBlock = true
			continue
		}
		if inBlock && line == ")" {
			inBlock = false
			continue
		}
		if !inBlock && strings.HasPrefix(line, "use ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "use "))
		}
		if !inBlock && !strings.HasPrefix(line, "use ") && !strings.HasPrefix(line, "./") && !strings.HasPrefix(line, "../") && line != "." {
			continue
		}
		member := strings.TrimSpace(line)
		if strings.HasPrefix(member, "use ") {
			member = strings.TrimSpace(strings.TrimPrefix(member, "use "))
		}
		if member == "" || filepath.IsAbs(filepath.FromSlash(member)) {
			return nil, fmt.Errorf("invalid use path %q", member)
		}
		resolved := filepath.Clean(filepath.Join(filepath.FromSlash(workspaceRoot), filepath.FromSlash(member)))
		relative, err := filepath.Rel(".", resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("use path escapes workspace root: %q", member)
		}
		members = append(members, normalizedModuleRoot(relative))
	}
	if inBlock {
		return nil, fmt.Errorf("unterminated use block")
	}
	sort.Strings(members)
	return members, nil
}

func normalizedModuleRoot(root string) string {
	root = filepath.ToSlash(filepath.Clean(root))
	if root == "" || root == "." {
		return "."
	}
	return root
}

func normalizedWorkspaceRoot(root string) string {
	if root == "" {
		return ""
	}
	return normalizedModuleRoot(root)
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

func recordPackageManagerMembership(membership map[string]map[string]bool, manager, relative string) {
	directory := filepath.ToSlash(filepath.Dir(relative))
	if directory == "" {
		directory = "."
	}
	if membership[directory] == nil {
		membership[directory] = map[string]bool{}
	}
	membership[directory][manager] = true
}

func packageManagerAmbiguities(membership map[string]map[string]bool) []string {
	nodeManagers := map[string]bool{"bun": true, "npm": true, "pnpm": true, "yarn": true}
	directories := make([]string, 0, len(membership))
	for directory := range membership {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	ambiguities := []string{}
	for _, directory := range directories {
		managers := membership[directory]
		names := make([]string, 0, len(managers))
		for manager := range managers {
			if nodeManagers[manager] {
				names = append(names, manager)
			}
		}
		if len(names) < 2 {
			continue
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
