package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/hooks"
)

// Capture bindings identify delivered hook input and returned control output.
// They do not establish that the host obeyed either response.
type liveHookCaptureBinding struct {
	RunID             string    `json:"run_id"`
	StartedAt         time.Time `json:"started_at"`
	SessionSHA256     string    `json:"session_sha256,omitempty"`
	TurnSHA256        string    `json:"turn_sha256,omitempty"`
	CallSHA256        string    `json:"call_sha256,omitempty"`
	ToolNameSHA256    string    `json:"tool_name_sha256,omitempty"`
	ToolInputSHA256   string    `json:"tool_input_sha256,omitempty"`
	CommandSHA256     string    `json:"command_sha256,omitempty"`
	PayloadSHA256     string    `json:"payload_sha256"`
	ResponseSHA256    string    `json:"response_sha256"`
	Decision          string    `json:"decision"`
	DecisionSource    string    `json:"decision_source"`
	PolicyDecision    string    `json:"policy_decision"`
	OwnedProcessGroup int       `json:"owned_process_group,omitempty"`
}

type liveHookCaptureExecution struct {
	Body, Diagnostics []byte
	ExitCode          int
	PolicyDecision    string
	OwnedProcessGroup int
}

func runHookVerificationCaptureChild(args []string, input io.Reader, stdout, stderr io.Writer) error {
	if len(args) < 3 || os.Getenv(hookVerificationChildEnv) != "1" || args[0] != os.Getenv(hookVerificationRepoEnv) || !liveHookHex(args[1], 16) {
		return &CLIError{ExitCode: 1, Message: "reconc hook: unknown subcommand \"__verify-live-capture\""}
	}
	if _, ok := hooks.RuntimeEvent(args[2]); !ok {
		return fmt.Errorf("live capture: unknown runtime route")
	}
	payload, err := io.ReadAll(io.LimitReader(input, maxHookVerificationOutput+1))
	if err != nil || len(payload) > maxHookVerificationOutput {
		return fmt.Errorf("live capture: input unavailable or exceeds its limit")
	}
	record, err := newLiveHookCaptureRecord(args[1], args[2], payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	execution, runErr := executeLiveHookCapture(ctx, args[0], args[2:], payload)
	record.ExitCode, record.DurationNanos = execution.ExitCode, time.Since(record.Binding.StartedAt).Nanoseconds()
	record.Binding.ResponseSHA256 = liveHookDigest(execution.Body)
	record.Binding.PolicyDecision = execution.PolicyDecision
	record.Binding.OwnedProcessGroup = execution.OwnedProcessGroup
	record.Binding.Decision, record.Binding.DecisionSource, record.ResultClass = classifyLiveHookResponse(args[2], execution.ExitCode, execution.Body)
	if runErr != nil {
		record.Binding.Decision, record.Binding.DecisionSource, record.ResultClass = "error", "capture-failure", "runtime-error"
	}
	if err := appendLiveHookCapture(args[0], record); err != nil {
		return err
	}
	if runErr != nil {
		return fmt.Errorf("live capture: wrapper failed, timed out, or exceeded its output limit")
	}
	if _, err := stdout.Write(execution.Body); err != nil {
		return err
	}
	if _, err := stderr.Write(execution.Diagnostics); err != nil {
		return err
	}
	if execution.ExitCode != 0 {
		return &CLIError{ExitCode: execution.ExitCode}
	}
	return nil
}

func executeLiveHookCapture(ctx context.Context, repo string, args []string, payload []byte) (liveHookCaptureExecution, error) {
	result := liveHookCaptureExecution{ExitCode: 1, PolicyDecision: "unproven"}
	command := exec.CommandContext(ctx, filepath.Join(repo, hooks.WrapperPath+"-verify-real"), args...)
	command.Stdin = bytes.NewReader(payload)
	group, err := configureLiveHookCaptureProcess(command)
	if err != nil {
		return result, err
	}
	result.OwnedProcessGroup = group
	probe, err := prepareLiveHookPolicyProbe(command)
	if err != nil {
		return result, err
	}
	defer probe.close()
	diagnostics, err := boundedexec.NewBuffer(64 << 10)
	if err != nil {
		return result, err
	}
	command.Stderr = diagnostics
	body, runErr := boundedexec.Output(command, 64<<10)
	result.Body, result.Diagnostics = body, diagnostics.Bytes()
	decision, probeErr := probe.decision()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		code = exitErr.ExitCode()
		if code >= 0 && code <= 255 && !errors.Is(runErr, boundedexec.ErrOutputLimit) {
			runErr = nil
		}
	}
	if ctx.Err() != nil || diagnostics.Truncated() || code < 0 || runErr != nil || probeErr != nil {
		return result, fmt.Errorf("unusable wrapper result or policy metadata")
	}
	result.ExitCode, result.PolicyDecision = code, decision
	return result, nil
}

