package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/runtime"
)

const (
	preDecisionCacheVersion       = "pre-decision-v4"
	maxPreDecisionCacheBytes      = 16 * 1024
	maxPreDecisionDiagnostic      = 8 * 1024
	maxPreDecisionIdentityFile    = 8 * 1024 * 1024
	maxPreDecisionDependencyBytes = 32 * 1024 * 1024
	maxPreDecisionPathAncestors   = 256
)

type preDecisionResultClass string

const (
	preDecisionResultPass  preDecisionResultClass = "pass"
	preDecisionResultBlock preDecisionResultClass = "block"
)

type preDecisionCache struct {
	FormatVersion string                 `json:"format_version"`
	Key           string                 `json:"key"`
	DecisionClass preDecisionResultClass `json:"decision_class"`
	ExitCode      int                    `json:"exit_code"`
	Stderr        string                 `json:"stderr,omitempty"`
}

// runPreDecisionResolvedWithEvaluatorAndStopCache reuses a decision only
// across identical tool-call identity, policy bytes, session-state bytes,
// dependency snapshots, repository taint bytes, and the bounded repository
// Git-alias snapshot. A cache hit samples before lookup and again after
// reading the candidate. A miss is sampled again after evaluation, so a
// concurrent evidence, policy, path, or alias mutation cannot validate or
// warm a stale record.
func runPreDecisionResolvedWithEvaluatorAndStopCache(
	root string,
	payloadBytes []byte,
	permission bool,
	evaluator *runtime.Evaluator,
	stopCache *StopDecisionCache,
) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return adaptPreDecision(Result{ExitCode: 2, Stderr: fmt.Sprintf("reconc hook (pre): %s", err)}, permission)
	}
	if !preDecisionRequiresPolicy(payload) {
		return adaptPreDecision(Result{}, permission)
	}
	inputs, cacheable := preDecisionInputsForPayloadWithEvaluatorAndStopCache(root, payload, evaluator, stopCache)
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
	evaluationInputs := inputs
	if cacheable {
		cached, cachedOK := readPreDecisionCacheCandidate(root, payload)
		if cachedOK && cached.Key == inputs.key {
			if current, ok := resamplePreDecisionInputsWithEvaluatorAndStopCache(root, payload, inputs, evaluator, stopCache); ok &&
				inputs.identity.equal(current.identity) && cached.Key == current.key && current.cacheableAt(time.Now()) {
				return adaptPreDecision(Result{
					ExitCode: cached.ExitCode, Stderr: cached.Stderr, decisionClass: cached.DecisionClass,
				}, permission)
			} else if ok {
				evaluationInputs = current
			} else {
				evaluationInputs.aliasSnapshot = gitAliasSnapshot{}
			}
		}
	}

	decision := runPreToolUseParsedWithEvaluatorAndAliasSnapshotAndStopCache(root, payload, evaluator, evaluationInputs.aliasSnapshot, stopCache)
	if cacheable && decision.decisionClass.cacheable() && evaluationInputs.cacheableAt(time.Now()) {
		if postInputs, ok := resamplePreDecisionInputsWithEvaluatorAndStopCache(root, payload, evaluationInputs, evaluator, stopCache); ok &&
			evaluationInputs.identity.equal(postInputs.identity) && postInputs.cacheableAt(time.Now()) {
			_ = writePreDecisionCacheForPayload(root, payload, postInputs.key, decision)
		}
	}
	return adaptPreDecision(decision, permission)
}

func (class preDecisionResultClass) cacheable() bool {
	return class == preDecisionResultPass || class == preDecisionResultBlock
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
	validUntil    time.Time
	metrics       preDecisionSampleMetrics
}

type preDecisionSampleMetrics struct {
	DependencyPaths   int
	ContentHashPasses int
	ContentHashBytes  int64
}

func (inputs preDecisionInputs) cacheableAt(now time.Time) bool {
	return inputs.validUntil.IsZero() || now.Before(inputs.validUntil)
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
	Paths             []preDecisionPathIdentity `json:"paths"`
	Evidence          string                    `json:"evidence"`
	ScriptEnvironment string                    `json:"script_environment,omitempty"`
}

type preDecisionPathIdentity struct {
	Raw         string `json:"raw"`
	Resolved    string `json:"resolved"`
	Observation string `json:"observation"`
}

