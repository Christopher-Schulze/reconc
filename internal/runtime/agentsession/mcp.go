package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

// RunMCPBefore handles a host-native MCP pre-execution event.
func RunMCPBefore(repoRoot string, payloadBytes []byte) Result {
	return runMCPBefore(repoRoot, payloadBytes, true)
}

// RunMCPAfter handles a host-native MCP post-execution event.
func RunMCPAfter(repoRoot string, payloadBytes []byte) Result {
	return runMCPAfter(repoRoot, payloadBytes, true)
}

// RunPreToolUseMCPAware classifies configured generic OpenCode/Kilo/OMP/Pi tool
// identities as MCP and leaves every other generic tool on the normal path.
func RunPreToolUseMCPAware(repoRoot string, payloadBytes []byte) Result {
	return runMCPBefore(repoRoot, payloadBytes, false)
}

// RunPostToolUseMCPAware applies the same exact identity decision after a
// generic OpenCode/Kilo/OMP/Pi tool call.
func RunPostToolUseMCPAware(repoRoot string, payloadBytes []byte) Result {
	return runMCPAfter(repoRoot, payloadBytes, false)
}

func runMCPBefore(repoRoot string, payloadBytes []byte, hostIdentified bool) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): " + err.Error()}
	}
	if payload.MCP == nil {
		if hostIdentified {
			return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): host event has no MCP identity"}
		}
		return RunPreToolUse(repoRoot, payloadBytes)
	}
	contract, err := runtime.LoadMCPPolicy(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): " + err.Error()}
	}
	classification, classified := classifyMCP(contract, payload)
	if !classified && !hostIdentified {
		return RunPreToolUse(repoRoot, payloadBytes)
	}
	if !classified {
		return handleUnclassifiedMCPBefore(repoRoot, contract, payload)
	}
	values, valid := extractMCPValues(repoRoot, classification, payload.ToolInput, payload.MCP.InputValid)
	if !valid {
		return handleUnclassifiedMCPBefore(repoRoot, contract, payload)
	}
	result := enforceMCPBefore(repoRoot, payload, classification, values)
	outcome := "allowed"
	if result.ExitCode != 0 {
		outcome = "denied"
	}
	if err := recordMCPAudit(repoRoot, payload.MCP, classification.Effect, outcome, true, payload.MCP.BlockingPreHook); err != nil {
		result.Stderr = appendMCPWarning(result.Stderr, err)
	}
	return result
}

func runMCPAfter(repoRoot string, payloadBytes []byte, hostIdentified bool) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: "reconc hook (mcp post, warn): " + err.Error()}
	}
	if payload.MCP == nil {
		if hostIdentified {
			return Result{ExitCode: 0, Stderr: "reconc hook (mcp post, warn): host event has no MCP identity"}
		}
		return RunPostToolUseCompleteStrict(repoRoot, payloadBytes)
	}
	contract, err := runtime.LoadMCPPolicy(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: "reconc hook (mcp post, warn): " + err.Error()}
	}
	classification, classified := classifyMCP(contract, payload)
	if !classified && !hostIdentified {
		return RunPostToolUseCompleteStrict(repoRoot, payloadBytes)
	}
	if !classified {
		return observeUnclassifiedMCP(repoRoot, contract, payload, "unclassified")
	}
	values, valid := extractMCPValues(repoRoot, classification, payload.ToolInput, payload.MCP.InputValid)
	if !valid {
		return observeUnclassifiedMCP(repoRoot, contract, payload, "unclassified")
	}
	outcome := payload.MCP.Outcome
	if outcome != "success" && outcome != "failure" {
		outcome = "failure"
	}
	result := recordClassifiedMCPAfter(repoRoot, payload, classification, values, outcome)
	if err := recordMCPAudit(repoRoot, payload.MCP, classification.Effect, outcome, true, payload.MCP.BlockingPreHook); err != nil {
		result.Stderr = appendMCPWarning(result.Stderr, err)
	}
	return result
}

func classifyMCP(contract *policy.MCPPolicy, payload *HookPayload) (policy.MCPToolPolicy, bool) {
	if contract == nil || payload == nil || payload.MCP == nil {
		return policy.MCPToolPolicy{}, false
	}
	key := string(payload.MCP.Platform) + "\x00" + payload.MCP.ServerFingerprint + "\x00" + payload.MCP.Tool
	return exactMCPClassification(contract.Tools, key)
}

func exactMCPClassification(tools []policy.MCPToolPolicy, key string) (policy.MCPToolPolicy, bool) {
	index := sort.Search(len(tools), func(index int) bool {
		return tools[index].StableKey() >= key
	})
	if index >= len(tools) || tools[index].StableKey() != key {
		return policy.MCPToolPolicy{}, false
	}
	return tools[index], true
}

