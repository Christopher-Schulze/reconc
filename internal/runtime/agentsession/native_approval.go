package agentsession

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/actionstate"
	productruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/shellcommand"
)

const (
	nativeApprovalAuthoritiesEnv = "RECONC_APPROVAL_AUTHORITIES"
	nativeApprovalPolicyEnv      = "RECONC_APPROVAL_POLICY"
	nativeApprovalPrincipalEnv   = "RECONC_APPROVAL_PRINCIPAL"
)

// nativeApprovalEnvelope is intentionally transport-neutral. Adapters may
// carry it as an object named reconc_approval; the operator-owned registry and
// signing key never cross the host payload boundary.
type nativeApprovalEnvelope struct {
	request []byte
	receipt []byte
}

func boundApprovalRuleIDs(evaluator *productruntime.Evaluator, root string, writePaths []string) ([]string, error) {
	compiled, _, err := evaluator.CurrentCompiledPolicyEvaluator(root)
	if err != nil {
		return nil, err
	}
	return compiled.BoundApprovalRuleIDs(root, writePaths)
}

func commandWritePaths(payload *HookPayload) ([]string, bool, error) {
	if payload == nil || payload.Raw == nil {
		return nil, false, nil
	}
	raw, present := payload.Raw["reconc_write_paths"]
	if !present {
		return nil, false, nil
	}
	values, ok := raw.([]interface{})
	if !ok || len(values) == 0 {
		return nil, true, fmt.Errorf("reconc_write_paths must be a non-empty array of exact path strings")
	}
	paths := make([]string, 0, len(values))
	for _, value := range values {
		path, ok := value.(string)
		if !ok || path == "" || strings.TrimSpace(path) != path {
			return nil, true, fmt.Errorf("reconc_write_paths must contain non-empty exact path strings")
		}
		paths = append(paths, path)
	}
	return dedupePaths(paths), true, nil
}

func commandMayWriteRepository(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if hasRedirect, complete := shellcommand.HasRedirect(command); !complete || hasRedirect {
		return true
	}
	invocations, reason := shellcommand.InvocationsWithReason(command, 16)
	if reason != shellcommand.IncompleteNone {
		return true
	}
	for _, invocation := range invocations {
		if !knownReadOnlyInvocation(invocation.Words) {
			return true
		}
	}
	return false
}

func knownReadOnlyInvocation(words []string) bool {
	if len(words) == 0 {
		return false
	}
	name := filepath.Base(words[0])
	switch name {
	case "cat", "echo", "false", "grep", "head", "ls", "pwd", "tail", "test", "true", "which", "command":
		return true
	case "rg":
		// ripgrep's --pre and --pre-glob options execute a caller-selected
		// preprocessor, so the otherwise read-only binary is not safe here.
		return !hasCommandOption(words[1:], "--pre", "--pre-glob")
	case "go":
		// go test/check/doc can execute repository-controlled code (tests and
		// build hooks). Only metadata/version queries are unconditionally
		// read-only for this gate.
		return len(words) >= 2 && (words[1] == "env" || words[1] == "version") &&
			!hasCommandOption(words[2:], "-w", "-u", "--")
	case "cargo":
		return len(words) >= 2 && (words[1] == "metadata" || words[1] == "tree" || words[1] == "version")
	case "git":
		if len(words) < 2 {
			return false
		}
		switch words[1] {
		case "check-ignore", "describe", "log", "ls-files", "ls-tree", "rev-parse", "show", "status":
			return !hasCommandOption(words[2:], "--output", "-o", "--ext-diff", "--textconv")
		case "diff":
			return !hasCommandOption(words[2:], "--output", "-o", "--ext-diff", "--textconv")
		case "branch":
			return len(words) == 2
		default:
			return false
		}
	default:
		return false
	}
}

func hasCommandOption(words []string, options ...string) bool {
	for _, word := range words {
		for _, option := range options {
			if word == option || strings.HasPrefix(word, option+"=") {
				return true
			}
		}
	}
	return false
}

