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
	preDecisionCacheVersion    = "pre-decision-v2"
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
// tool-call identity, policy bytes, session-state bytes, repository taint bytes,
// and the bounded repository Git-alias snapshot. The key is sampled again after
// reading the cache and after evaluation, so a concurrent evidence, policy, or
// alias mutation cannot validate or warm a stale record.
func runPreDecisionResolvedWithEvaluator(root string, payloadBytes []byte, permission bool, evaluator *runtime.Evaluator) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return adaptPreDecision(Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}, permission)
	}
	inputs, cacheable := preDecisionInputsForPayload(root, payload)
	if cacheable {
		if cached, ok := readPreDecisionCacheForInputs(root, payload, inputs); ok {
			return adaptPreDecision(cached, permission)
		}
	}

	decision := runPreToolUseParsedWithEvaluatorAndAliasSnapshot(root, payload, evaluator, inputs.aliasSnapshot)
	if postInputs, ok := resamplePreDecisionInputs(root, payload, inputs); cacheable && ok &&
		inputs.identity.equal(postInputs.identity) {
		_ = writePreDecisionCacheForPayload(root, payload, postInputs.key, decision)
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
	if err != nil {
		return "", false
	}
	return preDecisionKeyForPayload(root, payload)
}

func preDecisionKeyForPayload(root string, payload *HookPayload) (string, bool) {
	inputs, ok := preDecisionInputsForPayload(root, payload)
	return inputs.key, ok
}

type preDecisionInputs struct {
	identity      preDecisionIdentity
	key           string
	aliasSnapshot gitAliasSnapshot
}

type preDecisionIdentity struct {
	payload      string
	policyLock   string
	policySource string
	session      string
	taint        string
	alias        string
}

func (identity preDecisionIdentity) key() string {
	hash := sha256.New()
	for _, part := range []string{
		preDecisionCacheVersion,
		identity.payload,
		identity.policyLock,
		identity.policySource,
		identity.session,
		identity.taint,
		identity.alias,
	} {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (identity preDecisionIdentity) equal(other preDecisionIdentity) bool {
	return identity == other
}

func preDecisionInputsForPayload(root string, payload *HookPayload) (preDecisionInputs, bool) {
	if payload == nil || strings.TrimSpace(payload.SessionID) == "" || strings.TrimSpace(payload.ToolUseID) == "" {
		return preDecisionInputs{}, false
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
		return preDecisionInputs{}, false
	}
	inputs := preDecisionInputs{
		identity: preDecisionIdentity{payload: string(payloadIdentity)},
	}
	if !capturePreDecisionObservedIdentity(root, payload, &inputs) {
		return preDecisionInputs{}, false
	}
	inputs.key = inputs.identity.key()
	return inputs, true
}

func capturePreDecisionObservedIdentity(
	root string,
	payload *HookPayload,
	inputs *preDecisionInputs,
) bool {
	if payload == nil || inputs == nil {
		return false
	}
	policyIdentity, ok := hashPreDecisionFile(filepath.Join(root, policyLockfilePath), false)
	if !ok {
		return false
	}
	policySourceIdentity, ok := preDecisionPolicySourceIdentity(root)
	if !ok {
		return false
	}
	stateIdentity, ok := preDecisionSessionIdentity(root, payload.SessionID)
	if !ok {
		return false
	}
	taintIdentity, ok := hashPreDecisionFile(evidenceTaintPath(root), true)
	if !ok {
		return false
	}
	inputs.identity.policyLock = policyIdentity
	inputs.identity.policySource = policySourceIdentity
	inputs.identity.session = stateIdentity
	inputs.identity.taint = taintIdentity
	inputs.identity.alias = "not-applicable"
	if payload.IsCommandTool() {
		inputs.aliasSnapshot = captureGitAliasSnapshot(root)
		aliasIdentity, ok := inputs.aliasSnapshot.identityValue()
		if !ok {
			return false
		}
		inputs.identity.alias = aliasIdentity
	}
	return true
}

func resamplePreDecisionInputs(
	root string,
	payload *HookPayload,
	baseline preDecisionInputs,
) (preDecisionInputs, bool) {
	if payload == nil || baseline.identity.payload == "" {
		return preDecisionInputs{}, false
	}
	inputs := preDecisionInputs{
		identity: preDecisionIdentity{payload: baseline.identity.payload},
	}
	if !capturePreDecisionObservedIdentity(root, payload, &inputs) {
		return preDecisionInputs{}, false
	}
	inputs.key = inputs.identity.key()
	return inputs, true
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
	return preDecisionCachePathForPayload(root, payload)
}

func preDecisionCachePathForPayload(root string, payload *HookPayload) string {
	if payload == nil {
		return ""
	}
	return filepath.Join(projectDir(root), "pre-decisions", sessionFileKey(payload.SessionID)+".json")
}

func readPreDecisionCache(root string, payloadBytes []byte, expectedKey string) (Result, bool) {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{}, false
	}
	return readPreDecisionCacheForPayload(root, payload, expectedKey)
}

func readPreDecisionCacheForPayload(root string, payload *HookPayload, expectedKey string) (Result, bool) {
	inputs, ok := preDecisionInputsForPayload(root, payload)
	if !ok || inputs.key != expectedKey {
		return Result{}, false
	}
	return readPreDecisionCacheForInputs(root, payload, inputs)
}

func readPreDecisionCacheForInputs(
	root string,
	payload *HookPayload,
	expected preDecisionInputs,
) (Result, bool) {
	path := preDecisionCachePathForPayload(root, payload)
	if path == "" {
		return Result{}, false
	}
	body, err := boundedio.ReadRegularFile(path, maxPreDecisionCacheBytes)
	if err != nil {
		return Result{}, false
	}
	var cached preDecisionCache
	if json.Unmarshal(body, &cached) != nil || cached.FormatVersion != preDecisionCacheVersion || cached.Key != expected.key ||
		(cached.ExitCode != 0 && cached.ExitCode != 2) || len(cached.Stderr) > maxPreDecisionDiagnostic {
		return Result{}, false
	}
	current, ok := resamplePreDecisionInputs(root, payload, expected)
	if !ok || !expected.identity.equal(current.identity) {
		return Result{}, false
	}
	return Result{ExitCode: cached.ExitCode, Stderr: cached.Stderr}, true
}

func writePreDecisionCacheForPayload(root string, payload *HookPayload, key string, decision Result) error {
	if decision.ExitCode != 0 && decision.ExitCode != 2 || decision.Stdout != "" || len(decision.Stderr) > maxPreDecisionDiagnostic {
		return nil
	}
	path := preDecisionCachePathForPayload(root, payload)
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
	_, err = atomicfile.WritePrivateIfChanged(path, body, 0o600)
	return err
}