type preDecisionPathObservation struct {
	Exists             bool                              `json:"exists"`
	Mode               uint32                            `json:"mode"`
	Size               int64                             `json:"size"`
	ModTime            int64                             `json:"mod_time"`
	Generation         string                            `json:"generation,omitempty"`
	ResolvedMode       uint32                            `json:"resolved_mode,omitempty"`
	ResolvedSize       int64                             `json:"resolved_size,omitempty"`
	ResolvedModTime    int64                             `json:"resolved_mod_time,omitempty"`
	ResolvedGeneration string                            `json:"resolved_generation,omitempty"`
	Content            string                            `json:"content,omitempty"`
	Missing            []string                          `json:"missing,omitempty"`
	Ancestors          []preDecisionAncestorObservation  `json:"ancestors,omitempty"`
	Freshness          []preDecisionFreshnessObservation `json:"freshness,omitempty"`
}

type preDecisionFreshnessObservation struct {
	Hours   int  `json:"hours"`
	Expired bool `json:"expired"`
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
	return preDecisionInputsForPayloadWithEvaluatorAndStopCache(root, payload, evaluator, nil)
}

func preDecisionInputsForPayloadWithEvaluatorAndStopCache(
	root string,
	payload *HookPayload,
	evaluator *runtime.Evaluator,
	stopCache *StopDecisionCache,
) (preDecisionInputs, bool) {
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
	if !capturePreDecisionObservedIdentityWithEvaluatorAndStopCache(root, payload, &inputs, evaluator, stopCache) {
		return preDecisionInputs{}, false
	}
	inputs.key = inputs.identity.key()
	return inputs, true
}