func newLiveHookCaptureRecord(runID, route string, payload []byte) (liveHookProbeRecord, error) {
	if len(payload) > maxHookVerificationOutput || !jsontext.Value(payload).IsValid() {
		return liveHookProbeRecord{}, fmt.Errorf("live capture: input is oversized or ambiguous JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil || len(fields) > 64 {
		return liveHookProbeRecord{}, fmt.Errorf("live capture: input must be a bounded JSON object")
	}
	names := make(map[string]bool, len(fields))
	for name := range fields {
		if len(name) == 0 || len(name) > 128 {
			return liveHookProbeRecord{}, fmt.Errorf("live capture: field name is outside its contract")
		}
		names[name] = true
	}
	binding := &liveHookCaptureBinding{RunID: runID, StartedAt: time.Now().UTC(), PayloadSHA256: liveHookDigest(payload), PolicyDecision: "unproven"}
	// Only exact native keys are bound here. A host without these keys needs its
	// own qualified correlation adapter; never invent a session or tool call.
	binding.SessionSHA256 = liveHookStringDigest(fields["session_id"])
	binding.TurnSHA256 = liveHookStringDigest(fields["turn_id"])
	binding.CallSHA256 = liveHookStringDigest(fields["tool_use_id"])
	binding.ToolNameSHA256 = liveHookStringDigest(fields["tool_name"])
	if toolInput := fields["tool_input"]; len(toolInput) > 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, toolInput); err != nil {
			return liveHookProbeRecord{}, err
		}
		binding.ToolInputSHA256 = liveHookDigest(compact.Bytes())
		var command struct {
			Command string `json:"command"`
		}
		if jsonv2.Unmarshal(toolInput, &command) == nil && command.Command != "" {
			binding.CommandSHA256 = liveHookDigest([]byte(command.Command))
		}
	}
	return liveHookProbeRecord{Route: route, Fields: sortedBoolKeys(names), Binding: binding}, nil
}

func liveHookStringDigest(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" || len(value) > 512 {
		return ""
	}
	return liveHookDigest([]byte(value))
}

