package customruntime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Decision string

const (
	DecisionAllow       Decision = "allow"
	DecisionBlock       Decision = "block"
	DecisionObserve     Decision = "observe"
	DecisionContinue    Decision = "continue"
	DecisionHost        Decision = "host"
	DecisionUnsupported Decision = "unsupported"
)

type NeutralResponse struct {
	Schema        string   `json:"$schema"`
	FormatVersion string   `json:"format_version"`
	Runtime       string   `json:"runtime"`
	HostEvent     string   `json:"host_event"`
	Event         Event    `json:"event"`
	Decision      Decision `json:"decision"`
	Reason        string   `json:"reason,omitempty"`
	ExitCode      int      `json:"exit_code"`
}

func BuildResponse(manifest Manifest, route Route, exitCode int, stdout, stderr string, operationalError error, timedOut bool) NeutralResponse {
	response := NeutralResponse{
		Schema:        NeutralResponseSchemaURL,
		FormatVersion: ResponseFormatVersion, Runtime: manifest.Runtime(),
		HostEvent: route.HostEvent, Event: route.Event, ExitCode: exitCode,
	}
	if reasons := route.DegradedReasons(); len(reasons) > 0 {
		response.Decision = DecisionUnsupported
		response.Reason = strings.Join(reasons, "; ")
		response.ExitCode = 0
		return response
	}
	if operationalError != nil || timedOut {
		policy := route.ErrorPolicy
		if timedOut {
			policy = route.TimeoutPolicy
			response.Reason = fmt.Sprintf("custom runtime exceeded its declared %d second host timeout", route.TimeoutSeconds)
		} else {
			response.Reason = operationalError.Error()
		}
		if policy == FailureBlock {
			response.Decision = DecisionBlock
			response.ExitCode = 2
		} else if policy == FailureHost {
			response.Decision = DecisionHost
			response.ExitCode = 0
		} else {
			response.Decision = DecisionObserve
			response.ExitCode = 0
		}
		return response
	}
	blocked, reason := resultDecision(exitCode, stdout, stderr)
	if route.Response == ResponseObservation {
		response.Decision = DecisionObserve
		response.ExitCode = 0
		response.Reason = reason
		return response
	}
	if blocked {
		response.Decision = DecisionBlock
		if route.Response == ResponseStopContinuation {
			response.Decision = DecisionContinue
		}
		response.ExitCode = 2
		response.Reason = reason
		return response
	}
	response.Decision = DecisionAllow
	response.ExitCode = 0
	return response
}

func BoundResponse(response NeutralResponse, maxBytes int) ([]byte, error) {
	if maxBytes < 256 {
		return nil, fmt.Errorf("neutral response byte budget must be at least 256")
	}
	body := MarshalResponse(response)
	if len(body) <= maxBytes {
		return body, nil
	}
	const suffix = " [truncated]"
	original := response.Reason
	response.Reason = ""
	if len(MarshalResponse(response)) > maxBytes {
		return nil, fmt.Errorf("neutral response metadata exceeds %d bytes", maxBytes)
	}
	runes := []rune(original)
	response.Reason = suffix
	if len(MarshalResponse(response)) > maxBytes {
		response.Reason = ""
		return MarshalResponse(response), nil
	}
	low, high := 0, len(runes)
	for low < high {
		middle := low + (high-low+1)/2
		response.Reason = strings.TrimSpace(string(runes[:middle])) + suffix
		if len(MarshalResponse(response)) <= maxBytes {
			low = middle
		} else {
			high = middle - 1
		}
	}
	response.Reason = strings.TrimSpace(string(runes[:low])) + suffix
	return MarshalResponse(response), nil
}

type LivenessRecord struct {
	Schema         string `json:"$schema"`
	FormatVersion  string `json:"format_version"`
	Runtime        string `json:"runtime"`
	HostEvent      string `json:"host_event"`
	ObservedAt     string `json:"observed_at"`
	ManifestDigest string `json:"manifest_digest"`
}

func ValidateLivenessRecord(record LivenessRecord) error {
	if record.Schema != LivenessSchemaURL || record.FormatVersion != LivenessFormatVersion {
		return fmt.Errorf("custom runtime liveness schema or format_version is invalid")
	}
	name := strings.TrimPrefix(record.Runtime, "custom:")
	if name == record.Runtime || !validName(name, maxNameBytes) || !validHostEvent(record.HostEvent) || !validSHA256Digest(record.ManifestDigest) {
		return fmt.Errorf("custom runtime liveness identity is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.ObservedAt); err != nil {
		return fmt.Errorf("custom runtime liveness timestamp is invalid")
	}
	return nil
}

func MarshalResponse(response NeutralResponse) []byte {
	body, _ := json.Marshal(response)
	return append(body, '\n')
}

func resultDecision(exitCode int, stdout, stderr string) (bool, string) {
	reason := strings.TrimSpace(stderr)
	var object map[string]interface{}
	if json.Unmarshal([]byte(strings.TrimSpace(stdout)), &object) == nil {
		for _, key := range []string{"reason", "user_message", "followup_message", "additionalContext"} {
			if value, _ := object[key].(string); strings.TrimSpace(value) != "" {
				reason = strings.TrimSpace(value)
				break
			}
		}
		decision, _ := object["decision"].(string)
		if decision == "block" || decision == "deny" {
			return true, reason
		}
		if continued, present := object["continue"].(bool); present && !continued {
			return true, reason
		}
	}
	return exitCode == 2, reason
}
