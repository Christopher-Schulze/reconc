package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/gitexec"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

const hookVerificationLookPathProbeEnv = "RECONC_HOOK_VERIFY_LOOKPATH_PROBE"

func TestHookVerifyOfflineCoversSharedMatrixWithoutLiveClaims(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "verify", "--json"}, "test", &stdout, &stderr); err != nil {
		t.Fatalf("offline verify: %v (%s)", err, stderr.String())
	}
	var report hookVerificationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !report.Complete || report.Mode != "offline" || len(report.Results) != len(hooks.VerificationSurfaces()) {
		t.Fatalf("offline report = complete=%t mode=%s results=%d degraded=%+v", report.Complete, report.Mode, len(report.Results), degradedHookVerificationResults(report.Results))
	}
	for _, result := range report.Results {
		if !result.Configured || !result.Discoverable || !result.SyntheticEnforced || result.Loaded || result.Observed || result.Enforced || result.Degraded {
			t.Fatalf("offline facts for %s/%s = %+v", result.Kind, result.Surface, result)
		}
		if result.Transport != "verified" || result.PolicyDecision != "verified" || result.ResponseAdaptation != "verified" {
			t.Fatalf("offline stages for %s/%s = %+v", result.Kind, result.Surface, result)
		}
		if result.Unsupported == nil || len(result.UnprovenEvents) != len(result.ExpectedEvents) {
			t.Fatalf("offline completeness for %s/%s = %+v", result.Kind, result.Surface, result)
		}
	}
	encoded := stdout.String()
	for _, forbidden := range []string{os.TempDir(), "synthetic verification input", "tool_input", "session_id"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("offline report exposed private probe material %q", forbidden)
		}
	}
}

func TestHookVerificationIsolatedChild(t *testing.T) {
	if os.Getenv(hookVerificationChildEnv) != "1" {
		return
	}
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		fmt.Fprintln(os.Stderr, "isolated hook verification child arguments are missing")
		os.Exit(1)
	}
	if err := Run(os.Args[separator+1:], "test", os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(ExitCode(err))
	}
	os.Exit(0)
}

func TestHookVerificationIsolationDoesNotMutateParentEnvironment(t *testing.T) {
	want := map[string]string{
		"RECONC_HOME":             "parent-reconc-home",
		agentsession.StateRootEnv: "parent-session-state",
		"KIMI_CODE_HOME":          "parent-kimi-home",
		"PI_CODING_AGENT_DIR":     "parent-pi-home",
		"KILO_PURE":               "parent-kilo-mode",
	}
	for name, value := range want {
		t.Setenv(name, value)
	}
	all := hooks.VerificationSurfaces()
	if len(all) < 2 {
		t.Fatal("verification surface registry is unexpectedly small")
	}
	selected := []hooks.VerificationSurface{all[0], all[len(all)-1]}
	var wait sync.WaitGroup
	errorsByIndex := make([]error, len(selected))
	for index, surface := range selected {
		wait.Add(1)
		go func(index int, surface hooks.VerificationSurface) {
			defer wait.Done()
			_, errorsByIndex[index] = runOfflineHookVerification(
				hookVerifyOptions{host: surface.Kind, surface: surface.Surface, jsonOutput: true},
				[]hooks.VerificationSurface{surface},
			)
		}(index, surface)
	}
	wait.Wait()
	for _, err := range errorsByIndex {
		if err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range want {
		if got := os.Getenv(name); got != value {
			t.Fatalf("parent %s = %q, want %q", name, got, value)
		}
	}
}

func TestHookVerificationEnvironmentIsMinimalAndPathSafe(t *testing.T) {
	probeRoot := t.TempDir()
	binDir := filepath.Join(probeRoot, "bin")
	trustedDir := filepath.Join(probeRoot, "trusted")
	inheritedPath := strings.Join([]string{"", ".", "relative-bin", trustedDir, trustedDir, ""}, string(os.PathListSeparator))
	values := hookVerificationEnvironmentValues(probeRoot, binDir, []string{
		"PATH=" + inheritedPath,
		"HOME=/host/home",
		"TMPDIR=/host/tmp",
		"GIT_CONFIG_GLOBAL=/host/gitconfig",
		"GIT_DIR=/host/repository",
		"AWS_SECRET_ACCESS_KEY=secret",
		"SSH_AUTH_SOCK=/host/agent.sock",
		"NODE_OPTIONS=--require=/host/inject.js",
	})
	environment := hookVerificationEnvironment(values)
	if !sort.StringsAreSorted(environment) {
		t.Fatalf("verification environment is not deterministic: %q", environment)
	}
	got := hookVerificationEnvironmentMap(environment)
	wantPath := binDir + string(os.PathListSeparator) + trustedDir
	if got["PATH"] != wantPath {
		t.Fatalf("verification PATH = %q, want %q", got["PATH"], wantPath)
	}
	for _, path := range filepath.SplitList(got["PATH"]) {
		if path == "" || !filepath.IsAbs(path) {
			t.Fatalf("verification PATH retained unsafe element %q", path)
		}
	}
	for _, name := range []string{"AWS_SECRET_ACCESS_KEY", "GIT_CONFIG_GLOBAL", "GIT_DIR", "NODE_OPTIONS", "SSH_AUTH_SOCK"} {
		if _, found := got[name]; found {
			t.Fatalf("ambient %s survived the verification allowlist", name)
		}
	}
	if got["HOME"] != filepath.Join(probeRoot, "home") || got["TMPDIR"] != filepath.Join(probeRoot, "tmp") {
		t.Fatalf("verification homes are not disposable: HOME=%q TMPDIR=%q", got["HOME"], got["TMPDIR"])
	}
}

func TestHookVerificationPATHCannotResolveCurrentDirectoryExecutable(t *testing.T) {
	probeRoot := t.TempDir()
	binDir := filepath.Join(probeRoot, "bin")
	workingDirectory := filepath.Join(probeRoot, "working")
	for _, directory := range []string{binDir, workingDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range hookVerificationPrivateDirectories(probeRoot) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	name := "reconc-hook-verify-path-poison"
	filename := name
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}
	if err := os.WriteFile(filepath.Join(workingDirectory, filename), []byte("poison"), 0o700); err != nil {
		t.Fatal(err)
	}
	values := hookVerificationEnvironmentValues(probeRoot, binDir, []string{"PATH=" + string(os.PathListSeparator) + "."})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestHookVerificationCurrentDirectoryLookupChild$")
	command.Dir = workingDirectory
	command.Env = append(hookVerificationEnvironment(values), hookVerificationLookPathProbeEnv+"="+name)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("isolated lookup child: %v: %s", err, output)
	}
}

