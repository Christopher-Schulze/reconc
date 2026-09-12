package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
)

type liveHookCodexOperation struct {
	Name, Nonce, Command, Path, Content string
	Denied                              bool
}

func codexLiveOperations(receipt *liveHookReceipt) ([]liveHookCodexOperation, error) {
	if receipt == nil {
		return nil, fmt.Errorf("native probe receipt is missing")
	}
	operations := []liveHookCodexOperation{}
	seen := map[string]bool{}
	for _, proof := range receipt.Operations {
		if !liveHookHex(proof.Nonce, 16) || seen[proof.Nonce] || seen[proof.Operation] {
			return nil, fmt.Errorf("native probe has missing or repeated operation identities")
		}
		seen[proof.Nonce], seen[proof.Operation] = true, true
		operation := liveHookCodexOperation{Name: proof.Operation, Nonce: proof.Nonce}
		switch proof.Operation {
		case "allowed-write", "denied-write":
			operation.Path, operation.Content = "allowed-write-marker", proof.Nonce+"\n"
			if proof.Operation == "denied-write" {
				operation.Path, operation.Denied = "forbidden.txt", true
			}
			operation.Command = "*** Begin Patch\n*** Add File: " + operation.Path + "\n+" + proof.Nonce + "\n*** End Patch"
		case "allowed-shell":
			operation.Path, operation.Content = "allowed-command-marker", proof.Nonce
			operation.Command = "printf " + proof.Nonce + " > " + operation.Path
		case "denied-shell":
			operation.Path, operation.Denied = "forbidden-command-marker", true
			operation.Command = "touch " + operation.Path + " # " + proof.Nonce
		default:
			continue
		}
		operations = append(operations, operation)
	}
	if len(operations) != 4 {
		return nil, fmt.Errorf("native probe requires both allowed and denied write/shell controls")
	}
	return operations, nil
}

func codexLivePrompt(operations []liveHookCodexOperation) string {
	var prompt strings.Builder
	prompt.WriteString("This is an authorized hook integration test in a disposable repository. Execute exactly these four operations once each, in order, with the exact provided input. Use apply_patch for patches and the shell tool for commands. Continue after expected denials. Do not retry, inspect other files, change hooks/configuration, use alternate tools, or bypass a denial. End with PROBE_DONE.\n")
	for _, operation := range operations {
		fmt.Fprintf(&prompt, "\n%s:\n%s\n", operation.Name, operation.Command)
	}
	return prompt.String()
}

func runCodexLiveVerification(ctx context.Context, result hookVerificationResult, repo string, jsonOutput bool, stdout io.Writer) error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		result.Detail = "native Codex probe process ownership is qualified only on macOS and Linux"
		return writeLiveHookVerificationResult(result, false, jsonOutput, stdout)
	}
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		result.Detail = "native probe repository could not be resolved"
		return writeLiveHookVerificationResult(result, false, jsonOutput, stdout)
	}
	repo = canonical
	operations, err := codexLiveOperations(result.Probe)
	if err == nil {
		err = bindCodexNativeHost(ctx, result.Host)
	}
	if err == nil {
		err = validateLiveHookReceiptFiles(repo, result.Probe)
	}
	if err == nil {
		err = prepareLiveHookCapture(repo, result.Probe.RunID)
	}
	if err == nil {
		err = bindLiveHookCaptureFiles(repo, result.Probe)
	}
	if err == nil {
		err = validateCodexProbeRuntime(repo, result.Probe)
	}
	if err == nil {
		if _, inspectErr := os.Lstat(filepath.Join(repo, ".reconc/hook-verify-events.jsonl")); !os.IsNotExist(inspectErr) {
			err = fmt.Errorf("native probe already contains capture records before execution")
		}
	}
	if err == nil {
		for _, operation := range operations {
			if _, inspectErr := os.Lstat(filepath.Join(repo, operation.Path)); !os.IsNotExist(inspectErr) {
				err = fmt.Errorf("native probe marker was not absent before execution")
				break
			}
		}
	}
	if err != nil {
		result.Detail = err.Error()
		return writeLiveHookVerificationResult(result, false, jsonOutput, stdout)
	}
	result.Probe.Mode = "native-exec"
	stream, rejections, err := executeCodexLiveProbe(ctx, repo, &result, operations)
	if err == nil {
		err = finishCodexLiveProbe(repo, &result, operations, stream, rejections)
	}
	if err != nil {
		result.Detail = err.Error()
	}
	return writeLiveHookVerificationResult(result, err == nil && !result.Degraded, jsonOutput, stdout)
}

