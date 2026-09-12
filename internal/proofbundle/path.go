package proofbundle

import (
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"reconc.dev/reconc/internal/pathidentity"
)

const proofExternalPath = "<external>"

func sanitizeProofPath(root, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if pathidentity.Rooted(value) {
		return relativizeHostAbsolute(root, value)
	}
	normalized := path.Clean(strings.ReplaceAll(value, `\`, "/"))
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || pathidentity.Rooted(normalized) {
		return proofExternalPath
	}
	return normalized
}

func relativizeHostAbsolute(root, value string) string {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(value) {
		return proofExternalPath
	}
	relative, err := filepath.Rel(filepath.Clean(root), value)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return proofExternalPath
	}
	relative = filepath.ToSlash(relative)
	if relative == "" || pathidentity.Rooted(relative) || strings.Contains(relative, `\`) {
		return proofExternalPath
	}
	return relative
}

func portableProofPath(value string) bool {
	if value == proofExternalPath {
		return true
	}
	if value == "" || value == "." || strings.Contains(value, `\`) || pathidentity.Rooted(value) {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func redactAbsolutePathSpans(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	for index := 0; index < len(value); {
		end, ok := proofAbsoluteSpan(value, index)
		if ok && end > index {
			output.WriteString(proofExternalPath)
			index = end
			continue
		}
		output.WriteByte(value[index])
		index++
	}
	return output.String()
}

func proofAbsoluteSpan(value string, index int) (int, bool) {
	if !tokenBoundaryBefore(value, index) {
		return 0, false
	}
	if index+1 < len(value) && value[index] == '/' && index > 0 && value[index-1] == ':' && value[index+1] == '/' {
		return 0, false
	}
	if index > 1 && value[index] == '/' && value[index-1] == '/' && value[index-2] == ':' {
		return 0, false
	}
	if index > 0 && (value[index] == '/' || value[index] == '\\') && value[index-1] == '.' {
		return 0, false
	}
	rest := value[index:]
	switch {
	case strings.HasPrefix(rest, `\\`), strings.HasPrefix(rest, "//") && (index == 0 || value[index-1] != ':'):
		return proofAbsoluteSpanEnd(value, index), true
	case strings.HasPrefix(rest, "/"):
		return proofAbsoluteSpanEnd(value, index), true
	case len(rest) >= 3 && isASCIILetter(rest[0]) && rest[1] == ':' && (rest[2] == '/' || rest[2] == '\\'):
		return proofAbsoluteSpanEnd(value, index), true
	case len(rest) >= 3 && isASCIILetter(rest[0]) && rest[1] == ':' && rest[2] != '/' && rest[2] != '\\' && !unicode.IsSpace(rune(rest[2])):
		return proofAbsoluteSpanEnd(value, index), true
	default:
		return 0, false
	}
}

func proofAbsoluteSpanEnd(value string, start int) int {
	for index := start; index < len(value); {
		character, size := utf8.DecodeRuneInString(value[index:])
		if character < 0x20 || unicode.IsSpace(character) || strings.ContainsRune("'\"`)]}>,;", character) {
			return index
		}
		if character == ':' && !(index == start+1 && isASCIILetter(value[start])) {
			return index
		}
		index += size
	}
	return len(value)
}

func isASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}