func TestHookVerificationCurrentDirectoryLookupChild(t *testing.T) {
	name := os.Getenv(hookVerificationLookPathProbeEnv)
	if name == "" {
		return
	}
	if path, err := exec.LookPath(name); err == nil {
		t.Fatalf("current-directory executable resolved through isolated PATH: %s", path)
	}
}

func TestInitializeHookVerificationRepoIgnoresAmbientGitControls(t *testing.T) {
	repo := t.TempDir()
	template := t.TempDir()
	if err := os.WriteFile(filepath.Join(template, "poisoned-template"), []byte("poison"), 0o600); err != nil {
		t.Fatal(err)
	}
	globalConfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(globalConfig, []byte("[init]\n\ttemplateDir = "+template+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreignRoot := t.TempDir()
	foreignGitDir := filepath.Join(foreignRoot, "foreign.git")
	foreignIndex := filepath.Join(foreignRoot, "foreign.index")
	for name, value := range map[string]string{
		"GIT_CONFIG_GLOBAL":   globalConfig,
		"GIT_CONFIG_COUNT":    "1",
		"GIT_CONFIG_KEY_0":    "init.templateDir",
		"GIT_CONFIG_VALUE_0":  template,
		"GIT_DIR":             foreignGitDir,
		"GIT_INDEX_FILE":      foreignIndex,
		"GIT_WORK_TREE":       foreignRoot,
		"GIT_TERMINAL_PROMPT": "1",
	} {
		t.Setenv(name, value)
	}
	if err := initializeHookVerificationRepo(repo, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".git", "poisoned-template")); !os.IsNotExist(err) {
		t.Fatalf("ambient Git template entered disposable repository: %v", err)
	}
	if _, err := os.Lstat(foreignIndex); !os.IsNotExist(err) {
		t.Fatalf("ambient Git index received disposable state: %v", err)
	}
	command := gitexec.CommandContext(context.Background(), repo, nil, "ls-files", "--error-unmatch", "forbidden.txt")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("disposable denied path was not staged in its own index: %v: %s", err, output)
	}
}

func hookVerificationEnvironmentMap(environment []string) map[string]string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[name] = value
		}
	}
	return values
}

func TestHookVerificationInternalCommandsRequireIsolatedWorkspace(t *testing.T) {
	t.Setenv(hookVerificationChildEnv, "")
	for _, args := range [][]string{
		{"hook", "__verify-offline", "", "", t.TempDir()},
		{"hook", "__verify-live-setup", hooks.KindOpenCode, "cli", t.TempDir()},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(args, "test", &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
			t.Fatalf("Run(%v) error = %v, want hidden-command rejection", args, err)
		}
	}
}

func degradedHookVerificationResults(results []hookVerificationResult) []hookVerificationResult {
	degraded := make([]hookVerificationResult, 0)
	for _, result := range results {
		if !hookVerificationResultComplete(result) {
			degraded = append(degraded, result)
		}
	}
	return degraded
}

