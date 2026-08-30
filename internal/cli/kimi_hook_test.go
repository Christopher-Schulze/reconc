package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/usercli"
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
	stdout, stderr, code := runWithStdin(t, "", "hook", "kimi-runtime", "receipt-v1", "kimi-pre-tool-use")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("outside runtime = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestKimiCodeGlobalRuntimeDiscoversAndEnforcesRepository(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	bindRunningKimiRuntimeForCLITest(t)
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
	_, stderr, code := runWithStdin(t, payload, "hook", "kimi-runtime", "receipt-v1", "kimi-pre-tool-use")
	if code != 2 || !strings.Contains(stderr, "deny-gen") {
		t.Fatalf("runtime = code %d stderr %q", code, stderr)
	}
}

func TestKimiCodeGlobalRuntimeRejectsLegacyContractAndMissingReceipt(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	err := runKimiCodeRuntime([]string{"kimi-pre-tool-use"}, &bytes.Buffer{}, &bytes.Buffer{})
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "reinstall Kimi Code hooks") {
		t.Fatalf("legacy runtime error = %v", err)
	}

	t.Setenv("RECONC_HOME", t.TempDir())
	err = runKimiCodeRuntime([]string{"receipt-v1", "kimi-pre-tool-use"}, &bytes.Buffer{}, &bytes.Buffer{})
	if ExitCode(err) != 2 || !strings.Contains(err.Error(), "installation receipt") || !strings.Contains(err.Error(), "install-cli") {
		t.Fatalf("unreceipted runtime error = %v", err)
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
	t.Setenv("RECONC_HOME", t.TempDir())
	binDir := t.TempDir()
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	report, err := usercli.InstallCurrentWithReceipt(binDir, usercli.InstallOptions{Version: "test"})
	if err != nil {
		t.Fatalf("install receipt-bound test CLI: %v", err)
	}
	if report.Receipt == nil {
		t.Fatalf("test CLI installation did not publish a receipt: %+v", report)
	}
}

func bindRunningKimiRuntimeForCLITest(t *testing.T) {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	running, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	running, err = filepath.EvalSymlinks(running)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(running)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	receipt, err := usercli.NewReceipt(usercli.ReceiptInput{
		Manager: usercli.ManagerSource, Channel: usercli.ChannelSource, Version: "test",
		SourceRepository: "local-source", ArtifactName: filepath.Base(running),
		ArtifactSHA256: hex.EncodeToString(digest[:]), BinaryPath: running,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, SourceDigest: "unavailable",
		ProvenanceState: usercli.ProvenanceSourceLocal, InstalledAt: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := usercli.WriteReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := usercli.VerifyRunningReceiptIdentity(); err != nil {
		t.Fatalf("verify running test receipt identity: %v", err)
	}
}
