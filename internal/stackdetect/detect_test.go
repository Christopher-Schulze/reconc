package stackdetect

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDetectFindsPortableStacksInMonoreposDeterministically(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"backend/go.mod":                  "module example\n",
		"frontend/package.json":           "{\"dependencies\":{\"next\":\"16.0.0\"}}\n",
		"frontend/bun.lock":               "lock\n",
		"frontend/svelte/package.json":    "{\"devDependencies\":{\"@sveltejs/kit\":\"2.0.0\"}}\n",
		"native/src/main.cpp":             "int main() { return 0; }\n",
		"native/zig/build.zig":            "const std = @import(\"std\");\n",
		"ops/scripts/check.sh":            "#!/bin/sh\n",
		"ops/scripts/check.ps1":           "exit 0\n",
		"services/elixir/mix.exs":         "defmodule Demo.MixProject do\nend\n",
		"services/jvm/pom.xml":            "<project/>\n",
		"services/php/composer.json":      "{}\n",
		"services/python/pyproject.toml":  "[project]\nname = 'example'\n",
		"services/rust/Cargo.toml":        "[package]\nname = \"example\"\n",
		"services/windows/example.csproj": "<Project/>\n",
	}
	for relative, body := range files {
		writeDetectionFile(t, root, relative, body)
	}

	first, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bun", "cpp", "csharp", "elixir", "go", "java", "javascript", "nextjs", "php", "powershell", "python", "rust", "shell", "svelte", "zig"}
	if !reflect.DeepEqual(first.Stacks, want) {
		t.Fatalf("stacks = %v, want %v", first.Stacks, want)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("stack detection is not deterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}
	for _, stack := range want {
		if len(first.Evidence[stack]) == 0 {
			t.Errorf("stack %s has no evidence", stack)
		}
	}
}

func TestDetectFrameworksRequireDeclaredPackageDependencies(t *testing.T) {
	root := t.TempDir()
	writeDetectionFile(t, root, "plain/package.json", "{\"dependencies\":{\"react\":\"19.0.0\"}}\n")
	writeDetectionFile(t, root, "invalid/package.json", "{invalid\n")
	writeDetectionFile(t, root, "oversized/package.json", strings.Repeat(" ", maxPackageJSONBytes+1))
	writeDetectionFile(t, root, "next/package.json", "{\"peerDependencies\":{\"next\":\"16.0.0\"}}\n")
	writeDetectionFile(t, root, "svelte/package.json", "{\"optionalDependencies\":{\"svelte\":\"5.0.0\"}}\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"javascript", "nextjs", "svelte"}
	if !reflect.DeepEqual(result.Stacks, want) {
		t.Fatalf("framework stacks = %v, want %v", result.Stacks, want)
	}
	if !reflect.DeepEqual(result.Evidence["nextjs"], []string{"next/package.json"}) {
		t.Fatalf("Next.js evidence = %v", result.Evidence["nextjs"])
	}
	if !reflect.DeepEqual(result.Evidence["svelte"], []string{"svelte/package.json"}) {
		t.Fatalf("Svelte evidence = %v", result.Evidence["svelte"])
	}
}

func TestDetectDistinguishesJavaScriptTypeScriptAndPackageManagers(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"npm/package.json":         `{"scripts":{"test":"node --test"}}` + "\n",
		"npm/package-lock.json":    "{}\n",
		"pnpm/package.json":        `{"packageManager":"pnpm@10.0.0"}` + "\n",
		"pnpm/tsconfig.build.json": "{}\n",
		"yarn/package.json":        "{}\n",
		"yarn/yarn.lock":           "lock\n",
	}
	for relative, body := range files {
		writeDetectionFile(t, root, relative, body)
	}

	result, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	wantStacks := []string{"javascript", "npm", "pnpm", "typescript", "yarn"}
	if !reflect.DeepEqual(result.Stacks, wantStacks) {
		t.Fatalf("stacks = %v, want %v", result.Stacks, wantStacks)
	}
	if !reflect.DeepEqual(result.PackageManagers["pnpm"], []string{"pnpm/package.json"}) {
		t.Fatalf("packageManager metadata evidence = %+v", result.PackageManagers)
	}
}

func TestDetectReportsMetadataAndLockfileManagerConflict(t *testing.T) {
	root := t.TempDir()
	writeDetectionFile(t, root, "package.json", `{"packageManager":"pnpm@10.0.0"}`+"\n")
	writeDetectionFile(t, root, "package-lock.json", "{}\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Ambiguities) != 1 || !strings.Contains(result.Ambiguities[0], "npm, pnpm") {
		t.Fatalf("metadata/lockfile ambiguity = %v", result.Ambiguities)
	}
}

func TestDetectIgnoresDependenciesBuildOutputAndUnpairedBunLocks(t *testing.T) {
	root := t.TempDir()
	writeDetectionFile(t, root, "bun.lock", "lock\n")
	writeDetectionFile(t, root, "global.json", "{}\n")
	writeDetectionFile(t, root, "Example.sln", "\n")
	writeDetectionFile(t, root, "node_modules/tool/index.java", "class Tool {}\n")
	writeDetectionFile(t, root, "build/generated/main.cpp", "int main() {}\n")
	writeDetectionFile(t, root, "vendor/tool.php", "<?php\n")
	writeDetectionFile(t, root, ".next/server/index.ps1", "exit 0\n")
	writeDetectionFile(t, root, ".svelte-kit/output/server/index.ex", "defmodule Generated do\nend\n")
	writeDetectionFile(t, root, ".zig-cache/o/generated.zig", "const generated = true;\n")
	writeDetectionFile(t, root, "_build/lib/generated.ex", "defmodule Generated do\nend\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stacks) != 0 {
		t.Fatalf("ignored, incomplete, or non-C# .NET evidence detected stacks: %+v", result)
	}
}

func TestPathDepthUsesNormalizedSlashForm(t *testing.T) {
	for relative, want := range map[string]int{
		"go.mod":              1,
		"services/api/go.mod": 3,
		"a/b/c/d/e/f":         6,
		"a/b/c/d/e/f/g":       7,
	} {
		if got := pathDepth(relative); got != want {
			t.Errorf("pathDepth(%q) = %d, want %d", relative, got, want)
		}
	}
}

func TestDetectReportsManagersMarkersAndSameDirectoryAmbiguity(t *testing.T) {
	root := t.TempDir()
	writeDetectionFile(t, root, "AGENTS.md", "# Context\n")
	writeDetectionFile(t, root, ".reconc.yml", "rules: []\n")
	writeDetectionFile(t, root, "package.json", "{}\n")
	writeDetectionFile(t, root, "bun.lock", "lock\n")
	writeDetectionFile(t, root, "package-lock.json", "{}\n")
	writeDetectionFile(t, root, "services/api/go.mod", "module example\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.PackageManagers["bun"], []string{"bun.lock"}) ||
		!reflect.DeepEqual(result.PackageManagers["npm"], []string{"package-lock.json"}) ||
		!reflect.DeepEqual(result.PackageManagers["go-modules"], []string{"services/api/go.mod"}) {
		t.Fatalf("package manager evidence = %+v", result.PackageManagers)
	}
	if !reflect.DeepEqual(result.RepositoryMarkers, []string{".reconc.yml", "AGENTS.md"}) {
		t.Fatalf("repository markers = %v", result.RepositoryMarkers)
	}
	if len(result.Ambiguities) != 1 || !strings.Contains(result.Ambiguities[0], "bun, npm") {
		t.Fatalf("ambiguities = %v", result.Ambiguities)
	}
}

func writeDetectionFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