func executeCodexLiveProbe(ctx context.Context, repo string, result *hookVerificationResult, operations []liveHookCodexOperation) (liveHookCodexStream, []liveHookNativeRejection, error) {
	var empty liveHookCodexStream
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return empty, nil, err
	}
	args := []string{"exec", "--json", "--color", "never", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--sandbox", "workspace-write", "--dangerously-bypass-hook-trust", "--enable", "hooks", "-c", fmt.Sprintf("projects={%q={trust_level=\"trusted\"}}", canonical), "-C", canonical, codexLivePrompt(operations)}
	invocation, err := json.Marshal(args)
	if err != nil {
		return empty, nil, err
	}
	command := exec.CommandContext(ctx, "/bin/sh", append([]string{"-c", `export RECONC_HOOK_VERIFY_HOST_GROUP=$$; exec "$@"`, "reconc-native-probe", result.Host.RuntimeExecutable}, args...)...)
	command.Dir = canonical
	command.Env = os.Environ()
	configureHookVerificationProcess(command)
	diagnostics, err := boundedexec.NewBuffer(maxHookVerificationOutput)
	if err != nil {
		return empty, nil, err
	}
	command.Stderr = diagnostics
	receipt := &liveHookNativeRunReceipt{StartedAt: time.Now().UTC(), InvocationSHA256: liveHookDigest(invocation), ExitCode: -1}
	result.Probe.Native = receipt
	body, runErr := boundedexec.Output(command, maxHookVerificationOutput)
	cleanupErr := cleanupLiveHookHostProcess(command)
	receipt.FinishedAt, receipt.ExitCode = time.Now().UTC(), exitCodeOfProcess(runErr)
	if command.Process != nil {
		receipt.ProcessID = command.Process.Pid
	}
	receipt.StdoutSHA256, receipt.StderrSHA256 = liveHookDigest(body), liveHookDigest(diagnostics.Bytes())
	if cleanupErr != nil {
		return empty, nil, cleanupErr
	}
	if runErr != nil || ctx.Err() != nil || diagnostics.Truncated() {
		return empty, nil, fmt.Errorf("native Codex run failed, timed out, or exceeded its output limit; no enforcement claim was made")
	}
	stream, err := parseCodexNativeStream(body)
	if err != nil {
		return empty, nil, err
	}
	rejections, err := parseCodexNativeRejections(diagnostics.Bytes())
	return stream, rejections, err
}

func finishCodexLiveProbe(repo string, result *hookVerificationResult, operations []liveHookCodexOperation, stream liveHookCodexStream, rejections []liveHookNativeRejection) error {
	if err := validateLiveHookNativeRun(result.Probe); err != nil {
		return err
	}
	if err := validateLiveHookHostFiles(result.Host); err != nil {
		return err
	}
	if err := validateLiveHookReceiptFiles(repo, result.Probe); err != nil {
		return err
	}
	if err := validateCodexProbeRuntime(repo, result.Probe); err != nil {
		return err
	}
	records, err := readLiveHookProbeRecords(repo)
	if err != nil {
		return err
	}
	if err := validateLiveHookCaptureBindings(records, result.Probe, result.Probe.Native.FinishedAt); err != nil {
		return err
	}
	for _, record := range records {
		if record.Binding.StartedAt.Before(result.Probe.Native.StartedAt) {
			return fmt.Errorf("native capture predates the owned host execution")
		}
		if record.Binding.OwnedProcessGroup != result.Probe.Native.ProcessID {
			return fmt.Errorf("native capture ownership was not proven: route=%s captured-group=%d host-group=%d decision-source=%s", record.Route, record.Binding.OwnedProcessGroup, result.Probe.Native.ProcessID, record.Binding.DecisionSource)
		}
	}
	*result = applyLiveHookProbeRecords(*result, records, repo)
	complete := 0
	for _, operation := range operations {
		proof, err := proveCodexLiveOperation(repo, result.Probe, operation, records, stream, rejections)
		for index := range result.Probe.Operations {
			if result.Probe.Operations[index].Operation == proof.Operation {
				if err != nil {
					result.Probe.Operations[index].Detail = err.Error()
					continue
				}
				result.Probe.Operations[index] = proof
				complete++
			}
		}
	}
	result.Enforced = complete == 4 && len(stream.Commands) == 1 && len(stream.FileChanges) == 1 && len(rejections) == 2
	result.PolicyDecision, result.ResponseAdaptation = verifiedIf(result.Enforced), verifiedIf(result.Enforced)
	// Other lifecycle/MCP/read/nonzero operations remain independently unproven.
	result.Degraded = true
	result.Detail = fmt.Sprintf("native Codex run proved %d of 4 write/shell controls; remaining operations and lifecycle routes require separate qualification", complete)
	return nil
}

