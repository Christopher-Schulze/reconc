package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/runtime"
)

const (
	preDecisionCacheVersion    = "pre-decision-v1"
	maxPreDecisionCacheBytes   = 16 * 1024
	maxPreDecisionDiagnostic   = 8 * 1024
	maxPreDecisionIdentityFile = 8 * 1024 * 1024
)

type preDecisionCache struct {
	FormatVersion string `json:"format_version"`
	Key           string `json:"key"`
	ExitCode      int    `json:"exit_code"`
	Stderr        string `json:"stderr,omitempty"`
}

// runPreDecisionResolvedWithEvaluator reuses a decision only across identical normalized
// tool-call identity, policy bytes, session-state bytes, and repository taint
// bytes. The key is sampled again after reading the cache, so a concurrent
// evidence or policy mutation cannot validate a stale record.
func runPreDecisionResolvedWithEvaluator(root string, payloadBytes []byte, permission bool, evaluator *runtime.Evaluator) Result {
	key, cacheable := preDecisionKey(root, payloadBytes)
	if cacheable {
		if cached, ok := readPreDecisionCache(root, payloadBytes, key); ok {
			return adaptPreDecision(cached, permission)
		}
	}

	decision := runPreToolUseResolvedWithEvaluator(root, payloadBytes, evaluator)
	if postKey, ok := preDecisionKey(root, payloadBytes); cacheable && ok && postKey == key {
		_ = writePreDecisionCache(root, payloadBytes, postKey, decision)
	}
	return adaptPreDecision(decision, permission)
}

func adaptPreDecision(decision Result, permission bool) Result {
	if !permission || decision.ExitCode == 0 {
		return decision
	}
	reason := strings.TrimSpace(decision.Stderr)
	if reason == "" {
		reason = "reconc denied this permission request before execution."
	}
	return Result{ExitCode: 0, Stdout: permissionRequestDenyJSONOutput(reason)}
}

func preDecisionKey(root string, payloadBytes []byte) (string, bool) {
	payload, err := ParsePayload(payloadBytes)
	if err != nil || strings.TrimSpace(payload.SessionID) == "" || strings.TrimSpace(payload.ToolUseID) == "" {
		return "", false
	}
	payloadIdentity, err := json.Marshal(struct {
		SessionID string                 `json:"session_id"`
		ToolUseID string                 `json:"tool_use_id"`
		ToolName  string                 `json:"tool_name"`
		ToolInput map[string]interface{} `json:"tool_input"`
	}{
		SessionID: payload.SessionID,
		ToolUseID: payload.ToolUseID,
		ToolName:  payload.ToolName,
		ToolInput: payload.ToolInput,
	})
	if err != nil {
		return "", false
	}
	policyIdentity, ok := hashPreDecisionFile(filepath.Join(root, ".reconc", "policy.lock.json"), false)
	if !ok {
		return "", false
	}
	policySourceIdentity, ok := preDecisionPolicySourceIdentity(root)
	if !ok {
		return "", false
	}
	stateIdentity, ok := preDecisionSessionIdentity(root, payload.SessionID)
	if !ok {
		return "", false
	}
	taintIdentity, ok := hashPreDecisionFile(evidenceTaintPath(root), true)
	if !ok {
		return "", false
	}
	hash := sha256.New()
	for _, part := range [][]byte{
		[]byte(preDecisionCacheVersion), payloadIdentity,
		[]byte(policyIdentity), []byte(policySourceIdentity),
		[]byte(stateIdentity), []byte(taintIdentity),
	} {
		_, _ = hash.Write(part)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), true
}

func preDecisionPolicySourceIdentity(root string) (string, bool) {
	bundle, err := ingest.LoadPolicySources(root)
	if err != nil {
		return "", false
	}
	digest, err := compiler.ComputeSourceDigest(bundle)
	if err != nil || len(digest) != sha256.Size*2 {
		return "", false
	}
	return digest, true
}

func preDecisionSessionIdentity(root, sessionID string) (string, bool) {
	path := sessionStatePath(root, sessionID)
	identity, ok := hashPreDecisionFile(path, true)
	if !ok || identity != "missing" {
		return identity, ok
	}
	legacyPath := legacySessionStatePath(root, sessionID)
	if legacyPath == path {
		return identity, true
	}
	return hashPreDecisionFile(legacyPath, true)
}

func hashPreDecisionFile(path string, allowMissing bool) (string, bool) {
	body, err := boundedio.ReadRegularFile(path, maxPreDecisionIdentityFile)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return "missing", true
	}
	if err != nil {
		return "", false
	}
	return hashBytes(body), true
}

func preDecisionCachePath(root string, payloadBytes []byte) string {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return ""
	}
	return filepath.Join(projectDir(root), "pre-decisions", sessionFileKey(payload.SessionID)+".json")
}

func readPreDecisionCache(root string, payloadBytes []byte, expectedKey string) (Result, bool) {
	path := preDecisionCachePath(root, payloadBytes)
	if path == "" {
		return Result{}, false
	}
	body, err := boundedio.ReadRegularFile(path, maxPreDecisionCacheBytes)
	if err != nil {
		return Result{}, false
	}
	var cached preDecisionCache
	if json.Unmarshal(body, &cached) != nil || cached.FormatVersion != preDecisionCacheVersion || cached.Key != expectedKey ||
		(cached.ExitCode != 0 && cached.ExitCode != 2) || len(cached.Stderr) > maxPreDecisionDiagnostic {
		return Result{}, false
	}
	currentKey, ok := preDecisionKey(root, payloadBytes)
	if !ok || currentKey != expectedKey {
		return Result{}, false
	}
	return Result{ExitCode: cached.ExitCode, Stderr: cached.Stderr}, true
}

func writePreDecisionCache(root string, payloadBytes []byte, key string, decision Result) error {
	if decision.ExitCode != 0 && decision.ExitCode != 2 || decision.Stdout != "" || len(decision.Stderr) > maxPreDecisionDiagnostic {
		return nil
	}
	path := preDecisionCachePath(root, payloadBytes)
	if path == "" {
		return nil
	}
	body, err := json.MarshalIndent(preDecisionCache{
		FormatVersion: preDecisionCacheVersion,
		Key:           key,
		ExitCode:      decision.ExitCode,
		Stderr:        decision.Stderr,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pre-decision cache: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxPreDecisionCacheBytes {
		return nil
	}
	if err := ensurePrivateStateDir(filepath.Dir(path)); err != nil {
		return err
	}
	_, err = atomicfile.WriteIfChanged(path, body, 0o600)
	return err
}