func nativeApprovalEnvelopeFromPayload(payload *HookPayload) (nativeApprovalEnvelope, bool, error) {
	if payload == nil || payload.Raw == nil {
		return nativeApprovalEnvelope{}, false, nil
	}
	raw, present := payload.Raw["reconc_approval"]
	if !present {
		return nativeApprovalEnvelope{}, false, nil
	}
	mapping, ok := raw.(map[string]interface{})
	if !ok || mapping == nil {
		return nativeApprovalEnvelope{}, true, fmt.Errorf("reconc_approval must be an object")
	}
	for key := range mapping {
		if key != "request" && key != "receipt" {
			return nativeApprovalEnvelope{}, true, fmt.Errorf("reconc_approval contains unsupported field %q", key)
		}
	}
	request, ok := mapping["request"].(map[string]interface{})
	if !ok || request == nil {
		return nativeApprovalEnvelope{}, true, fmt.Errorf("reconc_approval.request must be an object")
	}
	receipt, ok := mapping["receipt"].(map[string]interface{})
	if !ok || receipt == nil {
		return nativeApprovalEnvelope{}, true, fmt.Errorf("reconc_approval.receipt must be an object")
	}
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nativeApprovalEnvelope{}, true, fmt.Errorf("encode reconc_approval.request: %w", err)
	}
	receiptBody, err := json.Marshal(receipt)
	if err != nil {
		return nativeApprovalEnvelope{}, true, fmt.Errorf("encode reconc_approval.receipt: %w", err)
	}
	return nativeApprovalEnvelope{request: requestBody, receipt: receiptBody}, true, nil
}

func rejectStaleNativeApprovalEnvelope(payload *HookPayload, currentRuleIDs []string) error {
	envelope, present, err := nativeApprovalEnvelopeFromPayload(payload)
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: invalid approval envelope: %w", err)
	}
	if !present {
		return nil
	}
	candidate, err := actionapproval.DecodeRequest(envelope.request)
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: invalid approval request: %w", err)
	}
	if len(candidate.RuleIDs) > 0 && len(currentRuleIDs) == 0 {
		return fmt.Errorf("bound authority change is blocked: supplied approval no longer matches the current authority policy")
	}
	return nil
}

