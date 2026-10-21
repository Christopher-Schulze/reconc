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
	"unicode/utf8"
)

const maxSourceBytes = 2 << 20

var (
	mainElement   = regexp.MustCompile(`(?is)<main\b[^>]*>(.*?)</main\s*>`)
	scriptElement = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	styleElement  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	htmlComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlTag       = regexp.MustCompile(`<[^>]*>`)
	// codeFence matches a code fence line: its marker run and info string.
	codeFence = regexp.MustCompile("^(`{3,}|~{3,})([A-Za-z0-9_+.#-]*)$")
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
// table delimiter cell. Escapes, operators, quoting, alignment colons, and
// every other character stay significant.
func contractDigest(contract []byte, mode string) string {
	if mode != "markdown" {
		return digest(contract)
	}
	tokens := strings.Fields(string(contract))
	open := ""
	for index, token := range tokens {
		fence := codeFence.FindStringSubmatch(token)
		if open != "" {
			// Code block content stays exact; a bare fence of the same kind
			// and at least the opening length closes the block.
			if fence != nil && fence[2] == "" && fence[1][0] == open[0] && len(fence[1]) >= len(open) {
				open = ""
			}
			continue
		}
		if fence != nil {
			open = fence[1]
			tokens[index] = fence[1]
			continue
		}
		if compactDelimiterRow.MatchString(token) ||
			delimiterCell.MatchString(token) && index > 0 && index+1 < len(tokens) && tokens[index-1] == "|" && tokens[index+1] == "|" {
			tokens[index] = delimiterHyphens.ReplaceAllString(token, "---")
		}
	}
	return digest([]byte(strings.Join(tokens, " ")))
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
		text = html.UnescapeString(htmlTag.ReplaceAllString(text, " "))
	} else if strings.Contains(strings.ToLower(text), "<!doctype html") || strings.Contains(strings.ToLower(text), "<html") {
		return nil, errors.New("source returned HTML instead of the expected text contract")
	}
	if mode == "source" {
		lines := strings.Split(text, "\n")
		for index := range lines {
			lines[index] = strings.TrimRight(lines[index], " \t")
		}
		text = strings.TrimSpace(strings.Join(lines, "\n"))
	} else {
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
