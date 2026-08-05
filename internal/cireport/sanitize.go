package cireport

import (
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxTextBytes = 4096

func cleanText(value string) string {
	value = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == '\t' {
			return ' '
		}
		if character < 0x20 || character == 0x7f || character == '\ufeff' {
			return -1
		}
		return character
	}, strings.ToValidUTF8(value, "�"))
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= maxTextBytes {
		return value
	}
	value = value[:maxTextBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "...[bounded]"
}

func cleanPaths(values []string) ([]string, int) {
	result := make([]string, 0, min(len(values), maxPaths))
	for _, value := range values {
		if len(result) == maxPaths {
			break
		}
		if cleaned := cleanRelativePath(value); cleaned != "" {
			result = append(result, cleaned)
		}
	}
	sort.Strings(result)
	result = uniqueStrings(result)
	return result, len(values) - len(result)
}

func cleanRelativePath(value string) string {
	if !utf8.ValidString(value) || value == "" || strings.ContainsRune(value, 0) ||
		strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || hasUnsafePathControl(value) {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	first := cleaned
	if index := strings.IndexByte(first, '/'); index >= 0 {
		first = first[:index]
	}
	if strings.Contains(first, ":") {
		return ""
	}
	return cleaned
}

func hasUnsafePathControl(value string) bool {
	return strings.IndexFunc(value, func(character rune) bool {
		return (character < 0x20 && character != '\n' && character != '\r' && character != '\t') || character == 0x7f
	}) >= 0
}

func uniqueStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
