package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxSourceBytes = 2 << 20

var (
	mainElement   = regexp.MustCompile(`(?is)<main\b[^>]*>(.*?)</main\s*>`)
	scriptElement = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	styleElement  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	htmlComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlTag       = regexp.MustCompile(`<[^>]*>`)
	preElement    = regexp.MustCompile(`(?is)<pre\b[^>]*>(.*?)</pre\s*>`)
	htmlLineBreak = regexp.MustCompile(`(?i)<br\s*/?>`)
	// codeFence matches a trimmed code fence line: its marker run and info
	// string, which never contains a backtick after a backtick run.
	codeFence = regexp.MustCompile("^(?:(`{3,})([^`]*)|(~{3,})(.*))$")
	// delimiterCell and compactDelimiterRow match GFM table delimiter cells.
	delimiterCell       = regexp.MustCompile(`^:?-+:?$`)
	compactDelimiterRow = regexp.MustCompile(`^\|(:?-+:?\|)+$`)
	delimiterHyphens    = regexp.MustCompile(`-+`)
)

func digest(body []byte) string {
	value := sha256.Sum256(body)
	return hex.EncodeToString(value[:])
}

// contractDigest pins a source's contract by its normalized text. A markdown
// contract additionally ignores only presentation the CommonMark and GFM
// specifications define as meaningless: the info string of a fenced code
// block, which is not rendered as content, and the number of hyphens in a
// table delimiter cell. Code block lines, escapes, operators, quoting,
// alignment colons, and every other character stay significant.
func contractDigest(contract []byte, mode string) string {
	if mode != "markdown" {
		return digest(contract)
	}
	lines := strings.Split(string(contract), "\n")
	open := ""
	for index, line := range lines {
		if open != "" {
			if closesFence(line, open) {
				open = ""
			}
			continue
		}
		if marker, ok := fenceOpener(line); ok {
			open = marker
			lines[index] = marker
			continue
		}
		// Prose lines are trimmed; a line that starts with whitespace is an
		// indented code line and stays exact.
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		lines[index] = normalizeDelimiterCells(line)
	}
	return digest([]byte(strings.Join(lines, "\n")))
}

// normalizeDelimiterCells rewrites table delimiter cells in one collapsed
// prose line to three hyphens each.
func normalizeDelimiterCells(line string) string {
	tokens := strings.Split(line, " ")
	for index, token := range tokens {
		if compactDelimiterRow.MatchString(token) ||
			delimiterCell.MatchString(token) && index > 0 && index+1 < len(tokens) && tokens[index-1] == "|" && tokens[index+1] == "|" {
			tokens[index] = delimiterHyphens.ReplaceAllString(token, "---")
		}
	}
	return strings.Join(tokens, " ")
}

// fenceOpener reports the marker run of a code fence opening line.
func fenceOpener(line string) (string, bool) {
	match := codeFence.FindStringSubmatch(strings.TrimSpace(line))
	if match == nil {
		return "", false
	}
	return match[1] + match[3], true
}

// closesFence reports a bare fence of the opening kind and at least its
// length, which ends a fenced code block.
func closesFence(line, open string) bool {
	match := codeFence.FindStringSubmatch(strings.TrimSpace(line))
	if match == nil || strings.TrimSpace(match[2]+match[4]) != "" {
		return false
	}
	marker := match[1] + match[3]
	return marker[0] == open[0] && len(marker) >= len(open)
}

// normalizeMarkdown collapses whitespace in prose and keeps code exact where
// whitespace is content: fenced and indented code blocks line by line with
// their indentation, and inline code spans with their spaces. Each prose run
// and each code line is one output line; trailing line whitespace and the
// indentation of fence lines are dropped.
func normalizeMarkdown(text string) string {
	var output []string
	var prose []string
	flushProse := func() {
		if collapsed := collapseProse(prose); collapsed != "" {
			output = append(output, collapsed)
		}
		prose = prose[:0]
	}
	open := ""
	afterBlank, indented := true, false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t")
		switch {
		case open != "" && closesFence(line, open):
			output = append(output, strings.TrimSpace(line))
			open = ""
		case open != "":
			output = append(output, line)
		case line == "":
			// A blank line may continue an indented code block.
			afterBlank = true
			prose = append(prose, line)
		case (afterBlank || indented) && indentedCodeLine(line):
			flushProse()
			output = append(output, line)
			afterBlank, indented = false, true
		default:
			afterBlank, indented = false, false
			if marker, ok := fenceOpener(line); ok {
				flushProse()
				output = append(output, strings.TrimSpace(line))
				open = marker
				continue
			}
			prose = append(prose, line)
		}
	}
	flushProse()
	return strings.Join(output, "\n")
}

