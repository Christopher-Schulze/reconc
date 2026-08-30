package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

type normalizedMCPEnvelope struct {
	Platform          policy.MCPPlatform
	Tool              string
	ServerFingerprint string
	Observed          bool
	BlockingPreHook   bool
	InputValid        bool
	Outcome           string
}

func cursorMCPObject(raw map[string]interface{}, before bool) (map[string]interface{}, map[string]interface{}) {
	input, inputValid := cursorMCPInput(raw)
	envelope := normalizedMCPEnvelope{
		Platform:        policy.MCPPlatformCursor,
		Tool:            cursorToolName(raw),
		Observed:        true,
		BlockingPreHook: true,
		InputValid:      inputValid,
	}
	if fingerprint, present, valid := cursorMCPServerFingerprint(raw); present && valid {
		envelope.ServerFingerprint = fingerprint
	} else if present {
		envelope.InputValid = false
	}
	if !before {
		envelope.Outcome = cursorMCPOutcome(raw)
	}
	return mcpEnvelopeToMap(envelope), input
}

func mcpEnvelopeToMap(envelope normalizedMCPEnvelope) map[string]interface{} {
	out := map[string]interface{}{
		"platform":          string(envelope.Platform),
		"tool":              envelope.Tool,
		"observed":          envelope.Observed,
		"blocking_pre_hook": envelope.BlockingPreHook,
		"input_valid":       envelope.InputValid,
	}
	if envelope.ServerFingerprint != "" {
		out["server_fingerprint"] = envelope.ServerFingerprint
	}
	if envelope.Outcome != "" {
		out["outcome"] = envelope.Outcome
	}
	return out
}

func cursorMCPInput(raw map[string]interface{}) (map[string]interface{}, bool) {
	for _, key := range []string{"tool_input", "toolInput", "input", "args"} {
		switch value := raw[key].(type) {
		case map[string]interface{}:
			return cloneObject(value), true
		case string:
			var decoded map[string]interface{}
			if err := json.Unmarshal([]byte(value), &decoded); err == nil && decoded != nil {
				return decoded, true
			}
			return map[string]interface{}{}, false
		}
	}
	return map[string]interface{}{}, false
}

func cursorMCPServerFingerprint(raw map[string]interface{}) (fingerprint string, present, valid bool) {
	urlValue, urlPresent := raw["url"]
	commandValue, commandPresent := raw["command"]
	if urlPresent && commandPresent {
		return "", true, false
	}
	if urlPresent {
		locator, ok := exactString(urlValue)
		if !ok {
			return "", true, false
		}
		normalized, valid := normalizeMCPURL(locator)
		if !valid {
			return "", true, false
		}
		return fingerprintMCPServer(normalized), true, true
	}
	if commandPresent {
		locator, ok := exactString(commandValue)
		if !ok || strings.TrimSpace(locator) == "" {
			return "", true, false
		}
		return fingerprintMCPServer(locator), true, true
	}
	return "", false, true
}

func exactString(value interface{}) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

func normalizeMCPURL(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String(), true
}

func fingerprintMCPServer(locator string) string {
	sum := sha256.Sum256([]byte(locator))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cursorMCPOutcome(raw map[string]interface{}) string {
	if value, present := raw["error"]; present && value != nil {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) != "" {
			return "failure"
		}
	}
	for _, key := range []string{"tool_response", "toolResponse", "result_json", "resultJson", "response", "result"} {
		value, present := raw[key]
		if !present {
			continue
		}
		object, ok := mcpResultObject(value)
		if !ok {
			return "failure"
		}
		if errorValue, exists := object["error"]; exists && errorValue != nil {
			return "failure"
		}
		explicitSuccess := false
		for _, errorKey := range []string{"isError", "is_error"} {
			value, exists := object[errorKey]
			if !exists {
				continue
			}
			isError, valid := value.(bool)
			if !valid || isError {
				return "failure"
			}
			explicitSuccess = true
		}
		if value, exists := object["success"]; exists {
			success, valid := value.(bool)
			if !valid || !success {
				return "failure"
			}
			explicitSuccess = true
		}
		if explicitSuccess {
			return "success"
		}
		return "failure"
	}
	// A post event proves completion, not success. Positive MCP evidence
	// requires an explicit host result; missing or unknown shapes fail closed
	// into a non-evidentiary failure observation.
	return "failure"
}

func mcpResultObject(value interface{}) (map[string]interface{}, bool) {
	if object, ok := value.(map[string]interface{}); ok {
		return object, true
	}
	text, ok := value.(string)
	if !ok {
		return nil, false
	}
	var object map[string]interface{}
	if err := json.Unmarshal([]byte(text), &object); err != nil || object == nil {
		return nil, false
	}
	return object, true
}