func handleUnclassifiedMCPBefore(repoRoot string, contract *policy.MCPPolicy, payload *HookPayload) Result {
	mode := policy.MCPUnclassifiedHost
	if contract != nil {
		mode = contract.Unclassified
	}
	strictAvailable := payload.MCP.BlockingPreHook
	outcome := "unclassified"
	result := Result{ExitCode: 0, Stderr: "reconc MCP: unclassified call remains host-controlled and produces no repository evidence"}
	if mode == policy.MCPUnclassifiedDeny {
		if strictAvailable {
			outcome = "denied"
			result = Result{ExitCode: 2, Stderr: "reconc MCP: strict policy denied an unclassified MCP call before execution"}
		} else {
			outcome = "strict-unavailable"
			result.Stderr = "reconc MCP: strict unclassified deny is unavailable on this host surface; no enforcement claim or repository evidence was recorded"
		}
	}
	if err := recordMCPAudit(repoRoot, payload.MCP, "", outcome, false, strictAvailable); err != nil {
		result.Stderr = appendMCPWarning(result.Stderr, err)
	}
	return result
}

func observeUnclassifiedMCP(repoRoot string, contract *policy.MCPPolicy, payload *HookPayload, outcome string) Result {
	strictAvailable := payload.MCP.BlockingPreHook
	if contract != nil && contract.Unclassified == policy.MCPUnclassifiedDeny && !strictAvailable {
		outcome = "strict-unavailable"
	}
	result := Result{ExitCode: 0, Stderr: "reconc MCP: unclassified result was not accepted as repository evidence"}
	if err := recordMCPAudit(repoRoot, payload.MCP, "", outcome, false, strictAvailable); err != nil {
		result.Stderr = appendMCPWarning(result.Stderr, err)
	}
	return result
}

type mcpExtractedValues struct {
	Paths    []string
	Commands []string
}

func extractMCPValues(repoRoot string, classification policy.MCPToolPolicy, input map[string]interface{}, inputValid bool) (mcpExtractedValues, bool) {
	if !inputValid || input == nil {
		return mcpExtractedValues{}, false
	}
	switch classification.Effect {
	case policy.MCPEffectRepositoryRead, policy.MCPEffectRepositoryWrite:
		rawPaths, valid := selectMCPStrings(input, classification.PathFields)
		if !valid {
			return mcpExtractedValues{}, false
		}
		paths, valid := normalizeMCPRepoPaths(repoRoot, rawPaths)
		return mcpExtractedValues{Paths: paths}, valid
	case policy.MCPEffectCommand:
		commands, valid := selectMCPStrings(input, []string{classification.CommandField})
		return mcpExtractedValues{Commands: commands}, valid
	case policy.MCPEffectExternal:
		return mcpExtractedValues{}, true
	default:
		return mcpExtractedValues{}, false
	}
}

func selectMCPStrings(input map[string]interface{}, pointers []string) ([]string, bool) {
	values := []string{}
	for _, pointer := range pointers {
		selected, ok := policy.ResolveJSONPointer(input, pointer)
		if !ok {
			return nil, false
		}
		switch value := selected.(type) {
		case string:
			if strings.TrimSpace(value) == "" {
				return nil, false
			}
			values = append(values, value)
		case []interface{}:
			if len(value) == 0 {
				return nil, false
			}
			for _, rawItem := range value {
				item, ok := rawItem.(string)
				if !ok || strings.TrimSpace(item) == "" {
					return nil, false
				}
				values = append(values, item)
			}
		default:
			return nil, false
		}
	}
	return sortedUnique(values), len(values) > 0
}

func normalizeMCPRepoPaths(repoRoot string, rawPaths []string) ([]string, bool) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, false
	}
	out := make([]string, 0, len(rawPaths))
	for _, rawPath := range rawPaths {
		candidate := filepath.FromSlash(strings.TrimSpace(rawPath))
		if candidate == "" {
			return nil, false
		}
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		resolved, err := pathidentity.ResolveProspective(candidate)
		if err != nil {
			return nil, false
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, false
		}
		out = append(out, filepath.ToSlash(relative))
	}
	out = sortedUnique(out)
	return out, len(out) > 0
}

func enforceMCPBefore(repoRoot string, payload *HookPayload, classification policy.MCPToolPolicy, values mcpExtractedValues) Result {
	switch classification.Effect {
	case policy.MCPEffectRepositoryRead, policy.MCPEffectExternal:
		return Result{ExitCode: 0}
	case policy.MCPEffectRepositoryWrite:
		return runMCPWritePre(repoRoot, payload, values.Paths)
	case policy.MCPEffectCommand:
		for _, command := range values.Commands {
			result := runMCPCommandPre(repoRoot, payload, command)
			if result.ExitCode != 0 {
				return result
			}
		}
		return Result{ExitCode: 0}
	default:
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): invalid effect"}
	}
}