func liveHookDigest(body []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func liveHookHex(value string, size int) bool {
	if len(value) != 2*size {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size && hex.EncodeToString(decoded) == value
}

func appendLiveHookCapture(repo string, record liveHookProbeRecord) error {
	if !validLiveHookProbeRecord(record) {
		return fmt.Errorf("live capture: result is outside its contract")
	}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := filepath.Join(repo, ".reconc", "hook-verify-events.jsonl")
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Size()+int64(len(body))+1 > 64<<10) {
		return fmt.Errorf("live capture: record file is not regular or exceeds its limit")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.OpenFile(".reconc/hook-verify-events.jsonl", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size()+int64(len(body))+1 > 64<<10 {
		return errors.Join(fmt.Errorf("live capture: opened record file is invalid"), file.Close())
	}
	_, writeErr := file.Write(append(body, '\n'))
	return errors.Join(writeErr, file.Close())
}

func classifyLiveHookResponse(route string, code int, body []byte) (string, string, string) {
	if code == 2 {
		return "deny", "exit-2", "blocked"
	}
	if code != 0 {
		return "error", "exit-status", "runtime-error"
	}
	var response struct {
		Permission         string `json:"permission"`
		PermissionDecision string `json:"permissionDecision"`
		Decision           string `json:"decision"`
		HookSpecificOutput struct {
			HookEventName      string `json:"hookEventName"`
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "unproven", "empty-response", "allowed-or-observed"
	}
	if jsonv2.Unmarshal(trimmed, &response) != nil {
		return "unproven", "unrecognized-response", "allowed-or-observed"
	}
	decision, source := "", ""
	switch route {
	case "cursor-pre-tool-use", "cursor-before-shell-execution", "cursor-before-mcp-execution", "cursor-before-read-file", "cursor-before-tab-file-read", "cursor-subagent-start":
		decision, source = response.Permission, "cursor-permission"
	case "copilot-pre-tool-use":
		decision, source = response.PermissionDecision, "copilot-permission"
	case "grok-pre-tool-use", "antigravity-pre-tool-use":
		decision, source = response.Decision, "tool-decision"
	case "codex-pre-tool-use", "claude-pre-tool-use", "devin-pre-tool-use":
		if response.HookSpecificOutput.HookEventName == "PreToolUse" {
			decision, source = response.HookSpecificOutput.PermissionDecision, "pre-tool-permission"
		}
	}
	if decision == "deny" {
		return "deny", source, "blocked"
	}
	if decision == "allow" {
		return "allow", source, "allowed-or-observed"
	}
	return "unproven", "unrecognized-response", "allowed-or-observed"
}

func validLiveHookBoundRecord(record liveHookProbeRecord) bool {
	binding := record.Binding
	if binding.OwnedProcessGroup < 0 || binding.OwnedProcessGroup == 1 {
		return false
	}
	if !liveHookHex(binding.RunID, 16) || binding.StartedAt.IsZero() ||
		!liveHookHex(binding.PayloadSHA256, 32) || !liveHookHex(binding.ResponseSHA256, 32) {
		return false
	}
	if binding.PolicyDecision != "unproven" && binding.PolicyDecision != "pass" && binding.PolicyDecision != "block" {
		return false
	}
	for _, digest := range []string{binding.SessionSHA256, binding.TurnSHA256, binding.CallSHA256, binding.ToolNameSHA256, binding.ToolInputSHA256, binding.CommandSHA256} {
		if digest != "" && !liveHookHex(digest, 32) {
			return false
		}
	}
	switch binding.Decision {
	case "error":
		return record.ResultClass == "runtime-error" && record.ExitCode > 0 && record.ExitCode <= 255 &&
			(binding.DecisionSource == "capture-failure" || binding.DecisionSource == "exit-status")
	case "unproven":
		return record.ResultClass == "allowed-or-observed" && record.ExitCode == 0 &&
			(binding.DecisionSource == "empty-response" || binding.DecisionSource == "unrecognized-response")
	case "deny":
		return record.ResultClass == "blocked" && (record.ExitCode == 2 && binding.DecisionSource == "exit-2" ||
			record.ExitCode == 0 && liveHookJSONDecisionSource(record.Route, binding.DecisionSource))
	case "allow":
		return record.ResultClass == "allowed-or-observed" && record.ExitCode == 0 && liveHookJSONDecisionSource(record.Route, binding.DecisionSource)
	}
	return false
}

func liveHookJSONDecisionSource(route, source string) bool {
	switch route {
	case "cursor-pre-tool-use", "cursor-before-shell-execution", "cursor-before-mcp-execution", "cursor-before-read-file", "cursor-before-tab-file-read", "cursor-subagent-start":
		return source == "cursor-permission"
	case "copilot-pre-tool-use":
		return source == "copilot-permission"
	case "grok-pre-tool-use", "antigravity-pre-tool-use":
		return source == "tool-decision"
	case "codex-pre-tool-use", "claude-pre-tool-use", "devin-pre-tool-use":
		return source == "pre-tool-permission"
	}
	return false
}

func validateLiveHookCaptureBindings(records []liveHookProbeRecord, receipt *liveHookReceipt, now time.Time) error {
	if receipt == nil || !liveHookHex(receipt.RunID, 16) || receipt.StartedAt.IsZero() || now.Before(receipt.StartedAt) || now.Sub(receipt.StartedAt) > 5*time.Minute {
		return fmt.Errorf("live capture: missing or expired probe identity")
	}
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		binding := record.Binding
		if binding == nil || !validLiveHookProbeRecord(record) || binding.RunID != receipt.RunID || binding.StartedAt.Before(receipt.StartedAt) || binding.StartedAt.After(now) || record.DurationNanos > int64(now.Sub(binding.StartedAt)) {
			return fmt.Errorf("live capture: missing, foreign, or stale observation binding")
		}
		if binding.CallSHA256 == "" {
			continue
		}
		key := record.Route + "/" + binding.SessionSHA256 + "/" + binding.CallSHA256
		if seen[key] {
			return fmt.Errorf("live capture: repeated native call on the same route")
		}
		seen[key] = true
	}
	return nil
}
