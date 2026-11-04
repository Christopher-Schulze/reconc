package agentsession

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func writeFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPreToolUseAdmitsCopyOntoAnExistingRegularFile(t *testing.T) {
	repo := setupStopBenchmarkRepo(t)
	writeFixtureFile(t, filepath.Join(repo, "candidate.txt"), "candidate\n")
	writeFixtureFile(t, filepath.Join(repo, "target.txt"), "target\n")
	writeFixtureFile(t, filepath.Join(repo, "sub", "target.txt"), "target\n")
	absolute := filepath.Join(repo, "target.txt")
	for _, test := range []struct {
		command string
		input   map[string]interface{}
	}{
		{command: "cp candidate.txt target.txt"},
		{command: "mv candidate.txt target.txt"},
		{command: "install candidate.txt target.txt"},
		{command: "ln -f candidate.txt target.txt"},
		{command: "cp candidate.txt " + absolute},
		{command: "cp ../candidate.txt target.txt", input: map[string]interface{}{"cwd": filepath.Join(repo, "sub")}},
		{command: "cp candidate.txt new.txt"},
		{command: "cp -t . candidate.txt"},
		{command: "cp candidate.txt src"},
	} {
		input := map[string]interface{}{"command": test.command}
		for key, value := range test.input {
			input[key] = value
		}
		if result := RunPreToolUse(repo, preToolUsePayload(t, "Bash", input)); result.ExitCode != 0 {
			t.Fatalf("%s: %+v", test.command, result)
		}
	}
}

func TestPreToolUseStillBlocksProtectedCopyTargets(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("symlink fixture")
	}
	repo := setupStopBenchmarkRepo(t)
	policy := "rules:\n  - id: deny-protected\n    kind: deny_write\n    paths: ['protected.md']\n    mode: block\n    message: protected is locked\n"
	writeFixtureFile(t, filepath.Join(repo, "policies", "rules.yml"), policy)
	writeFixtureFile(t, filepath.Join(repo, "protected.md"), "protected\n")
	if _, err := compiler.CompileRepoPolicy(repo, "test"); err != nil {
		t.Fatal(err)
	}
	stopGenerationGit(t, repo, "add", "-A")
	stopGenerationGit(t, repo, "commit", "-m", "protected fixture", "--quiet")
	writeFixtureFile(t, filepath.Join(repo, "elsewhere", "protected.md"), "other\n")
	for link, target := range map[string]string{"file-alias": "protected.md", "dir-alias": "."} {
		if err := os.Symlink(target, filepath.Join(repo, link)); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range []string{
		"cp src/a.go protected.md",
		"mv src/a.go protected.md",
		"cp src/a.go file-alias",
		"cp elsewhere/protected.md .",
		"cp elsewhere/protected.md dir-alias",
		"cp -t . elsewhere/protected.md",
	} {
		result := RunPreToolUse(repo, preToolUsePayload(t, "Bash", map[string]interface{}{"command": command}))
		if result.ExitCode != 2 || !strings.Contains(result.Stderr, "deny-protected") {
			t.Fatalf("%s must stay blocked before execution: %+v", command, result)
		}
	}
}

func TestLiteralCommandWritePathsResolveDirectoryEntriesAtTheirDestination(t *testing.T) {
	repo := setupStopBenchmarkRepo(t)
	root, err := ResolveRepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "file.txt"), "file\n")
	writeFixtureFile(t, filepath.Join(root, "dir", "kept.txt"), "kept\n")
	abs := func(relative string) string { return filepath.Join(root, relative) }
	for _, test := range []struct {
		command string
		want    []string
	}{
		{"cp a file.txt", []string{abs("file.txt")}},
		{"mv a file.txt", []string{abs("a"), abs("file.txt")}},
		{"cp a b dir", []string{abs("dir"), abs("dir/a"), abs("dir/b")}},
		{"cp a new", []string{abs("new"), abs("new/a")}},
		{"cp -t dir a", []string{abs("dir/a")}},
		{"cd dir && cp a file.txt", nil},
	} {
		payload, err := ParsePayload(preToolUsePayload(t, "Bash", map[string]interface{}{"command": test.command}))
		if err != nil {
			t.Fatal(err)
		}
		got := literalCommandWritePaths(root, payload)
		if test.want == nil {
			test.want = []string{}
		}
		if !slices.Equal(got, test.want) {
			t.Fatalf("%s: paths = %v, want %v", test.command, got, test.want)
		}
	}
}