func runMCPWritePre(repoRoot string, payload *HookPayload, paths []string) Result {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): " + err.Error()}
	}
	state, err := EnsureSessionState(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): " + err.Error()}
	}
	if state.EvidenceOverflow {
		return Result{ExitCode: 2, Stderr: evidenceOverflowMessage(state)}
	}
	state, err = loadCompleteSessionEvidence(root, state)
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): load evidence chain: " + err.Error()}
	}
	trialWrites := append(append([]string(nil), state.WritePaths...), paths...)
	report, err := runPreWritePolicyCheck(root, state.ReadPaths, trialWrites, state.WriteEpochs, state.Commands, state.CommandResults, state.Claims)
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): write check failed: " + err.Error()}
	}
	violations := preWriteBlockingViolations(report)
	if len(violations) == 0 {
		return Result{ExitCode: 0}
	}
	return Result{ExitCode: 2, Stderr: firstLinesForViolations(violations, "reconc blocked this MCP repository write before execution.")}
}

func runMCPCommandPre(repoRoot string, payload *HookPayload, command string) Result {
	if reason := forbiddenShellCommandReason(command); reason != "" {
		return Result{ExitCode: 2, Stderr: reason}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): " + err.Error()}
	}
	state, err := EnsureSessionState(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): " + err.Error()}
	}
	if state.EvidenceOverflow {
		return Result{ExitCode: 2, Stderr: evidenceOverflowMessage(state)}
	}
	state, err = loadCompleteSessionEvidence(root, state)
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): load evidence chain: " + err.Error()}
	}
	report, err := runPreCommandPolicyCheck(root, state, command)
	if err != nil {
		return Result{ExitCode: 2, Stderr: "reconc hook (mcp pre): command check failed: " + err.Error()}
	}
	violations := blockingViolationsForKinds(report, preCommandBlockKinds)
	if len(violations) == 0 {
		return Result{ExitCode: 0}
	}
	return Result{ExitCode: 2, Stderr: firstLinesForViolations(violations, "reconc blocked this MCP command before execution.")}
}

func recordClassifiedMCPAfter(repoRoot string, payload *HookPayload, classification policy.MCPToolPolicy, values mcpExtractedValues, outcome string) Result {
	if classification.Effect == policy.MCPEffectExternal || outcome != "success" && classification.Effect != policy.MCPEffectCommand {
		return Result{ExitCode: 0}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: "reconc hook (mcp post, warn): " + err.Error()}
	}
	updated, err := MutateSessionState(root, payload.SessionID, func(state SessionState) SessionState {
		if state.EvidenceOverflow {
			return state
		}
		signature := mcpMaterialSignature(payload.MCP, classification.Effect, values, outcome)
		if signature != "" && signature == state.LastMaterialSignature {
			return state
		}
		switch classification.Effect {
		case policy.MCPEffectRepositoryRead:
			for _, path := range values.Paths {
				state = AppendReadPath(state, path)
			}
		case policy.MCPEffectRepositoryWrite:
			state = RecordWriteEvent(state, values.Paths)
			state = RecordMaterialEvent(state, signature)
		case policy.MCPEffectCommand:
			for _, command := range values.Commands {
				if outcome == "success" {
					state = AppendCommand(state, command)
				}
				state = AppendCommandResult(state, CommandResult{
					Command:       command,
					Outcome:       outcome,
					EvidenceEpoch: state.EvidenceEpoch,
					ToolUseID:     payload.ToolUseID,
					ExitCode:      payload.ExitCode(),
					Error:         payload.Error,
					IsInterrupt:   payload.IsInterrupt,
				})
			}
			state = RecordMaterialEvent(state, signature)
		}
		return state
	})
	if err != nil {
		return Result{ExitCode: 0, Stderr: "reconc hook (mcp post, warn): " + err.Error()}
	}
	if updated.EvidenceOverflow {
		return Result{ExitCode: 0, Stderr: evidenceOverflowMessage(updated)}
	}
	return Result{ExitCode: 0}
}

func mcpMaterialSignature(envelope *MCPPayload, effect policy.MCPEffect, values mcpExtractedValues, outcome string) string {
	if envelope == nil {
		return ""
	}
	body, err := json.Marshal(struct {
		Selector string           `json:"selector"`
		Effect   policy.MCPEffect `json:"effect"`
		Paths    []string         `json:"paths,omitempty"`
		Commands []string         `json:"commands,omitempty"`
		Outcome  string           `json:"outcome"`
	}{
		Selector: mcpSelectorHash(envelope),
		Effect:   effect,
		Paths:    values.Paths,
		Commands: values.Commands,
		Outcome:  outcome,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func appendMCPWarning(current string, err error) string {
	warning := "reconc MCP audit (warn): " + err.Error()
	if strings.TrimSpace(current) == "" {
		return warning
	}
	return current + "; " + warning
}
