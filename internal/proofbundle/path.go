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

type proofTextQuote struct {
	character   byte
	backslashes int
}

func redactAbsolutePathSpans(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	var quote proofTextQuote
	for index := 0; index < len(value); {
		if delimiter, end := proofQuoteToken(value, index); end > index {
			if quote.character == delimiter.character && quote.matches(delimiter.backslashes) {
				quote = proofTextQuote{}
			} else if quote.character == 0 && tokenBoundaryBefore(value, index) {
				quote = delimiter
				if quote.backslashes%2 == 0 {
					quote.backslashes = 0
				}
			}
			output.WriteString(value[index:end])
			index = end
			continue
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

func proofAbsoluteSpan(value string, index int, quote proofTextQuote) (int, bool) {
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

func proofAbsoluteSpanEnd(value string, start int, quote proofTextQuote) int {
	for index := start; index < len(value); {
		character, size := utf8.DecodeRuneInString(value[index:])
		if quote.character != 0 {
			if character == rune(quote.character) && quote.matches(proofQuoteBackslashes(value, index)) {
				return index - quote.backslashes
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

// Consume encoded delimiters before UNC recognition can mistake their leading
// backslashes for a path. Preserve the encoding instead of unescaping the text.
func proofQuoteToken(value string, index int) (proofTextQuote, int) {
	end := index
	for end < len(value) && value[end] == '\\' {
		end++
	}
	if end == len(value) || !strings.ContainsRune("'\"`", rune(value[end])) {
		return proofTextQuote{}, index
	}
	return proofTextQuote{character: value[end], backslashes: end - index}, end + 1
}

func (q proofTextQuote) matches(backslashes int) bool {
	// At encoding depth one, delimiters have 1, 5, 9, ... backslashes;
	// embedded quotes have 3, 7, 11, ... . Each additional depth doubles
	// that width. Trailing encoded path backslashes must not hide the end.
	width := q.backslashes + 1
	return backslashes >= q.backslashes && (backslashes+1)%width == 0 && (backslashes+1)/width%2 == 1
}

func proofQuoteBackslashes(value string, index int) int {
	backslashes := 0
	for index > 0 && value[index-1] == '\\' {
		backslashes++
		index--
	}
	return backslashes
}

func isASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func proofFileURI(value string) bool {
	return len(value) >= len("file:") && strings.EqualFold(value[:len("file:")], "file:")
}
