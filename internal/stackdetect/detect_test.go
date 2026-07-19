package stackdetect

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectFindsPortableStacksInMonoreposDeterministically(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"backend/go.mod":                  "module example\n",
		"frontend/package.json":           "{}\n",
		"frontend/bun.lock":               "lock\n",
		"native/src/main.cpp":             "int main() { return 0; }\n",
		"ops/scripts/check.sh":            "#!/bin/sh\n",
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
	want := []string{"bun", "cpp", "csharp", "go", "java", "php", "python", "rust", "shell"}
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

func TestDetectIgnoresDependenciesBuildOutputAndUnpairedBunLocks(t *testing.T) {
	root := t.TempDir()
	writeDetectionFile(t, root, "bun.lock", "lock\n")
	writeDetectionFile(t, root, "global.json", "{}\n")
	writeDetectionFile(t, root, "Example.sln", "\n")
	writeDetectionFile(t, root, "node_modules/tool/index.java", "class Tool {}\n")
	writeDetectionFile(t, root, "build/generated/main.cpp", "int main() {}\n")
	writeDetectionFile(t, root, "vendor/tool.php", "<?php\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stacks) != 0 {
		t.Fatalf("ignored, incomplete, or non-C# .NET evidence detected stacks: %+v", result)
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
