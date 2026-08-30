package agentsession

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxHostReasonBytes = 1024

func cloneObject(raw map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		out[key] = value
	}
	return out
}

func resultReason(result Result, fallback string) string {
	for _, candidate := range []string{result.Stderr, result.Stdout, fallback} {
		if reason := boundHostReason(candidate); reason != "" {
			return reason
		}
	}
	return boundHostReason(fallback)
}

func boundHostReason(value string) string {
	cleaned := strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return ' '
		}
		return character
	}, value)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if len(cleaned) <= maxHostReasonBytes {
		return cleaned
	}
	const suffix = " [truncated]"
	limit := maxHostReasonBytes - len(suffix)
	for limit > 0 && !utf8.RuneStart(cleaned[limit]) {
		limit--
	}
	return strings.TrimSpace(cleaned[:limit]) + suffix
}