func capturePreDecisionObservedIdentityWithEvaluatorAndStopCache(
	root string,
	payload *HookPayload,
	inputs *preDecisionInputs,
	evaluator *runtime.Evaluator,
	stopCache *StopDecisionCache,
) bool {
	if payload == nil || inputs == nil {
		return false
	}
	policyIdentity, ok := hashPreDecisionFile(filepath.Join(root, policyLockfilePath), false)
	if !ok {
		return false
	}
	if evaluator == nil {
		evaluator = runtime.NewEvaluator()
	}
	compiled, policySourceIdentity, err := evaluator.CurrentCompiledPolicyEvaluator(root)
	if err != nil || len(policySourceIdentity) != sha256.Size*2 {
		return false
	}
	stateIdentity, state, evidenceIdentity, ok := preDecisionSessionDependenciesWithStopCache(root, payload.SessionID, stopCache)
	if !ok {
		return false
	}
	evaluationInputs, route, ok := preDecisionEvaluationInputs(root, payload, state)
	if !ok {
		return false
	}
	dependencyPlan, err := compiled.PreDecisionDependencies(root, evaluationInputs, route)
	if err != nil || !dependencyPlan.Cacheable {
		return false
	}
	taintIdentity, ok := hashPreDecisionFile(evidenceTaintPath(root), true)
	if !ok {
		return false
	}
	inputs.identity.policyLock = policyIdentity
	inputs.identity.policySource = policySourceIdentity
	inputs.identity.session = stateIdentity
	dependencies, validUntil, metrics, ok := capturePreDecisionDependencySnapshot(root, payload, state, evidenceIdentity, dependencyPlan)
	if !ok {
		return false
	}
	inputs.identity.dependencies = dependencies.identity()
	inputs.validUntil = validUntil
	inputs.metrics = metrics
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

func preDecisionEvaluationInputs(
	root string,
	payload *HookPayload,
	state SessionState,
) (runtime.ExecutionInputs, runtime.PreDecisionRoute, bool) {
	if payload == nil {
		return runtime.Empty(), 0, false
	}
	readPaths := filterRepoScopedReadPaths(root, state.ReadPaths)
	if payload.IsCommandTool() {
		return executionInputs(readPaths, state.WritePaths, state.WriteEpochs, []string{payload.Command()}, state.CommandResults, state.Claims),
			runtime.PreDecisionRouteCommand, true
	}
	if !payload.IsWriteTool() {
		return runtime.Empty(), 0, false
	}
	pending := withoutAgentMemoryPaths(root, payload.FilePaths())
	if len(pending) == 0 {
		return runtime.Empty(), 0, false
	}
	trialWrites := append(append([]string(nil), state.WritePaths...), pending...)
	return executionInputs(readPaths, trialWrites, state.WriteEpochs, state.Commands, state.CommandResults, state.Claims),
		runtime.PreDecisionRouteWrite, true
}

func preDecisionSessionDependenciesWithStopCache(root, sessionID string, stopCache *StopDecisionCache) (string, SessionState, string, bool) {
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
	complete, err := loadCompleteSessionEvidenceWithCacheCapture(root, state, stopCache, &prefix)
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
	plan runtime.PreDecisionDependencyPlan,
) (preDecisionDependencySnapshot, time.Time, preDecisionSampleMetrics, bool) {
	paths := make([]preDecisionDependencyRequest, 0, len(state.ReadPaths)+len(state.WritePaths)+len(plan.Dependencies)+4)
	for _, path := range filterRepoScopedReadPaths(root, state.ReadPaths) {
		paths = append(paths, preDecisionDependencyRequest{path: path})
	}
	for _, path := range state.WritePaths {
		paths = append(paths, preDecisionDependencyRequest{path: path})
	}
	if payload != nil && payload.IsWriteTool() {
		for _, path := range withoutAgentMemoryPaths(root, payload.FilePaths()) {
			paths = append(paths, preDecisionDependencyRequest{path: path})
		}
	}
	for _, dependency := range plan.Dependencies {
		paths = append(paths, preDecisionDependencyRequest{
			path: dependency.Path, freshnessHours: dependency.FreshnessHours, contentBound: dependency.ContentBound,
		})
	}
	paths = dedupePreDecisionPaths(paths)
	if len(paths) > runtime.MaxPreDecisionDependencyPaths {
		return preDecisionDependencySnapshot{}, time.Time{}, preDecisionSampleMetrics{}, false
	}
	snapshot := preDecisionDependencySnapshot{
		Paths: make([]preDecisionPathIdentity, 0, len(paths)), Evidence: evidence,
		ScriptEnvironment: plan.ScriptEnvironmentIdentity,
	}
	var totalBytes int64
	var validUntil time.Time
	metrics := preDecisionSampleMetrics{DependencyPaths: len(paths)}
	now := time.Now()
	for _, dependency := range paths {
		identity, bytesRead, contentHashPasses, dependencyValidUntil, ok := observePreDecisionPath(root, dependency, now)
		if !ok || totalBytes > maxPreDecisionDependencyBytes-bytesRead {
			return preDecisionDependencySnapshot{}, time.Time{}, preDecisionSampleMetrics{}, false
		}
		totalBytes += bytesRead
		metrics.ContentHashPasses += contentHashPasses
		metrics.ContentHashBytes += bytesRead
		snapshot.Paths = append(snapshot.Paths, identity)
		if !dependencyValidUntil.IsZero() && (validUntil.IsZero() || dependencyValidUntil.Before(validUntil)) {
			validUntil = dependencyValidUntil
		}
	}
	if snapshot.identity() == "" {
		return preDecisionDependencySnapshot{}, time.Time{}, preDecisionSampleMetrics{}, false
	}
	return snapshot, validUntil, metrics, true
}

type preDecisionDependencyRequest struct {
	path           string
	freshnessHours []int
	contentBound   bool
}

func dedupePreDecisionPaths(paths []preDecisionDependencyRequest) []preDecisionDependencyRequest {
	indexes := make(map[string]int, len(paths))
	result := make([]preDecisionDependencyRequest, 0, len(paths))
	for _, dependency := range paths {
		if dependency.path == "" {
			continue
		}
		index, exists := indexes[dependency.path]
		if exists {
			result[index].contentBound = result[index].contentBound || dependency.contentBound
			result[index].freshnessHours = mergePreDecisionFreshness(result[index].freshnessHours, dependency.freshnessHours)
			continue
		}
		indexes[dependency.path] = len(result)
		dependency.freshnessHours = mergePreDecisionFreshness(nil, dependency.freshnessHours)
		result = append(result, dependency)
	}
	return result
}

func mergePreDecisionFreshness(current, added []int) []int {
	seen := make(map[int]struct{}, len(current)+len(added))
	for _, value := range current {
		if value > 0 {
			seen[value] = struct{}{}
		}
	}
	for _, value := range added {
		if value > 0 {
			seen[value] = struct{}{}
		}
	}
	merged := make([]int, 0, len(seen))
	for value := range seen {
		merged = append(merged, value)
	}
	sort.Ints(merged)
	return merged
}

func observePreDecisionPath(
	root string,
	dependency preDecisionDependencyRequest,
	now time.Time,
) (preDecisionPathIdentity, int64, int, time.Time, bool) {
	raw := dependency.path
	candidate := preDecisionPathCandidate(root, raw)
	resolved, err := pathidentity.ResolveProspective(candidate)
	if err != nil {
		return preDecisionPathIdentity{}, 0, 0, time.Time{}, false
	}
	ancestors, missing, ok := capturePreDecisionAncestors(root, candidate)
	if !ok {
		return preDecisionPathIdentity{}, 0, 0, time.Time{}, false
	}
	info, err := os.Lstat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		identity, bytesRead, observed := observePreDecisionMissingPath(root, raw, candidate, resolved, ancestors, missing)
		return identity, bytesRead, 0, time.Time{}, observed
	}
	if err != nil {
		return preDecisionPathIdentity{}, 0, 0, time.Time{}, false
	}
	generation, reliable := platformFileGeneration(candidate, info)
	if !reliable {
		return preDecisionPathIdentity{}, 0, 0, time.Time{}, false
	}
	observation, bytesRead, ok := observePreDecisionExistingPath(candidate, resolved, info, generation, ancestors, missing)
	if !ok || dependency.contentBound && observation.Exists && observation.ResolvedMode&uint32(os.ModeType) != 0 {
		return preDecisionPathIdentity{}, 0, 0, time.Time{}, false
	}
	validUntil := applyPreDecisionFreshness(&observation, dependency.freshnessHours, now)
	body, err := json.Marshal(observation)
	if err != nil {
		return preDecisionPathIdentity{}, 0, 0, time.Time{}, false
	}
	if !preDecisionPathStable(root, candidate, resolved, &observation, ancestors, missing) {
		return preDecisionPathIdentity{}, 0, 0, time.Time{}, false
	}
	contentHashPasses := 0
	if observation.Content != "" {
		contentHashPasses = 1
	}
	return preDecisionPathIdentity{Raw: raw, Resolved: resolved, Observation: hashBytes(body)}, bytesRead, contentHashPasses, validUntil, true
}

