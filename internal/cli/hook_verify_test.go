package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/hooks"
)

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
