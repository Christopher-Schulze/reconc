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
	Stacks   []string
	Evidence map[string][]string
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
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Result{}, fmt.Errorf("resolve stack root symlinks: %w", err)
	}
	root = filepath.Clean(root)

	evidence := map[string][]string{}
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
		depth := pathDepth(relative)
		if entry.IsDir() {
			if ignoredDirectories[strings.ToLower(entry.Name())] || depth >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if depth > maxDepth || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relative = filepath.ToSlash(relative)
		stacks, err := stacksForFile(path, entry)
		if err != nil {
			return err
		}
		for _, stack := range stacks {
			if len(evidence[stack]) < maxEvidencePerStack {
				evidence[stack] = append(evidence[stack], relative)
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
	sort.Strings(stacks)
	return Result{Stacks: stacks, Evidence: evidence}, nil
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
	case "package.json":
		frameworks, err := packageFrameworks(path, entry)
		if err != nil {
			return nil, err
		}
		for _, framework := range frameworks {
			add(framework)
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
}

func packageFrameworks(path string, entry fs.DirEntry) ([]string, error) {
	info, err := entry.Info()
	if err != nil {
		return nil, fmt.Errorf("inspect package manifest %s: %w", path, err)
	}
	// Framework recommendations are optional discovery signals, not policy
	// validation. Only a bounded, valid manifest is strong enough evidence.
	if info.Size() > maxPackageJSONBytes {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read package manifest %s: %w", path, err)
	}
	var manifest packageManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, nil
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
	sort.Strings(frameworks)
	return frameworks, nil
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
	return strings.Count(filepath.Clean(relative), string(filepath.Separator)) + 1
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
