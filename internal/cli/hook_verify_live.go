package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/hooks"
)

var hookVerifyLookPath = exec.LookPath

func runLiveHookVerification(options hookVerifyOptions, surfaces []hooks.VerificationSurface, input io.Reader, stdout, stderr io.Writer) error {
	interruptContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(interruptContext, 5*time.Minute)
	defer cancel()
	workspace, err := newHookVerificationWorkspace("reconc-hook-live-")
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: " + err.Error()}
	}
	defer workspace.cleanup()
	repo := workspace.repo
	body, err := runHookVerificationChildContext(ctx, workspace, "hook", "__verify-live-setup", options.host, options.surface, repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: " + err.Error()}
	}
	var setup hookVerificationLiveSetup
	if err := json.Unmarshal(body, &setup); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: decode isolated live setup: " + err.Error()}
	}
	result, setupErr := setup.Result, setup.Error
	if setupErr != "" {
		result.Detail = setupErr
		return writeLiveHookVerificationResult(result, false, options.jsonOutput, stdout)
	}
	if len(liveHookHostCandidates(options.host, options.surface)) > 0 {
		identityContext, identityCancel := context.WithTimeout(ctx, 15*time.Second)
		host, identityErr := discoverLiveHookHost(identityContext, options.host, options.surface)
		identityCancel()
		result.Host, result.HostAvailable = host, boolPointer(identityErr == nil)
		if identityErr != nil {
			result.Unsupported = []string{"local host identity is unavailable or unverified"}
			result.Detail = identityErr.Error() + "; no live claim was made"
			return writeLiveHookVerificationResult(result, false, options.jsonOutput, stdout)
		}
	}
	if unsupported := liveHookCaptureUnsupported(options.host); unsupported != "" {
		result.Unsupported, result.Detail = []string{unsupported}, "configuration is complete, but "+unsupported
		return writeLiveHookVerificationResult(result, false, options.jsonOutput, stdout)
	}
	if options.host == hooks.KindCodex && options.surface == "cli" {
		return runCodexLiveVerification(ctx, result, repo, options.jsonOutput, stdout)
	}
	if options.jsonOutput {
		result.Detail = "selected surface currently requires an operator-assisted exercise; JSON mode never waits for Enter and no host was launched"
		return writeLiveHookVerificationResult(result, false, true, stdout)
	}
	if err := validateLiveHookReceiptFiles(repo, result.Probe); err != nil {
		result.Detail = err.Error()
		return writeLiveHookVerificationResult(result, false, options.jsonOutput, stdout)
	}
	if err := prepareLiveHookCapture(repo, result.Probe.RunID); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: " + err.Error()}
	}
	if err := bindLiveHookCaptureFiles(repo, result.Probe); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: " + err.Error()}
	}
	fmt.Fprintf(stderr, "Disposable live probe: %s\n%s\n", repo, surfaces[0].Action)
	fmt.Fprintln(stderr, "No host was launched. Press Enter after the approved host exercise; EOF records an operator-aborted incomplete probe.")
	if err := waitLiveHookOperator(ctx, workspace, input); err != nil {
		result.Detail = "operator aborted or the five-minute live probe deadline expired; no host execution is claimed"
		return writeLiveHookVerificationResult(result, false, options.jsonOutput, stdout)
	}
	return finishLiveHookVerification(result, repo, options.jsonOutput, stdout)
}

func waitLiveHookOperator(ctx context.Context, workspace hookVerificationWorkspace, input io.Reader) error {
	command := newHookVerificationChildCommand(ctx, workspace.executable, "hook", "__verify-live-confirm", workspace.repo)
	command.Env = append([]string(nil), workspace.environment...)
	command.Stdin = input
	// This child reads the operator's terminal. It must inherit the foreground
	// process group; CommandContext still terminates the bounded reader on cancel.
	_, err := boundedexec.Output(command, 1024)
	return err
}

func runHookVerificationLiveConfirmChild(args []string, input io.Reader) error {
	if os.Getenv(hookVerificationChildEnv) != "1" || len(args) != 1 || args[0] != os.Getenv(hookVerificationRepoEnv) {
		return &CLIError{ExitCode: 1, Message: "reconc hook: unknown subcommand \"__verify-live-confirm\""}
	}
	_, err := bufio.NewReader(io.LimitReader(input, 4096)).ReadString('\n')
	return err
}

