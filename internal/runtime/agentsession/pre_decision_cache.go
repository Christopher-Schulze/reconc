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
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/runtime"
)

const (
	preDecisionCacheVersion       = "pre-decision-v3"
	maxPreDecisionCacheBytes      = 16 * 1024
	maxPreDecisionDiagnostic      = 8 * 1024
	maxPreDecisionIdentityFile    = 8 * 1024 * 1024
	maxPreDecisionDependencyPaths = 2048
	maxPreDecisionDependencyBytes = 32 * 1024 * 1024
	maxPreDecisionPathAncestors   = 256
)

type preDecisionCache struct {
	FormatVersion string `json:"format_version"`
	Key           string `json:"key"`
	ExitCode      int    `json:"exit_code"`
	Stderr        string `json:"stderr,omitempty"`
}

// runPreDecisionResolvedWithEvaluator reuses a decision only across identical
// tool-call identity, policy bytes, session-state bytes, dependency snapshots,
// repository taint bytes, and the bounded repository Git-alias snapshot. A
// cache hit samples before lookup and again after reading the candidate. A
// miss is sampled again after evaluation, so a concurrent evidence, policy,
// path, or alias mutation cannot validate or warm a stale record.
func runPreDecisionResolvedWithEvaluator(root string, payloadBytes []byte, permission bool, evaluator *runtime.Evaluator) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return adaptPreDecision(Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}, permission)
	}
	if !preDecisionRequiresPolicy(payload) {
		return adaptPreDecision(Result{}, permission)
	}
	inputs, cacheable := preDecisionInputsForPayloadWithEvaluator(root, payload, evaluator)
	// Approval-gated writes must reach the live pre-write path on every call.
	// Reusing a claim-only decision here could skip receipt verification, and a
	// receipt is deliberately consumed only after the final policy generation
	// and effect binding have been checked.
	if cacheable && (payload.IsWriteTool() || payload.IsCommandTool()) {
		pending := withoutAgentMemoryPaths(root, payload.FilePaths())
		if envelope, present, envelopeErr := nativeApprovalEnvelopeFromPayload(payload); present || envelopeErr != nil || len(envelope.request) > 0 {
			cacheable = false
		}
		if payload.IsWriteTool() {
			if len(pending) == 0 {
				cacheable = false
			} else {
				normalized, normalizeErr := runtime.NormalizeReplayInputs(root, runtime.ExecutionInputs{WritePaths: pending})
				if normalizeErr != nil {
					cacheable = false
				} else if boundIDs, boundErr := boundApprovalRuleIDs(evaluator, root, normalized.WritePaths); boundErr != nil || len(boundIDs) > 0 {
					cacheable = false
				}
			}
		}
		if payload.IsCommandTool() {
			compiled, _, compiledErr := evaluator.CurrentCompiledPolicyEvaluator(root)
			if compiledErr != nil || compiled.HasBoundApprovalRules() {
				cacheable = false
			}
		}
	}
	cached, cachedOK := readPreDecisionCacheCandidate(root, payload)
	evaluationInputs := inputs
	if cacheable && cachedOK && cached.Key == inputs.key {
		if current, ok := resamplePreDecisionInputsWithEvaluator(root, payload, inputs, evaluator); ok &&
			inputs.identity.equal(current.identity) && cached.Key == current.key {
			return adaptPreDecision(Result{ExitCode: cached.ExitCode, Stderr: cached.Stderr}, permission)
		} else if ok {
			evaluationInputs = current
		}
	}

	decision := runPreToolUseParsedWithEvaluatorAndAliasSnapshot(root, payload, evaluator, evaluationInputs.aliasSnapshot)
	if postInputs, ok := resamplePreDecisionInputsWithEvaluator(root, payload, evaluationInputs, evaluator); cacheable && ok &&
		evaluationInputs.identity.equal(postInputs.identity) {
		_ = writePreDecisionCacheForPayload(root, payload, postInputs.key, decision)
	}
	return adaptPreDecision(decision, permission)
}

func preDecisionRequiresPolicy(payload *HookPayload) bool {
	return payload != nil && (payload.IsWriteTool() || payload.IsCommandTool())
}