func TestHookVerifyLiveRequiresExactApprovalAndSurface(t *testing.T) {
	for _, args := range [][]string{
		{"hook", "verify", "--live"},
		{"hook", "verify", "--live", "--host", "opencode", "--surface", "cli"},
		{"hook", "verify", "--surface", "cli"},
		{"hook", "verify", "--host", "opencode", "--surface", "vscode"},
	} {
		var stdout, stderr bytes.Buffer
		if err := Run(args, "test", &stdout, &stderr); err == nil {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}
}

func TestLiveHookVerifyReportsMissingKnownHostBinary(t *testing.T) {
	originalLookPath := hookVerifyLookPath
	hookVerifyLookPath = func(name string) (string, error) {
		if name == "opencode" {
			return "", exec.ErrNotFound
		}
		return originalLookPath(name)
	}
	t.Cleanup(func() { hookVerifyLookPath = originalLookPath })
	surfaces, err := selectHookVerificationSurfaces(hooks.KindOpenCode, "cli")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	options := hookVerifyOptions{host: hooks.KindOpenCode, surface: "cli", live: true, allowAuthenticated: true, jsonOutput: true}
	if err := runLiveHookVerification(options, surfaces, strings.NewReader("\n"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var report hookVerificationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	result := report.Results[0]
	if report.Complete || result.HostAvailable == nil || *result.HostAvailable || !result.Degraded || len(result.Unsupported) != 1 {
		t.Fatalf("missing-host report = %+v", report)
	}
}

func TestLiveHookVerifyReportsOperatorAbortWithoutClaims(t *testing.T) {
	originalLookPath := hookVerifyLookPath
	hookVerifyLookPath = func(name string) (string, error) {
		if name == "opencode" || name == "jq" {
			return "/usr/bin/" + name, nil
		}
		return originalLookPath(name)
	}
	t.Cleanup(func() { hookVerifyLookPath = originalLookPath })
	surfaces, err := selectHookVerificationSurfaces(hooks.KindOpenCode, "cli")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	options := hookVerifyOptions{host: hooks.KindOpenCode, surface: "cli", live: true, allowAuthenticated: true, jsonOutput: true}
	if err := runLiveHookVerification(options, surfaces, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var report hookVerificationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	result := report.Results[0]
	if report.Complete || result.Loaded || result.Observed || result.Enforced || !strings.Contains(result.Detail, "operator aborted") {
		t.Fatalf("aborted report = %+v", report)
	}
}

func TestApplyLiveHookProbeRecordsSeparatesObservedFromComplete(t *testing.T) {
	repo := t.TempDir()
	result := hookVerificationResult{
		ExpectedEvents: []string{"opencode-session-start", "opencode-pre-tool-use", "opencode-stop"},
		UnprovenEvents: []string{"opencode-session-start", "opencode-pre-tool-use", "opencode-stop"},
	}
	records := []liveHookProbeRecord{
		{Route: "opencode-session-start", Fields: []string{"session_id"}, ResultClass: "allowed-or-observed", ExitCode: 0, DurationNanos: int64(2 * time.Millisecond)},
		{Route: "opencode-pre-tool-use", Fields: []string{"session_id", "tool_input", "tool_name"}, ResultClass: "blocked", ExitCode: 2, DurationNanos: int64(3 * time.Millisecond)},
	}
	result = applyLiveHookProbeRecords(result, records, repo)
	if !result.Loaded || !result.Observed || !result.Enforced || !result.Degraded || result.DurationMillis != 3 {
		t.Fatalf("live facts = %+v", result)
	}
	if len(result.UnprovenEvents) != 1 || result.UnprovenEvents[0] != "opencode-stop" {
		t.Fatalf("unproven events = %#v", result.UnprovenEvents)
	}
	if strings.Join(result.ObservedFields, ",") != "session_id,tool_input,tool_name" {
		t.Fatalf("observed fields = %#v", result.ObservedFields)
	}
}

func TestHookRuntimeTimingWritesSanitizedProbeFD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timing.txt")
	probe, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	originalProbeFile := hookTimingProbeFile
	hookTimingProbeFile = func(int) *os.File { return probe }
	t.Cleanup(func() { hookTimingProbeFile = originalProbeFile })
	t.Setenv("RECONC_HOOK_TIMING", "")
	t.Setenv("RECONC_HOOK_TIMING_THRESHOLD_MS", "")
	t.Setenv("RECONC_HOOK_TIMING_FD", fmt.Sprint(3))
	var diagnostics bytes.Buffer
	timing := newHookRuntimeTiming("opencode-pre-tool-use", &diagnostics)
	timing.mark("payload_read")
	timing.finish(2)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := string(body)
	if !strings.HasPrefix(value, "duration_ns=") || strings.Contains(value, "opencode") || diagnostics.Len() != 0 {
		t.Fatalf("probe=%q diagnostics=%q", value, diagnostics.String())
	}
}

func TestReadLiveHookProbeRecordsRejectsRawOrOversizedState(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".reconc", "hook-verify-events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"route":"opencode-pre-tool-use","fields":[],"result_class":"blocked","exit_code":2,"duration_ns":1,"raw":"secret"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLiveHookProbeRecords(repo); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("raw field error = %v", err)
	}
}