type hookVerificationLiveSetup struct {
	Result hookVerificationResult `json:"result"`
	Error  string                 `json:"error,omitempty"`
}

func runHookVerificationLiveSetupChild(args []string, stdout io.Writer) error {
	if os.Getenv(hookVerificationChildEnv) != "1" || len(args) != 3 || args[2] != os.Getenv(hookVerificationRepoEnv) {
		return &CLIError{ExitCode: 1, Message: "reconc hook: unknown subcommand \"__verify-live-setup\""}
	}
	host, surfaceName, repo := args[0], args[1], args[2]
	surfaces, err := selectHookVerificationSurfaces(host, surfaceName)
	if err != nil || len(surfaces) != 1 {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: invalid isolated live surface"}
	}
	if err := initializeHookVerificationRepo(repo, false); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: " + err.Error()}
	}
	options := hookVerifyOptions{host: host, surface: surfaceName, live: true, allowAuthenticated: true, jsonOutput: true}
	result, setupErr := configureLiveHookVerification(options, surfaces[0], repo)
	response := hookVerificationLiveSetup{Result: result, Error: setupErr}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(response); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: encode isolated live setup: " + err.Error()}
	}
	return nil
}

func boolPointer(value bool) *bool {
	return &value
}

func configureLiveHookVerification(options hookVerifyOptions, surface hooks.VerificationSurface, repo string) (hookVerificationResult, string) {
	artifact, generationErr := hooks.Generate(options.host)
	installReport, installErr := hooks.Install(options.host, repo, false)
	result := hookVerificationResult{
		Kind: options.host, Surface: options.surface,
		ArtifactGeneration: verificationState(generationErr),
		Configuration:      verificationState(installErr),
		Transport:          "unproven",
		PolicyDecision:     "unproven",
		ResponseAdaptation: "unproven",
		Inferred:           surface.Inferred,
		Unsupported:        []string{},
		ExpectedEvents:     append([]string(nil), surface.ExpectedEvents...),
		UnprovenEvents:     append([]string(nil), surface.ExpectedEvents...),
		ObservedFields:     []string{},
		ActionRequired:     surface.Action,
		Discoverable:       generationErr == nil,
		Configured:         installErr == nil,
		Degraded:           true,
		ResultClass:        "unproven",
	}
	if generationErr != nil || installErr != nil {
		return result, combineHookVerificationErrors(generationErr, installErr)
	}
	if artifact == nil || installReport == nil {
		return result, "live setup returned an empty artifact or installation report"
	}
	result.Probe, installErr = newLiveHookReceipt(repo, installReport)
	if installErr != nil {
		return result, "live setup receipt: " + installErr.Error()
	}
	return result, ""
}

func liveHookCaptureUnsupported(kind string) string {
	if kind == hooks.KindGitPreCommit || kind == hooks.KindKimiCode {
		return "sanitized live capture for direct non-wrapper transport is unsupported"
	}
	return ""
}

func prepareLiveHookCapture(repo, runID string) error {
	if err := installLiveHookProbeShim(repo, runID); err != nil {
		return fmt.Errorf("install live capture shim: %w", err)
	}
	if err := copyLiveHookExecutable(repo); err != nil {
		return fmt.Errorf("install disposable runtime: %w", err)
	}
	return nil
}

func finishLiveHookVerification(result hookVerificationResult, repo string, jsonOutput bool, stdout io.Writer) error {
	if err := validateLiveHookReceiptFiles(repo, result.Probe); err != nil {
		result.Detail = err.Error()
		return writeLiveHookVerificationResult(result, false, jsonOutput, stdout)
	}
	records, err := readLiveHookProbeRecords(repo)
	if err != nil {
		result.Detail = "live capture is unavailable: " + err.Error()
		return writeLiveHookVerificationResult(result, false, jsonOutput, stdout)
	}
	if err := validateLiveHookCaptureBindings(records, result.Probe, time.Now()); err != nil {
		result.Detail = err.Error()
		return writeLiveHookVerificationResult(result, false, jsonOutput, stdout)
	}
	result = applyLiveHookProbeRecords(result, records, repo)
	return writeLiveHookVerificationResult(result, !result.Degraded && len(result.UnprovenEvents) == 0, jsonOutput, stdout)
}

