package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	runErr := Run([]string{"hook", "verify", "--json"}, "test", &stdout, &stderr)
	var report hookVerificationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if runErr != nil {
		t.Fatalf("offline verify: %v (%s), degraded=%+v", runErr, stderr.String(), degradedHookVerificationResults(report.Results))
	}
	if !report.Complete || report.Mode != "offline" || len(report.Results) != len(hooks.VerificationSurfaces()) {
		t.Fatalf("offline report = complete=%t mode=%s results=%d degraded=%+v", report.Complete, report.Mode, len(report.Results), degradedHookVerificationResults(report.Results))
	}
	for _, result := range report.Results {
		wantEnforced := result.Kind != hooks.KindDSH
		if !result.Configured || !result.Discoverable || result.SyntheticEnforced != wantEnforced || result.Loaded || result.Observed || result.Enforced || result.Degraded {
			t.Fatalf("offline facts for %s/%s = %+v", result.Kind, result.Surface, result)
		}
		if result.Kind == hooks.KindDSH && result.ResultClass != "synthetic-advisory" {
			t.Fatalf("DSH advisory proof mislabeled: %+v", result)
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

func TestManagedCLIHookVerifyCompletesMatrixWithoutChangingBinary(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	buildDirectory := t.TempDir()
	executable := filepath.Join(buildDirectory, "reconc")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "make", "build", "BINDIR="+buildDirectory)
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build product CLI: %v: %s", err, output)
	}
	versionOutput, err := exec.CommandContext(ctx, executable, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(versionOutput), "dev+") {
		t.Fatalf("managed binary version = %q, error = %v, want a development commit", versionOutput, err)
	}
	originalBinary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := sha256.Sum256(originalBinary)
	command := exec.CommandContext(ctx, executable, "hook", "verify", "--json")
	command.Dir = t.TempDir()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("managed CLI offline verification: %v: %s", err, output)
	}
	var report hookVerificationReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode built CLI report: %v: %s", err, output)
	}
	if !report.Complete || len(report.Results) != len(hooks.VerificationSurfaces()) {
		t.Fatalf("managed CLI offline report incomplete: %+v", report)
	}
	var dshVerified bool
	for _, result := range report.Results {
		if result.Kind == hooks.KindDSH {
			dshVerified = result.Transport == "verified" && result.PolicyDecision == "verified" && result.ResponseAdaptation == "verified"
		}
	}
	if !dshVerified {
		t.Fatal("managed CLI did not execute the DSH adapter worker")
	}
	currentBinary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(currentBinary) != originalDigest {
		t.Fatal("offline verification changed its managed source executable")
	}
	if runtime.GOOS == "windows" {
		return // Direct execution of the POSIX capture shim is a macOS/Linux check.
	}
	repo := t.TempDir()
	if err := initializeHookVerificationRepo(repo, false); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.Install(hooks.KindOMP, repo, false); err != nil {
		t.Fatal(err)
	}
	if err := copyHookVerificationExecutable(executable, filepath.Join(repo, "reconc")); err != nil {
		t.Fatal(err)
	}
	if err := installLiveHookProbeShim(repo, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	worker := exec.CommandContext(ctx, filepath.Join(repo, hooks.WrapperPath), "__worker_v1__", repo)
	worker.Dir = repo
	worker.Stdin = strings.NewReader("{\"format_version\":1,\"type\":\"ping\",\"id\":\"ping\"}\n{\"format_version\":1,\"type\":\"shutdown\",\"id\":\"bye\"}\n")
	workerOutput, err := worker.Output()
	if err != nil {
		t.Fatalf("live capture shim broke persistent worker transport: %v", err)
	}
	workerDecoder := json.NewDecoder(bytes.NewReader(workerOutput))
	for _, want := range []struct{ id, kind string }{{"ping", "response"}, {"bye", "shutdown"}} {
		var response hookWorkerResponse
		if err := workerDecoder.Decode(&response); err != nil || response.ID != want.id || response.Type != want.kind || response.Code != 0 {
			t.Fatalf("persistent worker response = %+v, %v, want %s/%s", response, err, want.id, want.kind)
		}
	}
}