func verifyAndConsumeNativeApproval(
	root string,
	payload *HookPayload,
	state SessionState,
	pendingWrites []string,
	ruleIDs []string,
	evaluator *productruntime.Evaluator,
) error {
	if strings.TrimSpace(payload.ToolUseID) == "" {
		return fmt.Errorf("bound authority change is blocked: host did not provide a stable pre-action tool identity")
	}
	principal := strings.TrimSpace(os.Getenv(nativeApprovalPrincipalEnv))
	if !action.SafeLabel(principal) {
		return fmt.Errorf("bound authority change is blocked: %s must provide a safe operator principal", nativeApprovalPrincipalEnv)
	}
	registryPath := strings.TrimSpace(os.Getenv(nativeApprovalAuthoritiesEnv))
	policyID := strings.TrimSpace(os.Getenv(nativeApprovalPolicyEnv))
	if registryPath == "" || !action.SafeLabel(policyID) {
		return fmt.Errorf("bound authority change is blocked: the operator approval channel is unavailable; set %s and %s outside the repository", nativeApprovalAuthoritiesEnv, nativeApprovalPolicyEnv)
	}
	envelope, present, err := nativeApprovalEnvelopeFromPayload(payload)
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: %w", err)
	}
	if !present {
		return fmt.Errorf("bound authority change is blocked: host did not provide a signed pre-action receipt in reconc_approval")
	}
	candidate, err := actionapproval.DecodeRequest(envelope.request)
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: invalid approval request: %w", err)
	}
	receipt, err := actionapproval.DecodeReceipt(envelope.receipt)
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: invalid approval receipt: %w", err)
	}
	if equal, equalErr := actionapproval.RequestsEqual(candidate, receipt.Request); equalErr != nil || !equal {
		return fmt.Errorf("bound authority change is blocked: receipt request does not match reconc_approval.request")
	}
	compiled, _, err := evaluator.CurrentCompiledPolicyEvaluator(root)
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: refresh current policy before receipt verification: %w", err)
	}
	normalizedWrites, err := productruntime.NormalizeReplayInputs(root, productruntime.ExecutionInputs{WritePaths: pendingWrites})
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: normalize proposed write paths: %w", err)
	}
	currentRuleIDs, err := compiled.BoundApprovalRuleIDs(root, normalizedWrites.WritePaths)
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: match current authority policy: %w", err)
	}
	if !equalStringSlices(currentRuleIDs, ruleIDs) {
		return fmt.Errorf("bound authority change is blocked: authority policy changed while preparing the approval")
	}
	sourceDigest, lockDigest, err := compiled.PolicyDigests()
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: read current policy identities: %w", err)
	}
	expected, err := nativeExpectedApprovalRequest(candidate, root, payload, state, normalizedWrites.WritePaths, ruleIDs, sourceDigest, lockDigest, principal, policyID)
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: build current approval binding: %w", err)
	}
	if equal, equalErr := actionapproval.RequestsEqual(expected, candidate); equalErr != nil || !equal {
		return fmt.Errorf("bound authority change is blocked: receipt is bound to a different repository, policy, tool, path set, or proposed content")
	}
	registry, err := actionstate.LoadApprovalAuthorityRegistry(registryPath, root)
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: load operator approval registry: %w", err)
	}
	if !registry.HasPolicy(policyID) {
		return fmt.Errorf("bound authority change is blocked: approval registry does not expose policy %q", policyID)
	}
	verified, err := registry.Verify(expected, envelope.receipt, nowUTC())
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: verify operator receipt: %w", err)
	}
	resampledWrites, err := productruntime.NormalizeReplayInputs(root, productruntime.ExecutionInputs{WritePaths: pendingWrites})
	if err != nil {
		return fmt.Errorf("bound authority change is blocked: recheck proposed path identity after receipt verification: %w", err)
	}
	if !equalStringSlices(resampledWrites.WritePaths, normalizedWrites.WritePaths) {
		return fmt.Errorf("bound authority change is blocked: proposed path identity changed during receipt verification")
	}
	// A second immutable-plan read is deliberate. It closes the race between
	// request binding and the final allow decision if the lock or source changes
	// while the operator receipt is being checked.
	refreshed, _, refreshErr := evaluator.CurrentCompiledPolicyEvaluator(root)
	if refreshErr != nil {
		return fmt.Errorf("bound authority change is blocked: recheck current policy after receipt verification: %w", refreshErr)
	}
	refreshedSource, refreshedLock, err := refreshed.PolicyDigests()
	if err != nil || refreshedSource != sourceDigest || refreshedLock != lockDigest {
		return fmt.Errorf("bound authority change is blocked: policy generation changed during receipt verification")
	}
	refreshedRuleIDs, err := refreshed.BoundApprovalRuleIDs(root, resampledWrites.WritePaths)
	if err != nil || !equalStringSlices(refreshedRuleIDs, ruleIDs) {
		return fmt.Errorf("bound authority change is blocked: authority policy changed during receipt verification")
	}
	stateVersion := nativeStateVersionIdentity(state)
	if err := consumeNativeApprovalIdentity(root, payload.SessionID, stateVersion, verified.Identity); err != nil {
		return fmt.Errorf("bound authority change is blocked: consume approval receipt: %w", err)
	}
	return nil
}

func nowUTC() time.Time { return time.Now().UTC() }