func adaptPreDecision(decision Result, permission bool) Result {
	if !permission || decision.ExitCode == 0 {
		return decision
	}
	reason := strings.TrimSpace(decision.Stderr)
	if reason == "" {
		reason = "reconc denied this permission request before execution."
	}
	body, err := permissionRequestDenyJSONOutput(reason)
	if err != nil {
		return resultWithEncodingError(Result{ExitCode: 2}, err)
	}
	return Result{ExitCode: 0, Stdout: body}
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
	dependencies string
	taint        string
	alias        string
}

// preDecisionDependencySnapshot binds the paths and complete sealed evidence
// view consumed by the pre-hook. The snapshot is reduced to a digest before it
// enters the cache key so a large evidence history cannot enlarge cache files.
type preDecisionDependencySnapshot struct {
	Paths    []preDecisionPathIdentity `json:"paths"`
	Evidence string                    `json:"evidence"`
}

type preDecisionPathIdentity struct {
	Raw         string `json:"raw"`
	Resolved    string `json:"resolved"`
	Observation string `json:"observation"`
}

type preDecisionPathObservation struct {
	Exists             bool                             `json:"exists"`
	Mode               uint32                           `json:"mode"`
	Size               int64                            `json:"size"`
	ModTime            int64                            `json:"mod_time"`
	Generation         string                           `json:"generation,omitempty"`
	ResolvedMode       uint32                           `json:"resolved_mode,omitempty"`
	ResolvedSize       int64                            `json:"resolved_size,omitempty"`
	ResolvedModTime    int64                            `json:"resolved_mod_time,omitempty"`
	ResolvedGeneration string                           `json:"resolved_generation,omitempty"`
	Content            string                           `json:"content,omitempty"`
	Missing            []string                         `json:"missing,omitempty"`
	Ancestors          []preDecisionAncestorObservation `json:"ancestors,omitempty"`
}

type preDecisionAncestorObservation struct {
	Path       string `json:"path"`
	Generation string `json:"generation"`
	Mode       uint32 `json:"mode"`
	Size       int64  `json:"size"`
	ModTime    int64  `json:"mod_time"`
	Identity   string `json:"identity"`
}

type preDecisionEvidenceSegmentIdentity struct {
	Digest     string `json:"digest"`
	Generation string `json:"generation"`
	Mode       uint32 `json:"mode"`
	Size       int64  `json:"size"`
	ModTime    int64  `json:"mod_time"`
	Identity   string `json:"identity"`
}

type preDecisionEvidencePrefixIdentity struct {
	Count    uint64                               `json:"count"`
	Head     string                               `json:"head"`
	Segments []preDecisionEvidenceSegmentIdentity `json:"segments"`
}

