package hooks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"reconc.dev/reconc/internal/boundedio"
)

// Codex runs a project hook only while the definition hash it computes for the
// hook equals the trusted_hash approved under /hooks. A regenerated command or
// matcher counts as modified and Codex skips that hook without a message, so a
// configured repository can lose enforcement after a reinstall. These helpers
// reproduce the Codex hash so hook status can name the hooks Codex will not run.

const maxCodexConfigBytes = 4 << 20

// codexHookEventLabels maps the hooks.json event names to the labels Codex
// uses in hook state keys and definition hashes.
var codexHookEventLabels = map[string]string{
	"PreToolUse":        "pre_tool_use",
	"PermissionRequest": "permission_request",
	"PostToolUse":       "post_tool_use",
	"PreCompact":        "pre_compact",
	"PostCompact":       "post_compact",
	"PreModelRequest":   "pre_model_request",
	"PostModelRequest":  "post_model_request",
	"SessionStart":      "session_start",
	"SessionEnd":        "session_end",
	"UserPromptSubmit":  "user_prompt_submit",
	"SubagentStart":     "subagent_start",
	"SubagentStop":      "subagent_stop",
	"Stop":              "stop",
	"Interrupt":         "interrupt",
}

// codexMatcherEvents keep their matcher in the definition hash; Codex drops
// the matcher of every other event.
var codexMatcherEvents = map[string]bool{
	"PreToolUse": true, "PermissionRequest": true, "PostToolUse": true,
	"SessionStart": true, "SessionEnd": true, "SubagentStart": true,
	"SubagentStop": true, "PreCompact": true, "PostCompact": true,
}

// codexContextEvents keep a non-default additionalContextLimit.
var codexContextEvents = map[string]bool{
	"PreToolUse": true, "PostToolUse": true, "SessionStart": true,
	"UserPromptSubmit": true, "SubagentStart": true,
}

const codexDefaultContextLimit = 2500

type codexHookGroup struct {
	Matcher *string                      `json:"matcher"`
	Hooks   []map[string]json.RawMessage `json:"hooks"`
}

// codexTrustFinding names one Reconc hook Codex will not run.
type codexTrustFinding struct {
	Hook   string
	Reason string
}

// codexTrustResult is the outcome of comparing the repository hooks with the
// user's Codex hook approvals. Verified is false when the Codex configuration
// or the project entry is missing, so nothing can be claimed either way.
type codexTrustResult struct {
	ConfigPath string
	Verified   bool
	Untrusted  bool
	Findings   []codexTrustFinding
}

// codexHookDefinitionHash reproduces the Codex definition hash of one command
// handler: SHA-256 over the key-sorted JSON of the event label, the matcher
// and the normalized handler. ok is false for non-command handlers.
func codexHookDefinitionHash(event string, matcher *string, handler map[string]json.RawMessage) (string, bool, error) {
	label, known := codexHookEventLabels[event]
	if !known {
		return "", false, nil
	}
	var kind string
	if raw, present := handler["type"]; !present || json.Unmarshal(raw, &kind) != nil || kind != "command" {
		return "", false, nil
	}
	normalized, err := normalizeCodexCommandHandler(event, handler)
	if err != nil {
		return "", false, err
	}
	identity := map[string]any{"event_name": label, "hooks": []any{normalized}}
	if matcher != nil && codexMatcherEvents[event] {
		identity["matcher"] = *matcher
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(identity); err != nil {
		return "", false, err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(encoded.Bytes(), []byte("\n")))
	return "sha256:" + hex.EncodeToString(sum[:]), true, nil
}

func normalizeCodexCommandHandler(event string, handler map[string]json.RawMessage) (map[string]any, error) {
	var command string
	if err := json.Unmarshal(handler["command"], &command); err != nil {
		return nil, fmt.Errorf("codex hook command: %w", err)
	}
	timeout, timeoutSet, err := optionalCodexUint(handler, "timeout")
	if err != nil {
		return nil, err
	}
	switch event {
	case "SessionEnd", "Interrupt":
		if !timeoutSet {
			timeout = 1
		}
		timeout = min(max(timeout, 1), 3)
	default:
		if !timeoutSet {
			timeout = 600
		}
		timeout = max(timeout, 1)
	}
	async := false
	if raw, present := handler["async"]; present {
		if err := json.Unmarshal(raw, &async); err != nil {
			return nil, fmt.Errorf("codex hook async: %w", err)
		}
	}
	normalized := map[string]any{"type": "command", "command": command, "timeout": timeout, "async": async}
	if raw, present := handler["statusMessage"]; present && string(raw) != "null" {
		var message string
		if err := json.Unmarshal(raw, &message); err != nil {
			return nil, fmt.Errorf("codex hook statusMessage: %w", err)
		}
		normalized["statusMessage"] = message
	}
	limit, limitSet, err := optionalCodexUint(handler, "additionalContextLimit")
	if err != nil {
		return nil, err
	}
	if limitSet && codexContextEvents[event] && limit != codexDefaultContextLimit {
		normalized["additionalContextLimit"] = limit
	}
	return normalized, nil
}

func optionalCodexUint(handler map[string]json.RawMessage, key string) (uint64, bool, error) {
	raw, present := handler[key]
	if !present || string(raw) == "null" {
		return 0, false, nil
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("codex hook %s: %w", key, err)
	}
	return value, true, nil
}

// codexHome is the directory Codex reads its user configuration from.
func codexHome() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".codex"), nil
}