func applyPreDecisionFreshness(observation *preDecisionPathObservation, hours []int, now time.Time) time.Time {
	if observation == nil {
		return time.Time{}
	}
	if !observation.Exists || observation.ResolvedMode&uint32(os.ModeType) != 0 {
		return time.Time{}
	}
	modified := time.Unix(0, observation.ResolvedModTime)
	var earliest time.Time
	for _, value := range hours {
		if value <= 0 {
			continue
		}
		deadline := modified.Add(time.Duration(value) * time.Hour)
		expired := now.After(deadline)
		observation.Freshness = append(observation.Freshness, preDecisionFreshnessObservation{Hours: value, Expired: expired})
		if !expired && (earliest.IsZero() || deadline.Before(earliest)) {
			earliest = deadline
		}
	}
	return earliest
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
	return resamplePreDecisionInputsWithEvaluatorAndStopCache(root, payload, baseline, evaluator, nil)
}

func resamplePreDecisionInputsWithEvaluatorAndStopCache(
	root string,
	payload *HookPayload,
	baseline preDecisionInputs,
	evaluator *runtime.Evaluator,
	stopCache *StopDecisionCache,
) (preDecisionInputs, bool) {
	if payload == nil || baseline.identity.payload == "" {
		return preDecisionInputs{}, false
	}
	inputs := preDecisionInputs{
		identity: preDecisionIdentity{payload: baseline.identity.payload},
	}
	if !capturePreDecisionObservedIdentityWithEvaluatorAndStopCache(root, payload, &inputs, evaluator, stopCache) {
		return preDecisionInputs{}, false
	}
	inputs.key = inputs.identity.key()
	return inputs, true
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
	if !ok || !expected.identity.equal(current.identity) || !current.cacheableAt(time.Now()) {
		return Result{}, false
	}
	return Result{ExitCode: cached.ExitCode, Stderr: cached.Stderr, decisionClass: cached.DecisionClass}, true
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
		!cached.DecisionClass.cacheable() || cached.DecisionClass == preDecisionResultPass && cached.ExitCode != 0 ||
		cached.DecisionClass == preDecisionResultBlock && cached.ExitCode != 2 || len(cached.Stderr) > maxPreDecisionDiagnostic {
		return preDecisionCache{}, false
	}
	return cached, true
}

func writePreDecisionCacheForPayload(root string, payload *HookPayload, key string, decision Result) error {
	if !decision.decisionClass.cacheable() || decision.decisionClass == preDecisionResultPass && decision.ExitCode != 0 ||
		decision.decisionClass == preDecisionResultBlock && decision.ExitCode != 2 || decision.Stdout != "" ||
		decision.Err != nil || len(decision.Stderr) > maxPreDecisionDiagnostic {
		return nil
	}
	path := preDecisionCachePathForPayload(root, payload)
	if path == "" {
		return nil
	}
	body, err := json.MarshalIndent(preDecisionCache{
		FormatVersion: preDecisionCacheVersion,
		Key:           key,
		DecisionClass: decision.decisionClass,
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