func writeLiveHookVerificationResult(result hookVerificationResult, complete, jsonOutput bool, stdout io.Writer) error {
	report := hookVerificationReport{FormatVersion: hookVerificationFormatVersion, Mode: "live", Complete: complete, Results: []hookVerificationResult{result}}
	return writeHookVerificationReport(report, jsonOutput, stdout)
}

func verificationState(err error) string {
	if err != nil {
		return "failed"
	}
	return "verified"
}

func combineHookVerificationErrors(generationErr, installErr error) string {
	parts := make([]string, 0, 2)
	if generationErr != nil {
		parts = append(parts, "generation: "+generationErr.Error())
	}
	if installErr != nil {
		parts = append(parts, "configuration: "+installErr.Error())
	}
	return strings.Join(parts, "; ")
}

type liveHookProbeRecord struct {
	Route         string                  `json:"route"`
	Fields        []string                `json:"fields"`
	ResultClass   string                  `json:"result_class"`
	ExitCode      int                     `json:"exit_code"`
	DurationNanos int64                   `json:"duration_ns"`
	Binding       *liveHookCaptureBinding `json:"binding,omitempty"`
}

func installLiveHookProbeShim(repo, runID string) error {
	if !liveHookHex(runID, 16) {
		return fmt.Errorf("capture requires a valid probe run ID")
	}
	wrapperPath := filepath.Join(repo, filepath.FromSlash(hooks.WrapperPath))
	realWrapperPath := wrapperPath + "-verify-real"
	if _, err := os.Lstat(realWrapperPath); !os.IsNotExist(err) {
		return fmt.Errorf("preserved wrapper path must be absent")
	}
	if err := os.Rename(wrapperPath, realWrapperPath); err != nil {
		return fmt.Errorf("preserve generated wrapper: %w", err)
	}
	shim := `#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
if [ "${1:-}" = "__worker_v1__" ]; then
  exec "$script_dir/hook-verify-real" "$@"
fi
export RECONC_HOOK_VERIFY_ISOLATED_CHILD=1 RECONC_HOOK_VERIFY_REPO="$repo"
exec "$repo/reconc" hook __verify-live-capture "$repo" "` + runID + `" "$@"
`
	if err := os.WriteFile(wrapperPath, []byte(shim), 0o755); err != nil {
		return fmt.Errorf("write live capture shim: %w", err)
	}
	return nil
}

func copyLiveHookExecutable(repo string) error {
	running, err := os.Executable()
	if err != nil {
		return err
	}
	target := filepath.Join(repo, "reconc")
	return linkOrCopyVerificationExecutable(running, target)
}