func (snapshot preDecisionDependencySnapshot) identity() string {
	body, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func (snapshot preDecisionEvidencePrefixIdentity) identity() string {
	body, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func (identity preDecisionIdentity) key() string {
	hash := sha256.New()
	for _, part := range []string{
		preDecisionCacheVersion,
		identity.payload,
		identity.policyLock,
		identity.policySource,
		identity.session,
		identity.dependencies,
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
	return preDecisionInputsForPayloadWithEvaluator(root, payload, nil)
}

func preDecisionInputsForPayloadWithEvaluator(root string, payload *HookPayload, evaluator *runtime.Evaluator) (preDecisionInputs, bool) {
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
	if !capturePreDecisionObservedIdentityWithEvaluator(root, payload, &inputs, evaluator) {
		return preDecisionInputs{}, false
	}
	inputs.key = inputs.identity.key()
	return inputs, true
}

func capturePreDecisionObservedIdentityWithEvaluator(
	root string,
	payload *HookPayload,
	inputs *preDecisionInputs,
	evaluator *runtime.Evaluator,
) bool {
	if payload == nil || inputs == nil {
		return false
	}
	policyIdentity, ok := hashPreDecisionFile(filepath.Join(root, policyLockfilePath), false)
	if !ok {
		return false
	}
	policySourceIdentity := ""
	if evaluator == nil {
		policySourceIdentity, ok = preDecisionPolicySourceIdentity(root)
		if !ok {
			return false
		}
	} else {
		var err error
		_, policySourceIdentity, err = evaluator.CurrentCompiledPolicyEvaluator(root)
		if err != nil || len(policySourceIdentity) != sha256.Size*2 {
			return false
		}
	}
	stateIdentity, state, evidenceIdentity, ok := preDecisionSessionDependencies(root, payload.SessionID)
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
	dependencies, ok := capturePreDecisionDependencySnapshot(root, payload, state, evidenceIdentity)
	if !ok {
		return false
	}
	inputs.identity.dependencies = dependencies.identity()
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

func preDecisionSessionDependencies(root, sessionID string) (string, SessionState, string, bool) {
	stateIdentityBefore, ok := preDecisionSessionIdentity(root, sessionID)
	if !ok {
		return "", SessionState{}, "", false
	}
	state, err := loadSessionStateWithLockResolved(root, sessionID)
	if err != nil {
		return "", SessionState{}, "", false
	}
	stateIdentityAfter, ok := preDecisionSessionIdentity(root, sessionID)
	if !ok || stateIdentityBefore != stateIdentityAfter {
		return "", SessionState{}, "", false
	}
	var prefix verifiedEvidencePrefix
	complete, err := loadCompleteSessionEvidenceWithCacheCapture(root, state, nil, &prefix)
	if err != nil {
		return "", SessionState{}, "", false
	}
	evidence := preDecisionEvidencePrefixIdentity{Count: prefix.count, Head: prefix.head}
	if state.EvidenceSegmentCount == 0 {
		evidence.Count = 0
		evidence.Head = ""
	} else if prefix.count != state.EvidenceSegmentCount || prefix.head != state.EvidenceSegmentDigest ||
		uint64(len(prefix.segments)) != prefix.count {
		return "", SessionState{}, "", false
	}
	evidence.Segments = make([]preDecisionEvidenceSegmentIdentity, 0, len(prefix.segments))
	for _, segment := range prefix.segments {
		if segment.identity == nil || segment.generation == "" || segment.digest == "" {
			return "", SessionState{}, "", false
		}
		info := segment.identity
		evidence.Segments = append(evidence.Segments, preDecisionEvidenceSegmentIdentity{
			Digest: segment.digest, Generation: segment.generation,
			Mode: uint32(info.Mode()), Size: info.Size(), ModTime: info.ModTime().UnixNano(),
			Identity: preDecisionFileInfoIdentity(info),
		})
	}
	identity := evidence.identity()
	if identity == "" {
		return "", SessionState{}, "", false
	}
	return stateIdentityAfter, complete, identity, true
}

func capturePreDecisionDependencySnapshot(
	root string,
	payload *HookPayload,
	state SessionState,
	evidence string,
) (preDecisionDependencySnapshot, bool) {
	paths := make([]string, 0, len(state.ReadPaths)+len(state.WritePaths)+4)
	paths = append(paths, filterRepoScopedReadPaths(root, state.ReadPaths)...)
	paths = append(paths, state.WritePaths...)
	if payload != nil && payload.IsWriteTool() {
		paths = append(paths, withoutAgentMemoryPaths(root, payload.FilePaths())...)
	}
	paths = dedupePreDecisionPaths(paths)
	if len(paths) > maxPreDecisionDependencyPaths {
		return preDecisionDependencySnapshot{}, false
	}
	snapshot := preDecisionDependencySnapshot{
		Paths: make([]preDecisionPathIdentity, 0, len(paths)), Evidence: evidence,
	}
	var totalBytes int64
	for _, raw := range paths {
		identity, bytesRead, ok := observePreDecisionPath(root, raw)
		if !ok || totalBytes > maxPreDecisionDependencyBytes-bytesRead {
			return preDecisionDependencySnapshot{}, false
		}
		totalBytes += bytesRead
		snapshot.Paths = append(snapshot.Paths, identity)
	}
	if snapshot.identity() == "" {
		return preDecisionDependencySnapshot{}, false
	}
	return snapshot, true
}

func dedupePreDecisionPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result
}

func observePreDecisionPath(root, raw string) (preDecisionPathIdentity, int64, bool) {
	candidate := preDecisionPathCandidate(root, raw)
	resolved, err := pathidentity.ResolveProspective(candidate)
	if err != nil {
		return preDecisionPathIdentity{}, 0, false
	}
	ancestors, missing, ok := capturePreDecisionAncestors(root, candidate)
	if !ok {
		return preDecisionPathIdentity{}, 0, false
	}
	info, err := os.Lstat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return observePreDecisionMissingPath(root, raw, candidate, resolved, ancestors, missing)
	}
	if err != nil {
		return preDecisionPathIdentity{}, 0, false
	}
	generation, reliable := platformFileGeneration(candidate, info)
	if !reliable {
		return preDecisionPathIdentity{}, 0, false
	}
	observation, bytesRead, ok := observePreDecisionExistingPath(candidate, resolved, info, generation, ancestors, missing)
	if !ok {
		return preDecisionPathIdentity{}, 0, false
	}
	body, err := json.Marshal(observation)
	if err != nil {
		return preDecisionPathIdentity{}, 0, false
	}
	if !preDecisionPathStable(root, candidate, resolved, &observation, ancestors, missing) {
		return preDecisionPathIdentity{}, 0, false
	}
	return preDecisionPathIdentity{Raw: raw, Resolved: resolved, Observation: hashBytes(body)}, bytesRead, true
}

func preDecisionPathCandidate(root, raw string) string {
	candidate := filepath.FromSlash(raw)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	return filepath.Clean(candidate)
}

func observePreDecisionMissingPath(
	root, raw, candidate, resolved string,
	ancestors []preDecisionAncestorObservation,
	missing []string,
) (preDecisionPathIdentity, int64, bool) {
	observation, err := json.Marshal(preDecisionPathObservation{Missing: missing, Ancestors: ancestors})
	if err != nil || !preDecisionPathStable(root, candidate, resolved, nil, ancestors, missing) {
		return preDecisionPathIdentity{}, 0, false
	}
	return preDecisionPathIdentity{Raw: raw, Resolved: resolved, Observation: hashBytes(observation)}, 0, true
}

func observePreDecisionExistingPath(
	candidate, resolved string,
	info os.FileInfo,
	generation string,
	ancestors []preDecisionAncestorObservation,
	missing []string,
) (preDecisionPathObservation, int64, bool) {
	observation := preDecisionPathObservation{
		Exists: true, Mode: uint32(info.Mode()), Size: info.Size(),
		ModTime: info.ModTime().UnixNano(), Generation: generation,
		Missing: missing, Ancestors: ancestors,
	}
	resolvedInfo, err := os.Stat(candidate)
	if err != nil {
		return observation, 0, true
	}
	resolvedGeneration, reliable := platformFileGeneration(resolved, resolvedInfo)
	if !reliable {
		return preDecisionPathObservation{}, 0, false
	}
	observation.ResolvedMode = uint32(resolvedInfo.Mode())
	observation.ResolvedSize = resolvedInfo.Size()
	observation.ResolvedModTime = resolvedInfo.ModTime().UnixNano()
	observation.ResolvedGeneration = resolvedGeneration
	if !resolvedInfo.Mode().IsRegular() {
		return observation, 0, true
	}
	if resolvedInfo.Size() > maxPreDecisionIdentityFile {
		return preDecisionPathObservation{}, 0, false
	}
	content, err := boundedio.ReadRegularFile(resolved, maxPreDecisionIdentityFile)
	if err != nil {
		return preDecisionPathObservation{}, 0, false
	}
	observation.Content = hashBytes(content)
	return observation, int64(len(content)), true
}

func preDecisionPathStable(
	root, candidate, resolved string,
	expected *preDecisionPathObservation,
	ancestors []preDecisionAncestorObservation,
	missing []string,
) bool {
	currentResolved, err := pathidentity.ResolveProspective(candidate)
	if err != nil || currentResolved != resolved {
		return false
	}
	currentAncestors, currentMissing, ok := capturePreDecisionAncestors(root, candidate)
	if !ok || preDecisionAncestorSnapshotIdentityFromValues(ancestors, missing) !=
		preDecisionAncestorSnapshotIdentityFromValues(currentAncestors, currentMissing) {
		return false
	}
	if expected == nil {
		_, err := os.Lstat(candidate)
		return errors.Is(err, os.ErrNotExist)
	}
	current, err := os.Lstat(candidate)
	if err != nil {
		return false
	}
	currentGeneration, reliable := platformFileGeneration(candidate, current)
	if !reliable || currentGeneration != expected.Generation ||
		uint32(current.Mode()) != expected.Mode || current.Size() != expected.Size ||
		current.ModTime().UnixNano() != expected.ModTime {
		return false
	}
	resolvedInfo, err := os.Stat(candidate)
	if err != nil || uint32(resolvedInfo.Mode()) != expected.ResolvedMode ||
		resolvedInfo.Size() != expected.ResolvedSize ||
		resolvedInfo.ModTime().UnixNano() != expected.ResolvedModTime {
		return false
	}
	resolvedGeneration, reliable := platformFileGeneration(resolved, resolvedInfo)
	return reliable && resolvedGeneration == expected.ResolvedGeneration
}

func capturePreDecisionAncestors(root, path string) ([]preDecisionAncestorObservation, []string, bool) {
	ancestors := make([]preDecisionAncestorObservation, 0, 8)
	missing := make([]string, 0, 4)
	cursor := filepath.Clean(path)
	boundary := filepath.Clean(root)
	for range maxPreDecisionPathAncestors {
		if cursor == boundary {
			return ancestors, missing, true
		}
		info, err := os.Lstat(cursor)
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, filepath.Base(cursor))
			parent := filepath.Dir(cursor)
			if parent == cursor {
				return nil, nil, false
			}
			cursor = parent
			continue
		}
		if err != nil {
			return nil, nil, false
		}
		generation, reliable := platformFileGeneration(cursor, info)
		if !reliable {
			return nil, nil, false
		}
		ancestors = append(ancestors, preDecisionAncestorObservation{
			Path: cursor, Generation: generation, Mode: uint32(info.Mode()),
			Size: info.Size(), ModTime: info.ModTime().UnixNano(), Identity: preDecisionFileInfoIdentity(info),
		})
		return ancestors, missing, true
	}
	return nil, nil, false
}

