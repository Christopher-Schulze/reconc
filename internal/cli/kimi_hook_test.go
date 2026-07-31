package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
)

func TestKimiCodeCLIInstallAndUninstallUseOnlyIsolatedGlobalHome(t *testing.T) {
	enableKimiCodeCLIForCLITest(t)
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "install", "kimi-code", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("install: %v (%s)", err, stderr.String())
	}
	configPath := filepath.Join(home, "config.toml")
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read isolated config: %v", err)
	}
	if strings.Count(string(body), "[[hooks]]") != 16 {
		t.Fatalf("installed hook count = %d", strings.Count(string(body), "[[hooks]]"))
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"hook", "uninstall", "kimi-code", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("uninstall: %v (%s)", err, stderr.String())
	}
	body, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after uninstall: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("uninstall left managed config bytes: %q", body)
	}
}

func TestKimiCodeCLIRejectsMisleadingRepositoryArguments(t *testing.T) {
	enableKimiCodeCLIForCLITest(t)
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)

	for _, args := range [][]string{
		{"hook", "install", "kimi-code", "."},
		{"hook", "uninstall", "kimi-code", "."},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(args, "test", &stdout, &stderr)
		if ExitCode(err) != 1 || !strings.Contains(err.Error(), "user-global") {
			t.Fatalf("%v error = %v", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("rejected commands unexpectedly created config: %v", err)
	}
}

func TestKimiCodeGlobalRuntimeNoOpsOutsideInitializedRepository(t *testing.T) {
	t.Chdir(t.TempDir())
	stdout, stderr, code := runWithStdin(t, "", "hook", "kimi-runtime", "kimi-pre-tool-use")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("outside runtime = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestKimiCodeGlobalRuntimeDiscoversAndEnforcesRepository(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repo, "kimi-test"); err != nil {
		t.Fatalf("recompile with explicit config: %v", err)
	}
	t.Chdir(repo)
	payload := `{
		"hook_event_name":"PreToolUse",
		"session_id":"kimi-s1",
		"cwd":` + quotedKimiCLITestJSON(repo) + `,
		"tool_name":"Write",
		"tool_input":{"path":"generated/x.go"},
		"tool_call_id":"call-1"
	}`
	_, stderr, code := runWithStdin(t, payload, "hook", "kimi-runtime", "kimi-pre-tool-use")
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("runtime = code %d stderr %q", code, stderr)
	}
}

func quotedKimiCLITestJSON(value string) string {
	var output bytes.Buffer
	for _, char := range value {
		switch char {
		case '\\', '"':
			output.WriteByte('\\')
			output.WriteRune(char)
		default:
			output.WriteRune(char)
		}
	}
	return `"` + output.String() + `"`
}

func enableKimiCodeCLIForCLITest(t *testing.T) {
	t.Helper()
	running, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	name := "reconc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(binDir, name)
	if runtime.GOOS == "windows" {
		body, err := os.ReadFile(running)
		if err != nil {
			t.Fatalf("read running test executable: %v", err)
		}
		if err := os.WriteFile(target, body, 0o700); err != nil {
			t.Fatalf("copy running test executable as bare reconc: %v", err)
		}
	} else if err := os.Link(running, target); err != nil {
		t.Fatalf("link running test executable as bare reconc: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