func TestHookVerificationReportExitContract(t *testing.T) {
	verified := hookVerificationResult{
		Kind:               hooks.KindOpenCode,
		Surface:            "cli",
		ArtifactGeneration: "verified",
		Configuration:      "verified",
		Transport:          "verified",
		PolicyDecision:     "verified",
		ResponseAdaptation: "verified",
		Configured:         true,
		Discoverable:       true,
		SyntheticEnforced:  true,
		Unsupported:        []string{},
		ExpectedEvents:     []string{"opencode-pre-tool-use"},
		UnprovenEvents:     []string{"opencode-pre-tool-use"},
		ObservedFields:     []string{},
		ResultClass:        "synthetic-block",
	}
	degraded := verified
	degraded.Kind = hooks.KindKilo
	degraded.Transport = "failed"
	degraded.Degraded = true
	degraded.Detail = "transport unavailable"
	unsupported := degraded
	unsupported.Kind = hooks.KindGitPreCommit
	unsupported.Unsupported = []string{"live capture unsupported"}
	unsupported.Detail = "live capture unsupported"

	tests := []struct {
		name     string
		report   hookVerificationReport
		wantExit int
		wantLast string
	}{
		{
			name:     "complete",
			report:   hookVerificationReport{FormatVersion: hookVerificationFormatVersion, Mode: "offline", Complete: true, Results: []hookVerificationResult{verified}},
			wantExit: 0,
			wantLast: hooks.KindOpenCode,
		},
		{
			name:     "partially-degraded",
			report:   hookVerificationReport{FormatVersion: hookVerificationFormatVersion, Mode: "offline", Results: []hookVerificationResult{verified, degraded}},
			wantExit: hookVerificationIncompleteExitCode,
			wantLast: degraded.Detail,
		},
		{
			name:     "fully-degraded",
			report:   hookVerificationReport{FormatVersion: hookVerificationFormatVersion, Mode: "offline", Results: []hookVerificationResult{degraded}},
			wantExit: hookVerificationIncompleteExitCode,
			wantLast: degraded.Detail,
		},
		{
			name:     "unsupported",
			report:   hookVerificationReport{FormatVersion: hookVerificationFormatVersion, Mode: "live", Results: []hookVerificationResult{unsupported}},
			wantExit: hookVerificationIncompleteExitCode,
			wantLast: unsupported.Detail,
		},
	}
	for _, test := range tests {
		for _, jsonOutput := range []bool{false, true} {
			format := "text"
			if jsonOutput {
				format = "json"
			}
			t.Run(test.name+"/"+format, func(t *testing.T) {
				var output bytes.Buffer
				err := writeHookVerificationReport(test.report, jsonOutput, &output)
				if got := ExitCode(err); got != test.wantExit {
					t.Fatalf("exit code = %d, want %d: %v", got, test.wantExit, err)
				}
				if jsonOutput {
					var rendered hookVerificationReport
					if err := json.Unmarshal(output.Bytes(), &rendered); err != nil {
						t.Fatalf("decode rendered report: %v\n%s", err, output.String())
					}
					if rendered.Complete != test.report.Complete || len(rendered.Results) != len(test.report.Results) {
						t.Fatalf("rendered report = %+v, want complete=%t results=%d", rendered, test.report.Complete, len(test.report.Results))
					}
				}
				if !strings.Contains(output.String(), test.wantLast) {
					t.Fatalf("report omitted final evidence %q:\n%s", test.wantLast, output.String())
				}
			})
		}
	}
}

func TestHookVerificationHelpDocumentsExitContract(t *testing.T) {
	var output bytes.Buffer
	if err := Run([]string{"hook", "verify", "--help"}, "test", &output, &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"incomplete verification exits 2", "failures exit 1"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("help omitted %q:\n%s", want, output.String())
		}
	}
}

