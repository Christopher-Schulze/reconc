package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/hooks"
)

var hookVerifyLookPath = exec.LookPath

func runLiveHookVerification(options hookVerifyOptions, surfaces []hooks.VerificationSurface, input io.Reader, stdout, stderr io.Writer) error {
	repo, cleanup, err := prepareHookVerificationRepo("reconc-hook-live-", false)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: " + err.Error()}
	}
	defer cleanup()
	result, setupErr := configureLiveHookVerification(options, surfaces[0], repo)
	if setupErr != "" {
		result.Detail = setupErr
		return writeLiveHookVerificationResult(result, false, options.jsonOutput, stdout)
	}
	if available, checked := liveHookHostAvailability(options.host, options.surface); checked {
		result.HostAvailable = boolPointer(available)
		if !available {
			result.Unsupported = []string{"local host executable is unavailable"}
			result.Detail = "artifact configuration succeeded, but the selected local host executable is unavailable; no live claim was made"
			return writeLiveHookVerificationResult(result, false, options.jsonOutput, stdout)
		}
	}
	if unsupported := liveHookCaptureUnsupported(options.host); unsupported != "" {
		result.Unsupported, result.Detail = []string{unsupported}, "configuration is complete, but "+unsupported
		return writeLiveHookVerificationResult(result, false, options.jsonOutput, stdout)
	}
	if _, err := hookVerifyLookPath("jq"); err != nil {
		result.Unsupported = []string{"live structural-field capture without jq"}
		result.Detail = "jq is unavailable; no host was launched and no live claim was made"
		return writeLiveHookVerificationResult(result, false, options.jsonOutput, stdout)
	}
	if err := prepareLiveHookCapture(repo); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc hook verify: " + err.Error()}
	}
	fmt.Fprintf(stderr, "Disposable live probe: %s\n%s\n", repo, surfaces[0].Action)
	fmt.Fprintln(stderr, "No host was launched. Press Enter after the approved host exercise; EOF records an operator-aborted incomplete probe.")
	if _, err := bufio.NewReader(input).ReadString('\n'); err != nil {
		result.Detail = "operator aborted the live probe before confirmation; no host execution is claimed"
		return writeLiveHookVerificationResult(result, false, options.jsonOutput, stdout)
	}
	return finishLiveHookVerification(result, repo, options.jsonOutput, stdout)
}

func liveHookHostAvailability(kind, surface string) (bool, bool) {
	var candidates []string
	switch kind + ":" + surface {
	case hooks.KindCursor + ":" + string(hooks.HostSurfaceCursorDesktopAgent),
		hooks.KindCursor + ":" + string(hooks.HostSurfaceCursorDesktopCmdK),
		hooks.KindCursor + ":" + string(hooks.HostSurfaceCursorTab):
		if info, err := os.Stat("/Applications/Cursor.app/Contents/Resources/app/bin/cursor"); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return true, true
		}
		candidates = []string{"cursor"}
	case hooks.KindCursor + ":" + string(hooks.HostSurfaceCursorCLIInteractive),
		hooks.KindCursor + ":" + string(hooks.HostSurfaceCursorCLIPrint):
		candidates = []string{"agent", "cursor-agent"}
	case hooks.KindOpenCode + ":cli":
		candidates = []string{"opencode"}
	case hooks.KindKilo + ":cli":
		candidates = []string{"kilo", "kilocode"}
	case hooks.KindOMP + ":cli":
		candidates = []string{"omp"}
	case hooks.KindPi + ":cli":
		candidates = []string{"pi"}
	case hooks.KindZCode + ":cli":
		candidates = []string{"zcode"}
	default:
		return false, false
	}
	for _, candidate := range candidates {
		if _, err := hookVerifyLookPath(candidate); err == nil {
			return true, true
		}
	}
	return false, true
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
	return result, ""
}

func liveHookCaptureUnsupported(kind string) string {
	if kind == hooks.KindGitPreCommit || kind == hooks.KindKimiCode {
		return "sanitized live capture for direct non-wrapper transport is unsupported"
	}
	return ""
}

func prepareLiveHookCapture(repo string) error {
	if err := installLiveHookProbeShim(repo); err != nil {
		return fmt.Errorf("install live capture shim: %w", err)
	}
	if err := copyLiveHookExecutable(repo); err != nil {
		return fmt.Errorf("install disposable runtime: %w", err)
	}
	return nil
}