// inspectCodexHookTrust compares every command handler of the repository's
// .codex/hooks.json with the approvals in the user's Codex configuration. It
// reports findings only on positive evidence: an untrusted project, a disabled
// hook, a stale approval, or a hook without approval in a trusted project.
func inspectCodexHookTrust(root string) (codexTrustResult, error) {
	home, err := codexHome()
	if err != nil {
		return codexTrustResult{}, err
	}
	result := codexTrustResult{ConfigPath: filepath.Join(home, "config.toml")}
	data, err := boundedio.ReadFile(result.ConfigPath, maxCodexConfigBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var document map[string]any
	if err := toml.Unmarshal(data, &document); err != nil {
		return result, fmt.Errorf("invalid TOML in %s: %w", result.ConfigPath, err)
	}
	projectRoot, trustLevel := codexProjectTrust(document, root)
	switch trustLevel {
	case "":
		return result, nil
	case "trusted":
	default:
		result.Verified, result.Untrusted = true, true
		return result, nil
	}
	hooksPath := filepath.Join(projectRoot, filepath.FromSlash(CodexHooksPath))
	hooksData, err := boundedio.ReadFile(filepath.Join(root, filepath.FromSlash(CodexHooksPath)), maxHostSettingsBytes)
	if err != nil {
		return result, err
	}
	var hooksDocument struct {
		Hooks map[string][]codexHookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(hooksData, &hooksDocument); err != nil {
		return result, fmt.Errorf("invalid JSON in %s: %w", CodexHooksPath, err)
	}
	state := codexHookState(document)
	events := make([]string, 0, len(hooksDocument.Hooks))
	for event := range hooksDocument.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	for _, event := range events {
		for groupIndex, group := range hooksDocument.Hooks[event] {
			for handlerIndex, handler := range group.Hooks {
				hash, ok, err := codexHookDefinitionHash(event, group.Matcher, handler)
				if err != nil {
					return result, err
				}
				if !ok {
					continue
				}
				name := fmt.Sprintf("%s:%d:%d", codexHookEventLabels[event], groupIndex, handlerIndex)
				entry := state[hooksPath+":"+name]
				switch trusted, _ := entry["trusted_hash"].(string); {
				case entry["enabled"] == false:
					result.Findings = append(result.Findings, codexTrustFinding{Hook: name, Reason: "disabled"})
				case trusted == "":
					result.Findings = append(result.Findings, codexTrustFinding{Hook: name, Reason: "not approved"})
				case trusted != hash:
					result.Findings = append(result.Findings, codexTrustFinding{Hook: name, Reason: "approval is stale"})
				}
			}
		}
	}
	result.Verified = true
	return result, nil
}

// codexProjectTrust returns the project root spelling Codex recorded for the
// repository and its trust_level, or "" when no entry exists.
func codexProjectTrust(document map[string]any, root string) (string, string) {
	projects, _ := document["projects"].(map[string]any)
	candidates := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		candidates = append(candidates, resolved)
	}
	for _, candidate := range candidates {
		if entry, ok := projects[candidate].(map[string]any); ok {
			level, _ := entry["trust_level"].(string)
			if level != "" {
				return candidate, level
			}
		}
	}
	return root, ""
}

func codexHookState(document map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	hooksTable, _ := document["hooks"].(map[string]any)
	stateTable, _ := hooksTable["state"].(map[string]any)
	for key, value := range stateTable {
		if entry, ok := value.(map[string]any); ok {
			result[key] = entry
		}
	}
	return result
}

// describeCodexTrustFindings renders the findings in hook order.
func describeCodexTrustFindings(findings []codexTrustFinding) string {
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, finding.Hook+" ("+finding.Reason+")")
	}
	return strings.Join(parts, ", ")
}