// indentedCodeLine reports a line indented by at least four columns, counting
// a tab as four, which CommonMark reads as indented code after a blank line.
func indentedCodeLine(line string) bool {
	columns := 0
	for _, character := range line {
		switch character {
		case ' ':
			columns++
		case '\t':
			columns += 4 - columns%4
		default:
			return columns >= 4
		}
		if columns >= 4 {
			return true
		}
	}
	return false
}

// collapseProse joins prose lines and collapses every whitespace run outside
// inline code spans to one space. A code span keeps its spaces exactly; a line
// end inside it reads as one space, as CommonMark renders it. Backtick runs
// without a closing run of the same length are literal text.
func collapseProse(lines []string) string {
	trimmed := make([]string, len(lines))
	for index, line := range lines {
		trimmed[index] = strings.TrimSpace(line)
	}
	text := strings.Join(trimmed, "\n")
	var out strings.Builder
	pendingSpace := false
	write := func(fragment string) {
		if pendingSpace && out.Len() > 0 {
			out.WriteByte(' ')
		}
		pendingSpace = false
		out.WriteString(fragment)
	}
	for index := 0; index < len(text); {
		if strings.HasPrefix(text[index:], "\\`") {
			write(text[index : index+2])
			index += 2
			continue
		}
		if text[index] == '`' {
			run := len(text[index:]) - len(strings.TrimLeft(text[index:], "`"))
			if closing := closingBacktickRun(text, index+run, run); closing >= 0 {
				write(strings.ReplaceAll(text[index:closing+run], "\n", " "))
				index = closing + run
				continue
			}
			write(text[index : index+run])
			index += run
			continue
		}
		character, size := utf8.DecodeRuneInString(text[index:])
		if unicode.IsSpace(character) {
			pendingSpace = true
		} else {
			write(text[index : index+size])
		}
		index += size
	}
	return out.String()
}

// closingBacktickRun returns the start of the first backtick run of exactly
// length at or after from, or -1.
func closingBacktickRun(text string, from, length int) int {
	for index := from; index < len(text); {
		if text[index] != '`' {
			index++
			continue
		}
		run := len(text[index:]) - len(strings.TrimLeft(text[index:], "`"))
		if run == length {
			return index
		}
		index += run
	}
	return -1
}

// normalizeHTML collapses whitespace in rendered prose and keeps every pre
// element line by line with its indentation, where browsers render the
// whitespace. Markup inside pre, such as highlighting spans, is removed
// without inserting text.
func normalizeHTML(text string) string {
	var output []string
	collapse := func(fragment string) {
		if words := strings.Fields(html.UnescapeString(htmlTag.ReplaceAllString(fragment, " "))); len(words) > 0 {
			output = append(output, strings.Join(words, " "))
		}
	}
	last := 0
	for _, match := range preElement.FindAllStringSubmatchIndex(text, -1) {
		collapse(text[last:match[0]])
		code := htmlLineBreak.ReplaceAllString(text[match[2]:match[3]], "\n")
		code = html.UnescapeString(htmlTag.ReplaceAllString(code, ""))
		lines := strings.Split(code, "\n")
		for index := range lines {
			lines[index] = strings.TrimRight(lines[index], " \t\r")
		}
		if code := strings.Trim(strings.Join(lines, "\n"), "\n"); code != "" {
			output = append(output, code)
		}
		last = match[1]
	}
	collapse(text[last:])
	return strings.Join(output, "\n")
}

func sourceClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("source redirect limit exceeded")
			}
			return validateSourceURL(request.URL.String())
		},
	}
}

func validateSourceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
		return errors.New("source requires an official HTTPS URL without credentials, query, fragment or port")
	}
	switch u.Host {
	case "raw.githubusercontent.com", "learn.chatgpt.com", "developers.openai.com",
		"cursor.com", "docs.devin.ai", "code.claude.com", "antigravity.google",
		"moonshotai.github.io", "docs.x.ai", "zcode.z.ai", "git-scm.com", "docs.github.com":
		return nil
	default:
		return fmt.Errorf("unrecognized source origin %q", u.Host)
	}
}

func fetchSource(ctx context.Context, client *http.Client, source sourceProbe) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/markdown,text/plain,text/html")
	request.Header.Set("User-Agent", "Reconc-host-contract-review/1")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch source: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxSourceBytes+1))
	closeErr := response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source HTTP status %d", response.StatusCode)
	}
	if readErr != nil {
		return nil, fmt.Errorf("read source: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close source: %w", closeErr)
	}
	if len(body) > maxSourceBytes {
		return nil, errors.New("source exceeds 2 MiB byte limit")
	}
	return normalizeSource(body, source.Normalization)
}

func normalizeSource(body []byte, mode string) ([]byte, error) {
	if len(body) == 0 || !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		return nil, errors.New("source is empty or not valid UTF-8 text")
	}
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	if mode == "html" {
		text = scriptElement.ReplaceAllString(text, "")
		text = styleElement.ReplaceAllString(text, "")
		text = htmlComment.ReplaceAllString(text, "")
		if match := mainElement.FindStringSubmatch(text); len(match) == 2 {
			text = match[1]
		}
	} else if strings.Contains(strings.ToLower(text), "<!doctype html") || strings.Contains(strings.ToLower(text), "<html") {
		return nil, errors.New("source returned HTML instead of the expected text contract")
	}
	switch mode {
	case "source":
		lines := strings.Split(text, "\n")
		for index := range lines {
			lines[index] = strings.TrimRight(lines[index], " \t")
		}
		text = strings.TrimSpace(strings.Join(lines, "\n"))
	case "markdown":
		text = normalizeMarkdown(text)
	case "html":
		text = normalizeHTML(text)
	default:
		text = strings.Join(strings.Fields(text), " ")
	}
	if len(text) < 80 {
		return nil, errors.New("source has insufficient contract content")
	}
	return []byte(text), nil
}

func compareSource(ctx context.Context, client *http.Client, host string, source sourceProbe) comparison {
	row := comparison{Host: host, Surface: source.ID, Kind: "upstream", URL: source.URL,
		BaselineURL: source.BaselineURL, ExpectedSHA256: source.SHA256}
	body, err := fetchSource(ctx, client, source)
	if err != nil {
		completeComparison(&row, nil, err, digest)
		return row
	}
	contract, missing := contractSections(body, source.Sections)
	completeComparison(&row, contract, nil, func(contract []byte) string {
		return contractDigest(contract, source.Normalization)
	})
	for _, marker := range source.RequiredMarkers {
		if !bytes.Contains(contract, []byte(marker)) {
			missing = append(missing, marker)
		}
	}
	if len(missing) > 0 {
		row.MissingMarkers = missing
		row.Status = "changed"
	}
	return row
}

// contractSections extracts the configured sections in order, separated by
// one line feed, and names every start or end marker it could not find.
func contractSections(body []byte, sections []sourceSection) ([]byte, []string) {
	var contract bytes.Buffer
	missing := []string{}
	for index, section := range sections {
		start := bytes.Index(body, []byte(section.Start))
		if start < 0 {
			missing = append(missing, section.Start)
			continue
		}
		text := body[start:]
		if section.End != "" {
			end := bytes.Index(text[len(section.Start):], []byte(section.End))
			if end < 0 {
				missing = append(missing, section.End)
				continue
			}
			text = text[:len(section.Start)+end]
		}
		if index > 0 {
			contract.WriteByte('\n')
		}
		contract.Write(text)
	}
	return contract.Bytes(), missing
}
