package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/hooks"
)

func TestCodexLiveOperationsUseFreshIdentityAndRealPolicy(t *testing.T) {
	repo := t.TempDir()
	if err := initializeHookVerificationRepo(repo, false); err != nil {
		t.Fatal(err)
	}
	installation, err := hooks.Install(hooks.KindCodex, repo, false)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := newLiveHookReceipt(repo, installation)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := codexLiveOperations(receipt)
	if err != nil || len(operations) != 4 {
		t.Fatalf("operations=%v error=%v", operations, err)
	}
	for _, operation := range operations {
		t.Run(operation.Name, func(t *testing.T) {
			tool := "Bash"
			if strings.HasSuffix(operation.Name, "write") {
				tool = "apply_patch"
			}
			payload := struct {
				Session string `json:"session_id"`
				Tool    string `json:"tool_name"`
				Input   struct {
					Command string `json:"command"`
				} `json:"tool_input"`
			}{Session: operation.Nonce, Tool: tool}
			payload.Input.Command = operation.Command
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			err = runHookRuntimeWithInput([]string{"codex-pre-tool-use", repo}, bytes.NewReader(body), &stdout, &stderr)
			code := ExitCode(err)
			if (code == 2) != operation.Denied || code != 0 && code != 2 {
				t.Fatalf("denied=%t exit=%d stdout=%s stderr=%s", operation.Denied, code, &stdout, &stderr)
			}
		})
	}
	for _, change := range []string{"duplicate-nonce", "duplicate-operation", "missing-control", "invalid-nonce"} {
		copy := *receipt
		copy.Operations = append([]liveHookOperationProof(nil), receipt.Operations...)
		switch change {
		case "duplicate-nonce":
			copy.Operations[1].Nonce = copy.Operations[0].Nonce
		case "duplicate-operation":
			copy.Operations[1].Operation = copy.Operations[0].Operation
		case "missing-control":
			copy.Operations = copy.Operations[:2]
		case "invalid-nonce":
			copy.Operations[0].Nonce = "unbound"
		}
		if _, err := codexLiveOperations(&copy); err == nil {
			t.Fatalf("accepted %s", change)
		}
	}
}

func TestLiveHookNativeIdentityRejectsScriptAndChangedRuntime(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !liveHookNativeExecutable(executable) {
		t.Fatal("test executable was not recognized as native")
	}
	path := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if liveHookNativeExecutable(path) {
		t.Fatal("script was promoted to native executable")
	}
	info, err := os.Lstat(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := hashHookVerificationExecutable(executable, info)
	if err != nil {
		t.Fatal(err)
	}
	host := &liveHookHostIdentity{Executable: executable, EntrypointSHA256: liveHookDigest([]byte("#!/bin/sh\nexit 0\n")), RuntimeExecutable: executable, RuntimeSHA256: strings.Repeat("0", 64), IdentityVerified: true}
	host.Executable = path
	host.RuntimeSHA256 = fmt.Sprintf("%x", digest)
	if err := validateLiveHookHostFiles(host); err != nil {
		t.Fatal(err)
	}
	host.RuntimeSHA256 = strings.Repeat("0", 64)
	if err := validateLiveHookHostFiles(host); err == nil {
		t.Fatal("changed native runtime checksum accepted")
	}
}

func TestCodexAllowedProofRequiresNativeOutcomeAndIndependentContent(t *testing.T) {
	for _, change := range []string{"valid", "missing-effect", "wrong-content", "missing-native-outcome", "duplicate-native-outcome", "shell-prefix-injection", "failed-native-process", "missing-native-receipt"} {
		t.Run(change, func(t *testing.T) {
			repo := t.TempDir()
			record, receipt := liveHookBoundCaptureFixture(t)
			operation := liveHookCodexOperation{Name: "allowed-shell", Nonce: strings.Repeat("b", 32), Path: "allowed-command-marker", Content: strings.Repeat("b", 32)}
			operation.Command = "printf " + operation.Nonce + " > " + operation.Path
			payload, err := json.Marshal(struct {
				Session string `json:"session_id"`
				Turn    string `json:"turn_id"`
				Call    string `json:"tool_use_id"`
				Tool    string `json:"tool_name"`
				Input   struct {
					Command string `json:"command"`
				} `json:"tool_input"`
			}{Session: "session", Turn: "turn", Call: "call", Tool: "Bash", Input: struct {
				Command string `json:"command"`
			}{operation.Command}})
			if err != nil {
				t.Fatal(err)
			}
			record, err = newLiveHookCaptureRecord(receipt.RunID, "codex-pre-tool-use", payload)
			if err != nil {
				t.Fatal(err)
			}
			record.ExitCode, record.ResultClass = 0, "allowed-or-observed"
			record.Binding.PolicyDecision, record.Binding.Decision, record.Binding.DecisionSource = "pass", "unproven", "empty-response"
			record.Binding.ResponseSHA256 = liveHookDigest(nil)
			prompt := record
			promptBinding := *record.Binding
			prompt.Binding, prompt.Route = &promptBinding, "codex-user-prompt-submit"
			promptBinding.CallSHA256, promptBinding.CommandSHA256 = "", ""
			promptBinding.StartedAt = receipt.StartedAt.Add(time.Millisecond)
			receipt.Mode = "native-exec"
			receipt.Native = &liveHookNativeRunReceipt{StartedAt: receipt.StartedAt, FinishedAt: time.Now(), ProcessID: os.Getpid(), InvocationSHA256: liveHookDigest([]byte("invocation")), StdoutSHA256: liveHookDigest([]byte("native stream")), StderrSHA256: liveHookDigest(nil)}
			stream := liveHookCodexStream{SessionSHA256: record.Binding.SessionSHA256, Commands: []liveHookCodexCommand{{ID: "item", Command: "/bin/bash -lc '" + operation.Command + "'", ExitCode: 0}}}
			content := operation.Content
			switch change {
			case "wrong-content":
				content = "stale marker"
			case "missing-native-outcome":
				stream.Commands = nil
			case "duplicate-native-outcome":
				stream.Commands = append(stream.Commands, stream.Commands[0])
			case "shell-prefix-injection":
				stream.Commands[0].Command = "/bin/evil; /bin/bash -lc '" + operation.Command + "'"
			case "failed-native-process":
				receipt.Native.ExitCode = 1
			case "missing-native-receipt":
				receipt.Native = nil
			}
			if change != "missing-effect" {
				if err := os.WriteFile(filepath.Join(repo, operation.Path), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			proof, err := proveCodexLiveOperation(repo, receipt, operation, []liveHookProbeRecord{prompt, record}, stream, nil)
			if change == "valid" {
				if err != nil || !proof.Complete || proof.AttemptID != record.Binding.CallSHA256 {
					t.Fatalf("proof=%+v error=%v", proof, err)
				}
			} else if err == nil || proof.Complete {
				t.Fatalf("incomplete evidence accepted: %+v error=%v", proof, err)
			}
		})
	}
}
