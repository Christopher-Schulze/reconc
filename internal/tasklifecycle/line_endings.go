package tasklifecycle

import "strings"

// splitControlLines returns LF lines and the file's own line ending. Boards
// checked out with CRLF (core.autocrlf on Windows) parse exactly like LF
// boards, and rewrites keep the ending the file already uses.
func splitControlLines(body []byte) ([]string, string) {
	text := string(body)
	if strings.Contains(text, "\r\n") {
		return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\r\n"
	}
	return strings.Split(text, "\n"), "\n"
}

func joinControlLines(lines []string, ending string) []byte {
	return []byte(strings.Join(lines, ending))
}

// withControlLineEnding converts an LF rendering back to the original ending.
func withControlLineEnding(body []byte, ending string) []byte {
	if ending != "\r\n" {
		return body
	}
	return []byte(strings.ReplaceAll(string(body), "\n", "\r\n"))
}

// normalizeControlText is the LF view used for parsing a control file.
func normalizeControlText(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}
