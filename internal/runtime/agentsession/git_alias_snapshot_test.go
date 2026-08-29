package agentsession

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestParseGitAliasSnapshot(t *testing.T) {
	body := []byte("alias.Safe\nstatus\x00alias.wipe\n!git clean -fd\x00alias.safe\nreset --hard\x00alias.empty\n\x00")
	aliases, ok := parseGitAliasSnapshot(body)
	if !ok {
		t.Fatal("valid Git alias output was rejected")
	}
	want := map[string]gitAlias{
		"safe":  {value: "reset --hard"},
		"wipe":  {value: "!git clean -fd"},
		"empty": {value: ""},
	}
	if !reflect.DeepEqual(aliases, want) {
		t.Fatalf("aliases = %#v, want %#v", aliases, want)
	}

	for _, malformed := range [][]byte{
		[]byte("alias.safe\nstatus"),
		[]byte("alias.safe\x00"),
		[]byte("core.editor\nvim\x00"),
		[]byte("alias.safe\nstatus\x00\x00"),
	} {
		if _, ok := parseGitAliasSnapshot(malformed); ok {
			t.Fatalf("malformed alias output %q was accepted", malformed)
		}
	}
}

func TestGitAliasSnapshotIsHermeticAndBoundedToRepository(t *testing.T) {
	repo := t.TempDir()
	runGitGuardTestCommand(t, "-C", repo, "init", "--quiet")
	runGitGuardTestCommand(t, "-C", repo, "config", "alias.safe", "status")
	globalConfig := filepath.Join(t.TempDir(), "foreign-gitconfig")
	if err := os.WriteFile(globalConfig, []byte("[alias]\n\tforeign = !git reset --hard\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "alias.injected")
	t.Setenv("GIT_CONFIG_VALUE_0", "status")

	snapshot := captureGitAliasSnapshot(repo)
	if !snapshot.complete {
		t.Fatal("repository alias snapshot was not complete")
	}
	if snapshot.aliases["safe"].value != "status" {
		t.Fatalf("repository alias missing from snapshot: %#v", snapshot.aliases)
	}
	for _, name := range []string{"foreign", "injected"} {
		if _, found := snapshot.aliases[name]; found {
			t.Fatalf("ambient alias %q entered repository snapshot", name)
		}
	}
	if identity, ok := snapshot.identityValue(); !ok || len(identity) != 64 {
		t.Fatalf("snapshot identity = %q, cacheable = %v", identity, ok)
	}
}

func TestGitAliasSnapshotPreservesInlinePrecedenceAndDynamicFailClosed(t *testing.T) {
	repo := t.TempDir()
	runGitGuardTestCommand(t, "-C", repo, "init", "--quiet")
	runGitGuardTestCommand(t, "-C", repo, "config", "alias.st", "status")
	snapshot := captureGitAliasSnapshot(repo)
	if !snapshot.complete {
		t.Fatal("repository alias snapshot was not complete")
	}

	if reason := forbiddenShellCommandReasonInRepoWithAliasSnapshot(repo, "git st --short", snapshot); reason != "" {
		t.Fatalf("snapshot safe alias was blocked: %s", reason)
	}
	if reason := forbiddenShellCommandReasonInRepoWithAliasSnapshot(repo, `git -c alias.st='reset --hard' st`, snapshot); reason == "" {
		t.Fatal("inline alias did not override the snapshot")
	}
	reason := forbiddenShellCommandReasonInRepoWithAliasSnapshot(repo, `git -c alias.st="$MODE" st`, snapshot)
	if !strings.Contains(reason, "dynamic") {
		t.Fatalf("dynamic inline alias was not fail-closed: %s", reason)
	}

	before := cloneGitAliases(snapshot.aliases)
	if reason := forbiddenShellCommandReasonInRepoWithAliasSnapshot(repo, "git st", snapshot); reason != "" {
		t.Fatalf("snapshot alias was blocked after precedence checks: %s", reason)
	}
	if !reflect.DeepEqual(snapshot.aliases, before) {
		t.Fatal("shell analysis mutated the immutable alias snapshot")
	}
}

func TestGitAliasSnapshotWorkingCopiesAreRaceSafe(t *testing.T) {
	repo := t.TempDir()
	runGitGuardTestCommand(t, "-C", repo, "init", "--quiet")
	runGitGuardTestCommand(t, "-C", repo, "config", "alias.st", "status")
	snapshot := captureGitAliasSnapshot(repo)
	if !snapshot.complete {
		t.Fatal("repository alias snapshot was not complete")
	}
	const callers = 32
	var wait sync.WaitGroup
	wait.Add(callers)
	results := make(chan string, callers)
	for range callers {
		go func() {
			defer wait.Done()
			results <- forbiddenShellCommandReasonInRepoWithAliasSnapshot(repo, "git st --short", snapshot)
		}()
	}
	wait.Wait()
	close(results)
	for reason := range results {
		if reason != "" {
			t.Fatalf("concurrent snapshot analysis was blocked: %s", reason)
		}
	}
}

func TestPreDecisionReusesGitAliasSnapshotForShellAnalysis(t *testing.T) {
	repo := setupPolicyRepo(t)
	runGitGuardTestCommand(t, "-C", repo, "init", "--quiet")
	root, err := ResolveRepoRootRef(repo)
	if err != nil {
		t.Fatal(err)
	}
	if result := RunHookRequest(root, HookHandlerSessionStart, "claude-session-start", []byte(`{"session_id":"alias-process-count"}`)); result.ExitCode != 0 {
		t.Fatalf("session start: %+v", result)
	}
	counterPath := filepath.Join(t.TempDir(), "git-config-count")
	installCountingGit(t, counterPath)
	payload := []byte(`{"session_id":"alias-process-count","tool_use_id":"call-unknown","tool_name":"Bash","tool_input":{"command":"git reconc-unknown-subcommand"}}`)
	result := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "unknown git subcommand") {
		t.Fatalf("unknown command result: %+v", result)
	}
	body, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(body), 2; got != want {
		t.Fatalf("Git config process count = %d, want %d for initial and post-evaluation snapshots", got, want)
	}
}

func installCountingGit(t *testing.T, counterPath string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(counterPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var wrapper, script string
	if runtime.GOOS == "windows" {
		wrapper = filepath.Join(directory, "git.cmd")
		t.Setenv("RECONC_TEST_GIT_COUNTER", counterPath)
		script = "@echo off\r\n" +
			"if /I \"%~1\"==\"config\" goto count\r\n" +
			"if /I \"%~2\"==\"config\" goto count\r\n" +
			"if /I \"%~3\"==\"config\" goto count\r\n" +
			"if /I \"%~4\"==\"config\" goto count\r\n" +
			"if /I \"%~5\"==\"config\" goto count\r\n" +
			"if /I \"%~6\"==\"config\" goto count\r\n" +
			"if /I \"%~7\"==\"config\" goto count\r\n" +
			"if /I \"%~8\"==\"config\" goto count\r\n" +
			"if /I \"%~9\"==\"config\" goto count\r\n" +
			"goto run\r\n" +
			":count\r\n" +
			"echo x>>\"%RECONC_TEST_GIT_COUNTER%\"\r\n" +
			":run\r\n" +
			"\"" + gitPath + "\" %*\r\n"
	} else {
		wrapper = filepath.Join(directory, "git")
		script = fmt.Sprintf("#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = config ]; then\n    printf x >> %s\n    break\n  fi\ndone\nexec %s \"$@\"\n", shellQuote(counterPath), shellQuote(gitPath))
	}
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("PATH")
	t.Setenv("PATH", directory+string(os.PathListSeparator)+path)
}
