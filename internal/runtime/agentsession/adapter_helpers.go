package agentsession

import "strings"

func cloneObject(raw map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		out[key] = value
	}
	return out
}

func resultReason(result Result, fallback string) string {
	for _, candidate := range []string{result.Stderr, result.Stdout, fallback} {
		if reason := strings.TrimSpace(candidate); reason != "" {
			return reason
		}
	}
	return fallback
}
