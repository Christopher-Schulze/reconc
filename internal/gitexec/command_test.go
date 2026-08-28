package gitexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandContextSanitizesAmbientGitEnvironment(t *testing.T) {
	hostile := map[string]string{
		"GIT_DIR":                         "foreign-git-dir",
		"GIT_WORK_TREE":                   "foreign-work-tree",
		"GIT_INDEX_FILE":                  "foreign-index",
		"GIT_COMMON_DIR":                  "foreign-common-dir",
		"GIT_OBJECT_DIRECTORY":            "foreign-objects",
		"GIT_CONFIG_GLOBAL":               "foreign-config",
		"GIT_CONFIG_COUNT":                "1",
		"GIT_CONFIG_KEY_0":                "core.hooksPath",
		"GIT_CONFIG_VALUE_0":              "foreign-hooks",
		"GIT_TERMINAL_PROMPT":             "1",
		"GIT_OPTIONAL_LOCKS":              "1",
		"GIT_DISCOVERY_ACROSS_FILESYSTEM": "1",
		"LC_ALL":                          "hostile-locale",
	}
	for key, value := range hostile {
		t.Setenv(key, value)
	}
	command := CommandContext(context.Background(), t.TempDir(), nil, "status", "--short")
	environment := environmentMap(command.Env)
	want := map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_OPTIONAL_LOCKS":  "0",
		"GIT_PAGER":           "cat",
		"LC_ALL":              "C",
	}
	for key, value := range want {
		if environment[key] != value {
			t.Fatalf("%s = %q, want %q", key, environment[key], value)
		}
	}
	for key := range hostile {
		if _, safe := want[key]; safe {
			continue
		}
		if _, found := environment[key]; found {
			t.Fatalf("hostile environment key %s survived", key)
		}
	}
	wantPrefix := []string{
		"git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false", "-c", "core.hooksPath=",
	}
	if len(command.Args) < len(wantPrefix) {
		t.Fatalf("Git argv is truncated: %v", command.Args)
	}
	for index, value := range wantPrefix {
		if command.Args[index] != value {
			t.Fatalf("Git argv[%d] = %q, want %q", index, command.Args[index], value)
		}
	}
}

func TestCommandContextIgnoresForeignRepositoryState(t *testing.T) {
	target := createGitInspectionRepository(t, "target.txt", "target\n")
	foreign := createGitInspectionRepository(t, "foreign.txt", "foreign\n")
	targetHead := runGitInspectionTest(t, target, "rev-parse", "HEAD")
	targetTree := runGitInspectionTest(t, target, "rev-parse", "HEAD^{tree}")
	foreignHead := runGitInspectionTest(t, foreign, "rev-parse", "HEAD")
	if targetHead == foreignHead {
		t.Fatal("test repositories unexpectedly have the same HEAD")
	}
	globalConfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(globalConfig, []byte("[alias]\n\tinjected = status\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hostile := map[string]string{
		"GIT_DIR":                          filepath.Join(foreign, ".git"),
		"GIT_WORK_TREE":                    foreign,
		"GIT_INDEX_FILE":                   filepath.Join(foreign, ".git", "index"),
		"GIT_COMMON_DIR":                   filepath.Join(foreign, ".git"),
		"GIT_OBJECT_DIRECTORY":             filepath.Join(foreign, ".git", "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": filepath.Join(foreign, ".git", "objects"),
		"GIT_CONFIG_GLOBAL":                globalConfig,
		"GIT_CONFIG_COUNT":                 "1",
		"GIT_CONFIG_KEY_0":                 "alias.in-memory",
		"GIT_CONFIG_VALUE_0":               "status",
	}
	for key, value := range hostile {
		t.Setenv(key, value)
	}
	if head := runGitInspectionTest(t, target, "rev-parse", "HEAD"); head != targetHead {
		t.Fatalf("HEAD = %q, want target %q", head, targetHead)
	}
	if tree := runGitInspectionTest(t, target, "rev-parse", "HEAD^{tree}"); tree != targetTree {
		t.Fatalf("tree = %q, want target %q", tree, targetTree)
	}
	if index := runGitInspectionTest(t, target, "ls-files", "-z"); index != "target.txt\x00" {
		t.Fatalf("index = %q, want target index", index)
	}
	common := runGitInspectionTest(t, target, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(common) {
		common = filepath.Join(target, common)
	}
	if filepath.Clean(common) != filepath.Join(target, ".git") {
		t.Fatalf("common directory = %q, want target .git", common)
	}
	assertGitConfigMissing(t, target, "alias.injected")
	assertGitConfigMissing(t, target, "alias.in-memory")
}

func createGitInspectionRepository(t *testing.T, name, body string) string {
	t.Helper()
	repository := t.TempDir()
	runGitInspectionTest(t, repository, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(repository, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitInspectionTest(t, repository, "add", "--", name)
	runGitInspectionTest(
		t, repository,
		"-c", "user.name=reconc-test", "-c", "user.email=reconc-test@example.com",
		"commit", "--quiet", "-m", "initial",
	)
	return repository
}

func runGitInspectionTest(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := CommandContext(context.Background(), repository, nil, args...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func assertGitConfigMissing(t *testing.T, repository, key string) {
	t.Helper()
	command := CommandContext(context.Background(), repository, nil, "config", "--get", key)
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf("git config --get %s: err=%v output=%q", key, err, output)
	}
}

func environmentMap(entries []string) map[string]string {
	environment := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, found := strings.Cut(entry, "=")
		if found {
			environment[strings.ToUpper(key)] = value
		}
	}
	return environment
}