func readLiveHookProbeRecords(repo string) ([]liveHookProbeRecord, error) {
	path := filepath.Join(repo, ".reconc", "hook-verify-events.jsonl")
	file, absent, err := openLiveHookProbeRecords(path)
	if err != nil || absent {
		return []liveHookProbeRecord{}, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(body) > 64*1024 {
		return nil, fmt.Errorf("sanitized live record file could not be read within its limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	records := make([]liveHookProbeRecord, 0, 32)
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); errors.Is(err, io.EOF) {
			return records, nil
		} else if err != nil {
			return nil, fmt.Errorf("decode sanitized live record: %w", err)
		}
		if !jsontext.Value(raw).IsValid() {
			return nil, fmt.Errorf("sanitized live record contains ambiguous JSON")
		}
		var record liveHookProbeRecord
		if err := jsonv2.Unmarshal(raw, &record, jsonv2.RejectUnknownMembers(true)); err != nil {
			if errors.Is(err, jsonv2.ErrUnknownName) {
				return nil, fmt.Errorf("sanitized live record contains an unknown field")
			}
			return nil, fmt.Errorf("decode sanitized live record: %w", err)
		}
		if len(records) >= 128 || !validLiveHookProbeRecord(record) {
			return nil, fmt.Errorf("sanitized live record is outside its contract")
		}
		records = append(records, record)
	}
}

func openLiveHookProbeRecords(path string) (*os.File, bool, error) {
	info, statErr := os.Lstat(path)
	if os.IsNotExist(statErr) {
		return nil, true, nil
	}
	if statErr != nil {
		return nil, false, statErr
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("sanitized live record path is not a real regular file")
	}
	if info.Size() > 64*1024 {
		return nil, false, fmt.Errorf("sanitized live record exceeds 65536 bytes")
	}
	file, err := os.Open(path)
	return file, false, err
}

func validLiveHookProbeRecord(record liveHookProbeRecord) bool {
	if _, ok := hooks.RuntimeEvent(record.Route); !ok || len(record.Fields) > 64 || record.DurationNanos < 0 {
		return false
	}
	if record.ResultClass != "allowed-or-observed" && record.ResultClass != "blocked" && record.ResultClass != "runtime-error" {
		return false
	}
	for _, field := range record.Fields {
		if len(field) > 128 || strings.TrimSpace(field) == "" {
			return false
		}
	}
	if record.Binding != nil {
		return validLiveHookBoundRecord(record)
	}
	if record.ExitCode < 0 || record.ExitCode > 255 ||
		(record.ResultClass == "allowed-or-observed" && record.ExitCode != 0) ||
		(record.ResultClass == "blocked" && record.ExitCode != 2) ||
		(record.ResultClass == "runtime-error" && (record.ExitCode == 0 || record.ExitCode == 2)) {
		return false
	}
	return true
}

func applyLiveHookProbeRecords(result hookVerificationResult, records []liveHookProbeRecord, repo string) hookVerificationResult {
	summary := summarizeLiveHookProbeRecords(records)
	result.ObservedFields = sortedBoolKeys(summary.fields)
	result.UnprovenEvents = unprovenLiveHookEvents(result.ExpectedEvents, summary.observed)
	result.Loaded, result.Observed = summary.loaded, len(records) > 0
	// Route-only records contain no bound native call or per-operation outcome.
	// An absent marker cannot prove that its operation was attempted and denied.
	result.Enforced = false
	result.DurationMillis = roundedHookDurationMillis(summary.maxDuration)
	result.ResultClass = summary.lastClass
	result.Transport = verifiedIf(result.Observed)
	result.PolicyDecision, result.ResponseAdaptation = verifiedIf(result.Enforced), verifiedIf(result.Enforced)
	result.Degraded = !result.Loaded || !result.Enforced || len(result.UnprovenEvents) > 0
	if len(records) == 0 {
		result.Detail = "operator confirmed the probe, but no generated route was observed"
	} else {
		result.Detail = "operator-assisted route observations lack bound native attempts and outcomes; enforcement remains unproven"
	}
	return result
}

type liveHookProbeSummary struct {
	observed    map[string]bool
	fields      map[string]bool
	loaded      bool
	maxDuration int64
	lastClass   string
}

func summarizeLiveHookProbeRecords(records []liveHookProbeRecord) liveHookProbeSummary {
	observed := map[string]bool{}
	fields := map[string]bool{}
	loaded := false
	maxDuration := int64(0)
	lastClass := "unproven"
	for _, record := range records {
		observed[record.Route] = true
		if route, ok := hooks.RuntimeEvent(record.Route); ok && (route.Event == hooks.EventSessionStart || route.Event == hooks.EventWorkspaceOpen) {
			loaded = true
		}
		for _, field := range record.Fields {
			fields[field] = true
		}
		if record.DurationNanos > maxDuration {
			maxDuration = record.DurationNanos
		}
		lastClass = record.ResultClass
	}
	return liveHookProbeSummary{observed: observed, fields: fields, loaded: loaded, maxDuration: maxDuration, lastClass: lastClass}
}

func unprovenLiveHookEvents(expected []string, observed map[string]bool) []string {
	unproven := make([]string, 0, len(expected))
	for _, event := range expected {
		if !observed[event] {
			unproven = append(unproven, event)
		}
	}
	return unproven
}

func roundedHookDurationMillis(nanos int64) int64 {
	millis := nanos / int64(time.Millisecond)
	if nanos > 0 && millis == 0 {
		return 1
	}
	return millis
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func verifiedIf(value bool) string {
	if value {
		return "verified"
	}
	return "unproven"
}