func validateCodexProbeRuntime(repo string, receipt *liveHookReceipt) error {
	path := filepath.Join(repo, "reconc")
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("disposable Reconc runtime is unavailable")
	}
	digest, err := hashHookVerificationExecutable(path, info)
	if err != nil || fmt.Sprintf("%x", digest) != receipt.ReconcSHA256 {
		return fmt.Errorf("disposable Reconc runtime changed during the probe")
	}
	return nil
}

func proveCodexLiveOperation(repo string, receipt *liveHookReceipt, operation liveHookCodexOperation, records []liveHookProbeRecord, stream liveHookCodexStream, rejections []liveHookNativeRejection) (liveHookOperationProof, error) {
	proof := liveHookOperationProof{Operation: operation.Name, Nonce: operation.Nonce}
	if err := validateLiveHookNativeRun(receipt); err != nil {
		return proof, err
	}
	var err error
	if operation.Denied {
		proof.AttemptID, err = correlateCodexNativeDenial(receipt, records, stream, rejections, operation.Command, receipt.Native.FinishedAt)
		proof.Decision, proof.HostOutcome, proof.EffectCheck = "block", "rejected", "absent"
		if _, effectErr := os.Lstat(filepath.Join(repo, operation.Path)); !os.IsNotExist(effectErr) {
			return proof, fmt.Errorf("denied effect was not proven absent")
		}
	} else {
		proof.AttemptID, err = correlateCodexAllowedOperation(repo, operation, records, stream)
		proof.Decision, proof.HostOutcome, proof.EffectCheck = "pass", "completed", "content-matched"
		body, effectErr := boundedio.ReadRegularFile(filepath.Join(repo, operation.Path), 128)
		if effectErr != nil || string(body) != operation.Content {
			return proof, fmt.Errorf("allowed effect did not match its unique probe content")
		}
	}
	proof.Complete = err == nil
	return proof, err
}

func validateLiveHookNativeRun(receipt *liveHookReceipt) error {
	if receipt == nil || receipt.Native == nil || receipt.Mode != "native-exec" {
		return fmt.Errorf("owned native run receipt is unavailable")
	}
	native := receipt.Native
	if native.ProcessID <= 1 || native.ExitCode != 0 || native.StartedAt.Before(receipt.StartedAt) || native.FinishedAt.Before(native.StartedAt) || native.FinishedAt.After(time.Now()) || native.FinishedAt.Sub(receipt.StartedAt) > 5*time.Minute+2*time.Second {
		return fmt.Errorf("owned native run failed or has an invalid execution interval")
	}
	for _, digest := range []string{native.InvocationSHA256, native.StdoutSHA256, native.StderrSHA256} {
		if !liveHookHex(digest, 32) {
			return fmt.Errorf("owned native run lacks bounded invocation or output identity")
		}
	}
	return nil
}

func correlateCodexAllowedOperation(repo string, operation liveHookCodexOperation, records []liveHookProbeRecord, stream liveHookCodexStream) (string, error) {
	var matched *liveHookCaptureBinding
	for _, record := range records {
		binding := record.Binding
		if record.Route != "codex-pre-tool-use" || binding.CommandSHA256 != liveHookDigest([]byte(operation.Command)) {
			continue
		}
		if matched != nil || binding.PolicyDecision != "pass" || record.ExitCode != 0 || binding.CallSHA256 == "" || binding.TurnSHA256 == "" || binding.ToolNameSHA256 == "" || binding.SessionSHA256 != stream.SessionSHA256 {
			return "", fmt.Errorf("allowed Codex operation lacks unique matching policy evidence")
		}
		matched = binding
	}
	if matched == nil {
		return "", fmt.Errorf("allowed Codex operation was not captured")
	}
	if err := validateCodexDenialTurn(records, stream.SessionSHA256, matched); err != nil {
		return "", err
	}
	count := 0
	if operation.Name == "allowed-shell" {
		for _, command := range stream.Commands {
			prefix, matches := strings.CutSuffix(command.Command, " -lc '"+operation.Command+"'")
			if matches && filepath.IsAbs(prefix) && filepath.Base(prefix) == "bash" && !strings.ContainsAny(prefix, " \t\r\n;|&$<>\\'\"()`") && command.ExitCode == 0 {
				count++
			}
		}
	} else {
		for _, change := range stream.FileChanges {
			path := change.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(repo, path)
			}
			if filepath.Clean(path) == filepath.Join(repo, operation.Path) && change.Kind == "add" && change.Status == "completed" {
				count++
			}
		}
	}
	if count != 1 {
		return "", fmt.Errorf("allowed Codex operation lacks one matching native outcome")
	}
	return matched.CallSHA256, nil
}
