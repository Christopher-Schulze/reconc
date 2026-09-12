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
	if proofFileURI(value) {
		return proofExternalPath
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
	if value == "" || value == "." || strings.Contains(value, `\`) || pathidentity.Rooted(value) || proofFileURI(value) {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func redactAbsolutePathSpans(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	var quote byte
	for index := 0; index < len(value); {
		if strings.ContainsRune("'\"`", rune(value[index])) && !proofQuoteEscaped(value, index) {
			if quote == value[index] {
				quote = 0
			} else if quote == 0 && tokenBoundaryBefore(value, index) {
				quote = value[index]
			}
		}
		end, ok := proofAbsoluteSpan(value, index, quote)
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

func proofAbsoluteSpan(value string, index int, quote byte) (int, bool) {
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
	case len(rest) >= len("file://") && strings.EqualFold(rest[:len("file://")], "file://"):
		return proofAbsoluteSpanEnd(value, index+len("file://"), quote), true
	case strings.HasPrefix(rest, `\\`), strings.HasPrefix(rest, "//") && (index == 0 || value[index-1] != ':'):
		return proofAbsoluteSpanEnd(value, index, quote), true
	case strings.HasPrefix(rest, "/"):
		return proofAbsoluteSpanEnd(value, index, quote), true
	case len(rest) >= 3 && isASCIILetter(rest[0]) && rest[1] == ':' && (rest[2] == '/' || rest[2] == '\\'):
		return proofAbsoluteSpanEnd(value, index, quote), true
	case len(rest) >= 3 && isASCIILetter(rest[0]) && rest[1] == ':' && rest[2] != '/' && rest[2] != '\\' && !unicode.IsSpace(rune(rest[2])):
		return proofAbsoluteSpanEnd(value, index, quote), true
	default:
		return 0, false
	}
}

func proofAbsoluteSpanEnd(value string, start int, quote byte) int {
	for index := start; index < len(value); {
		character, size := utf8.DecodeRuneInString(value[index:])
		if quote != 0 {
			if character == rune(quote) && !proofQuoteEscaped(value, index) {
				return index
			}
			index += size
			continue
		}
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

func proofQuoteEscaped(value string, index int) bool {
	backslashes := 0
	for index > 0 && value[index-1] == '\\' {
		backslashes++
		index--
	}
	return backslashes%2 != 0
}

func isASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func proofFileURI(value string) bool {
	return len(value) >= len("file:") && strings.EqualFold(value[:len("file:")], "file:")
}