func preDecisionAncestorSnapshotIdentityFromValues(ancestors []preDecisionAncestorObservation, missing []string) string {
	body, err := json.Marshal(struct {
		Missing   []string                         `json:"missing"`
		Ancestors []preDecisionAncestorObservation `json:"ancestors"`
	}{Missing: missing, Ancestors: ancestors})
	if err != nil {
		return ""
	}
	return hashBytes(body)
}

func preDecisionFileInfoIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	return fmt.Sprintf("mode=%d;size=%d;mtime=%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
}

func resamplePreDecisionInputs(
	root string,
	payload *HookPayload,
	baseline preDecisionInputs,
) (preDecisionInputs, bool) {
	return resamplePreDecisionInputsWithEvaluator(root, payload, baseline, nil)
}

func resamplePreDecisionInputsWithEvaluator(
	root string,
	payload *HookPayload,
	baseline preDecisionInputs,
	evaluator *runtime.Evaluator,
) (preDecisionInputs, bool) {
	if payload == nil || baseline.identity.payload == "" {
		return preDecisionInputs{}, false
	}
	inputs := preDecisionInputs{
		identity: preDecisionIdentity{payload: baseline.identity.payload},
	}
	if !capturePreDecisionObservedIdentityWithEvaluator(root, payload, &inputs, evaluator) {
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
	cached, ok := readPreDecisionCacheCandidate(root, payload)
	if !ok || cached.Key != expected.key {
		return Result{}, false
	}
	current, ok := resamplePreDecisionInputs(root, payload, expected)
	if !ok || !expected.identity.equal(current.identity) {
		return Result{}, false
	}
	return Result{ExitCode: cached.ExitCode, Stderr: cached.Stderr}, true
}

func readPreDecisionCacheCandidate(root string, payload *HookPayload) (preDecisionCache, bool) {
	path := preDecisionCachePathForPayload(root, payload)
	if path == "" {
		return preDecisionCache{}, false
	}
	body, err := boundedio.ReadRegularFile(path, maxPreDecisionCacheBytes)
	if err != nil {
		return preDecisionCache{}, false
	}
	var cached preDecisionCache
	if json.Unmarshal(body, &cached) != nil || cached.FormatVersion != preDecisionCacheVersion || len(cached.Key) != sha256.Size*2 ||
		(cached.ExitCode != 0 && cached.ExitCode != 2) || len(cached.Stderr) > maxPreDecisionDiagnostic {
		return preDecisionCache{}, false
	}
	return cached, true
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