func TestHookVerificationTextRetainsLiveReceiptAndEscapesPaths(t *testing.T) {
	result := hookVerificationResult{
		Host:  &liveHookHostIdentity{Executable: "/tmp/native\npath", RuntimeSHA256: strings.Repeat("b", 64)},
		Probe: &liveHookReceipt{RunID: strings.Repeat("a", 32), Mode: "native-exec", Operations: []liveHookOperationProof{{Operation: "denied-write", Decision: "block", HostOutcome: "rejected", EffectCheck: "absent", Complete: true}}},
	}
	var output bytes.Buffer
	if err := writeHookVerificationEvidence(result, &output); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(output.Bytes(), []byte{'\n'}) != 1 {
		t.Fatal("live evidence injected extra terminal lines")
	}
	var decoded struct {
		Host  liveHookHostIdentity `json:"host"`
		Probe liveHookReceipt      `json:"probe"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(output.String(), "  evidence: ")), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Host.Executable != result.Host.Executable || decoded.Host.RuntimeSHA256 != result.Host.RuntimeSHA256 || decoded.Probe.RunID != result.Probe.RunID || len(decoded.Probe.Operations) != 1 || !decoded.Probe.Operations[0].Complete {
		t.Fatalf("live receipt lost identity or operation evidence: %+v", decoded)
	}
	if err := writeHookVerificationEvidence(result, failingOutputWriter{}); err == nil {
		t.Fatal("live receipt output failure was ignored")
	}
}

func TestHookVerificationReportOutputFailureWinsOverIncompleteStatus(t *testing.T) {
	report := hookVerificationReport{
		FormatVersion: hookVerificationFormatVersion,
		Mode:          "offline",
		Results:       []hookVerificationResult{{Kind: hooks.KindOpenCode, Surface: "cli", Degraded: true}},
	}
	for _, jsonOutput := range []bool{false, true} {
		err := writeHookVerificationReport(report, jsonOutput, failingOutputWriter{})
		if ExitCode(err) != 1 || !strings.Contains(err.Error(), "output unavailable") {
			t.Fatalf("json=%t error = %v, want output failure with exit 1", jsonOutput, err)
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
		{"hook", "__verify-live-confirm", t.TempDir()},
		{"hook", "__verify-live-capture", t.TempDir(), strings.Repeat("a", 32), "codex-pre-tool-use"},
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
	if err := runLiveHookVerification(options, surfaces, strings.NewReader("\n"), &stdout, &stderr); ExitCode(err) != hookVerificationIncompleteExitCode {
		t.Fatalf("live verification exit = %d, want %d: %v", ExitCode(err), hookVerificationIncompleteExitCode, err)
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

func TestLiveHookVerifyJSONNeverWaitsForOperator(t *testing.T) {
	hostPath := writeLiveHookHostFixture(t, "printf '%s\\n' 'opencode test'\n")
	originalLookPath := hookVerifyLookPath
	hookVerifyLookPath = func(name string) (string, error) {
		if name == "opencode" {
			return hostPath, nil
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
	if err := runLiveHookVerification(options, surfaces, rejectedLiveHookInput{t}, &stdout, &stderr); ExitCode(err) != hookVerificationIncompleteExitCode {
		t.Fatalf("live verification exit = %d, want %d: %v", ExitCode(err), hookVerificationIncompleteExitCode, err)
	}
	var report hookVerificationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	result := report.Results[0]
	if report.Complete || result.Loaded || result.Observed || result.Enforced || !strings.Contains(result.Detail, "JSON mode never waits") {
		t.Fatalf("aborted report = %+v", report)
	}
}

type rejectedLiveHookInput struct{ t *testing.T }

func (input rejectedLiveHookInput) Read([]byte) (int, error) {
	input.t.Error("noninteractive live verification attempted to read operator input")
	return 0, fmt.Errorf("operator input must not be read")
}

func TestLiveHookOperatorConfirmationIsBounded(t *testing.T) {
	workspace, err := newHookVerificationWorkspace("reconc-hook-confirm-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.cleanup()
	for _, test := range []struct {
		name, input string
		valid       bool
	}{
		{"confirmed", "\n", true},
		{"aborted", "", false},
		{"oversized", strings.Repeat("x", 4096) + "\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := waitLiveHookOperator(ctx, workspace, strings.NewReader(test.input)); (err == nil) != test.valid {
				t.Fatalf("confirmation error=%v", err)
			}
		})
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := waitLiveHookOperator(ctx, workspace, reader); err == nil || ctx.Err() == nil || time.Since(start) > time.Second {
		t.Fatalf("blocked operator read did not respect deadline: %v", err)
	}
}

func TestLiveHookProbeRecordRejectsContradictoryExitStatus(t *testing.T) {
	for _, test := range []struct {
		class string
		exit  int
		valid bool
	}{
		{"allowed-or-observed", 0, true}, {"blocked", 2, true}, {"runtime-error", 1, true},
		{"blocked", 0, false}, {"allowed-or-observed", 2, false}, {"runtime-error", 0, false},
		{"runtime-error", -1, false}, {"runtime-error", 256, false},
	} {
		record := liveHookProbeRecord{Route: "opencode-pre-tool-use", Fields: []string{}, ResultClass: test.class, ExitCode: test.exit}
		if got := validLiveHookProbeRecord(record); got != test.valid {
			t.Fatalf("class=%s exit=%d accepted=%t", test.class, test.exit, got)
		}
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
	if !result.Loaded || !result.Observed || result.Enforced || !result.Degraded || result.DurationMillis != 3 {
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