func finishLiveHookVerification(result hookVerificationResult, repo string, jsonOutput bool, stdout io.Writer) error {
	records, err := readLiveHookProbeRecords(repo)
	if err != nil {
		result.Detail = "live capture is unavailable: " + err.Error()
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
	Route         string   `json:"route"`
	Fields        []string `json:"fields"`
	ResultClass   string   `json:"result_class"`
	ExitCode      int      `json:"exit_code"`
	DurationNanos int64    `json:"duration_ns"`
}

func installLiveHookProbeShim(repo string) error {
	wrapperPath := filepath.Join(repo, filepath.FromSlash(hooks.WrapperPath))
	realWrapperPath := wrapperPath + "-verify-real"
	if err := os.Rename(wrapperPath, realWrapperPath); err != nil {
		return fmt.Errorf("preserve generated wrapper: %w", err)
	}
	shim := `#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo=$(CDPATH= cd -- "$script_dir/../../.." && pwd)
real_wrapper="$script_dir/hook-verify-real"
event="${1:-unknown}"
payload=$(cat)
fields=$(printf '%s' "$payload" | jq -c 'if type == "object" then keys | sort else [] end' 2>/dev/null || printf '[]')
timing_file=$(mktemp "${TMPDIR:-/tmp}/reconc-hook-timing.XXXXXX") || exit 1
trap 'rm -f -- "$timing_file"' EXIT INT TERM
set +e
printf '%s' "$payload" | RECONC_HOOK_TIMING_FD=3 "$real_wrapper" "$@" 3>"$timing_file"
exit_code=$?
set -e
case "$exit_code" in
  0) result_class=allowed-or-observed ;;
  2) result_class=blocked ;;
  *) result_class=runtime-error ;;
esac
duration_ns=$(sed -n 's/^duration_ns=\([0-9][0-9]*\)$/\1/p' "$timing_file" | tail -n 1)
case "$duration_ns" in ''|*[!0-9]*) duration_ns=0 ;; esac
jq -cn --arg route "$event" --arg result_class "$result_class" --argjson fields "$fields" --argjson exit_code "$exit_code" --argjson duration_ns "$duration_ns" \
  '{route:$route,fields:$fields,result_class:$result_class,exit_code:$exit_code,duration_ns:$duration_ns}' >>"$repo/.reconc/hook-verify-events.jsonl"
exit "$exit_code"
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
	decoder := json.NewDecoder(io.LimitReader(file, 64*1024+1))
	decoder.DisallowUnknownFields()
	records := make([]liveHookProbeRecord, 0, 32)
	for {
		var record liveHookProbeRecord
		if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
			return records, nil
		} else if err != nil {
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
	return true
}

func applyLiveHookProbeRecords(result hookVerificationResult, records []liveHookProbeRecord, repo string) hookVerificationResult {
	summary := summarizeLiveHookProbeRecords(records)
	result.ObservedFields = sortedBoolKeys(summary.fields)
	result.UnprovenEvents = unprovenLiveHookEvents(result.ExpectedEvents, summary.observed)
	result.Loaded, result.Observed = summary.loaded, len(records) > 0
	result.Enforced = summary.blocked && pathAbsent(filepath.Join(repo, "forbidden.txt")) && pathAbsent(filepath.Join(repo, "forbidden-command-marker"))
	result.DurationMillis = roundedHookDurationMillis(summary.maxDuration)
	result.ResultClass = summary.lastClass
	result.Transport = verifiedIf(result.Observed)
	result.PolicyDecision, result.ResponseAdaptation = verifiedIf(result.Enforced), verifiedIf(result.Enforced)
	result.Degraded = !result.Loaded || !result.Enforced || len(result.UnprovenEvents) > 0
	if len(records) == 0 {
		result.Detail = "operator confirmed the probe, but no generated route was observed"
	} else {
		result.Detail = "approved host probe recorded only route identity, top-level field names, result class, exit code, and duration"
	}
	return result
}

type liveHookProbeSummary struct {
	observed    map[string]bool
	fields      map[string]bool
	blocked     bool
	loaded      bool
	maxDuration int64
	lastClass   string
}

func summarizeLiveHookProbeRecords(records []liveHookProbeRecord) liveHookProbeSummary {
	observed := map[string]bool{}
	fields := map[string]bool{}
	blocked := false
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
		if record.ResultClass == "blocked" {
			blocked = true
		}
		if record.DurationNanos > maxDuration {
			maxDuration = record.DurationNanos
		}
		lastClass = record.ResultClass
	}
	return liveHookProbeSummary{observed: observed, fields: fields, blocked: blocked, loaded: loaded, maxDuration: maxDuration, lastClass: lastClass}
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

func pathAbsent(path string) bool {
	_, err := os.Lstat(path)
	return os.IsNotExist(err)
}

func verifiedIf(value bool) string {
	if value {
		return "verified"
	}
	return "unproven"
}