func nativeExpectedApprovalRequest(
	candidate actionapproval.Request,
	root string,
	payload *HookPayload,
	state SessionState,
	writePaths, ruleIDs []string,
	sourceDigest, lockDigest, principal, policyID string,
) (actionapproval.Request, error) {
	if payload == nil {
		return actionapproval.Request{}, fmt.Errorf("native approval payload is unavailable")
	}
	if sourceDigest == "" || lockDigest == "" {
		return actionapproval.Request{}, fmt.Errorf("current policy identities are unavailable")
	}
	sortedPaths := append([]string(nil), writePaths...)
	sort.Strings(sortedPaths)
	effect, err := nativeEffectIdentity(payload, sortedPaths)
	if err != nil {
		return actionapproval.Request{}, err
	}
	toolName := nativeApprovalToolName(payload)
	toolID := nativeToolID(toolName)
	runtimeValue := strings.TrimSpace(state.Runtime)
	if runtimeValue == "" {
		runtimeValue = runtimeFromPayload(payload)
	}
	if strings.TrimSpace(runtimeValue) == "" && payload.MCP != nil {
		runtimeValue = string(payload.MCP.Platform)
	}
	runtimeName := nativeToolID(runtimeValue)
	if runtimeName == "" {
		runtimeName = "native-hook"
	}
	selectedBody, err := json.Marshal(payload.ToolInput)
	if err != nil || len(selectedBody) == 0 {
		return actionapproval.Request{}, fmt.Errorf("encode proposed tool input")
	}
	expected := candidate
	expected.CallID = nativeCallID(payload.ToolUseID)
	expected.RequiredApprovalIdentity = nativeSHA256Identity("required", ruleIDs, sortedPaths, effect)
	expected.PlanIdentity = nativeSHA256Identity("plan", lockDigest)
	expected.SourceIdentity = sourceDigest
	expected.RepositoryIdentity = nativeKeyedIdentity("repository", root)
	expected.StateVersion = nativeStateVersionIdentity(state)
	expected.PolicyDigest = sourceDigest
	expected.LockDigest = lockDigest
	expected.ExecutableDigest = nativeSHA256Identity("executable", runtimeName)
	expected.ServerLabel = runtimeName
	expected.ServerFingerprint = nativeServerFingerprint(payload, runtimeName)
	expected.ToolID = toolID
	expected.Tool = toolName
	expected.ToolContractDigest = nativeToolContractIdentity(payload, toolName)
	expected.Phase = action.PhasePreCall
	expected.Principal = principal
	expected.ContextIdentity = nativeKeyedIdentity("context", payload.SessionID)
	expected.CredentialLabels = []string{}
	expected.TaintIdentity = "none"
	expected.RepositoryEffectIdentity = effect
	expected.SelectedArguments = []actionapproval.SelectedArgument{{
		Pointer: "/tool_input", State: action.PointerPresent, Kind: action.ValueObject,
		ByteLength: uint64(len(selectedBody)), Identity: nativeKeyedIdentity("argument", selectedBody),
	}}
	expected.BudgetReservationID = "absent"
	expected.ReasonCode = action.ReasonApprovalRequired
	expected.RuleIDs = append([]string(nil), ruleIDs...)
	expected.AuthorityPolicyID = policyID
	binding := struct {
		CallID, RepositoryIdentity, StateVersion, PolicyDigest, LockDigest string
		ToolID, Tool, ToolContractDigest, Principal, ContextIdentity       string
		Effect, RequiredApprovalIdentity, PlanIdentity                     string
		Paths, RuleIDs                                                     []string
	}{
		CallID: expected.CallID, RepositoryIdentity: expected.RepositoryIdentity,
		StateVersion: expected.StateVersion, PolicyDigest: expected.PolicyDigest,
		LockDigest: expected.LockDigest, ToolID: expected.ToolID, Tool: expected.Tool,
		ToolContractDigest: expected.ToolContractDigest, Principal: expected.Principal,
		ContextIdentity: expected.ContextIdentity, Effect: effect,
		RequiredApprovalIdentity: expected.RequiredApprovalIdentity,
		PlanIdentity:             expected.PlanIdentity, Paths: sortedPaths, RuleIDs: append([]string(nil), ruleIDs...),
	}
	bindingBody, err := json.Marshal(binding)
	if err != nil {
		return actionapproval.Request{}, err
	}
	expected.RequestIdentity = nativeKeyedIdentity("request", bindingBody)
	if err := expected.Validate(); err != nil {
		return actionapproval.Request{}, err
	}
	return expected, nil
}

func nativeApprovalToolName(payload *HookPayload) string {
	if payload == nil {
		return ""
	}
	if payload.MCP != nil {
		return strings.TrimSpace(payload.MCP.Tool)
	}
	return strings.TrimSpace(payload.ToolName)
}

func nativeServerFingerprint(payload *HookPayload, runtimeName string) string {
	if payload != nil && payload.MCP != nil && payload.MCP.ServerFingerprint != "" {
		return nativeKeyedIdentity("server", payload.MCP.ServerFingerprint)
	}
	return nativeKeyedIdentity("server", runtimeName)
}

func nativeToolContractIdentity(payload *HookPayload, toolName string) string {
	var platform, serverFingerprint string
	if payload != nil && payload.MCP != nil {
		platform = string(payload.MCP.Platform)
		serverFingerprint = payload.MCP.ServerFingerprint
	}
	return nativeSHA256Identity("tool", struct {
		Platform          string `json:"platform,omitempty"`
		ServerFingerprint string `json:"server_fingerprint,omitempty"`
		Tool              string `json:"tool"`
	}{Platform: platform, ServerFingerprint: serverFingerprint, Tool: toolName})
}

func nativeEffectIdentity(payload *HookPayload, paths []string) (string, error) {
	if payload == nil {
		return "", fmt.Errorf("native approval payload is unavailable")
	}
	var platform, serverFingerprint string
	if payload.MCP != nil {
		platform = string(payload.MCP.Platform)
		serverFingerprint = payload.MCP.ServerFingerprint
	}
	body, err := json.Marshal(struct {
		ToolName          string                 `json:"tool_name"`
		Platform          string                 `json:"platform,omitempty"`
		ServerFingerprint string                 `json:"server_fingerprint,omitempty"`
		Paths             []string               `json:"paths"`
		Input             map[string]interface{} `json:"tool_input"`
	}{ToolName: nativeApprovalToolName(payload), Platform: platform, ServerFingerprint: serverFingerprint, Paths: paths, Input: payload.ToolInput})
	if err != nil {
		return "", fmt.Errorf("encode proposed repository effect: %w", err)
	}
	return nativeSHA256Identity("effect", body), nil
}

func nativeStateVersionIdentity(state SessionState) string {
	body, err := json.Marshal(normalizeSessionState(state))
	if err != nil {
		return nativeKeyedIdentity("state", []byte("invalid"))
	}
	return nativeKeyedIdentity("state", body)
}

func nativeSHA256Identity(label string, values ...interface{}) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("reconc/native-approval/" + label + "\x00"))
	for _, value := range values {
		body, _ := json.Marshal(value)
		_, _ = hash.Write(body)
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func nativeKeyedIdentity(label string, values ...interface{}) string {
	digest := strings.TrimPrefix(nativeSHA256Identity(label, values...), "sha256:")
	return "hmac-sha256:v1:hook:" + digest
}

func nativeCallID(value string) string {
	sum := sha256.Sum256([]byte("reconc/native-call\x00" + value))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return "act_" + strings.ToLower(encoded[:26])
}

func nativeToolID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			builder.WriteRune(character)
		} else if builder.Len() > 0 {
			builder.WriteByte('-')
		}
		if builder.Len() >= 63 {
			break
		}
	}
	value = strings.Trim(builder.String(), "-")
	if value == "" {
		return "native-tool"
	}
	return value
}

func consumeNativeApprovalIdentity(root, sessionID, expectedStateVersion, identity string) error {
	var transitionErr error
	_, err := mutateSessionStateResolved(root, sessionID, func(state SessionState) SessionState {
		if nativeStateVersionIdentity(state) != expectedStateVersion {
			transitionErr = fmt.Errorf("session evidence changed while consuming receipt")
			return state
		}
		for _, consumed := range state.ConsumedApprovalIdentities {
			if consumed == identity {
				transitionErr = fmt.Errorf("approval receipt was already consumed")
				return state
			}
		}
		if len(state.ConsumedApprovalIdentities) >= maxConsumedApprovalIdentities {
			transitionErr = fmt.Errorf("approval replay ledger is full")
			return state
		}
		state.ConsumedApprovalIdentities = append(state.ConsumedApprovalIdentities, identity)
		return state
	})
	if err != nil {
		return err
	}
	return transitionErr
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
