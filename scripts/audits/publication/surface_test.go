package main

import (
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

var markdownLinkPattern = regexp.MustCompile(`!?\[[^]\n]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
var htmlImagePattern = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)

func TestPublicREADMEContract(t *testing.T) {
	root := publicSurfaceRoot(t)
	readme := readPublicSurfaceFile(t, root, "README.md")
	for _, want := range []string{
		"# Reconc\n",
		"**AI agents say they're done. Reconc proves it.**",
		"assets/reconc.png",
		"## See the real loop in under a minute",
		"## OpenAI Build Week",
		"2daa5372b08d7f479d895b2b5419a39026eb6719",
		"Claude also assisted",
		"## Production dogfooding",
		"## FAQ",
		"## Security boundary",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("README.md omits required public-surface text %q", want)
		}
	}
	if strings.Contains(readme, "reconc-release.yml/badge.svg") {
		t.Error("README exposes a release badge before a public release exists")
	}
	if strings.Index(readme, "reconc demo") > strings.Index(readme, "## What It Does") {
		t.Error("README does not expose the real demo before deep product detail")
	}
}

func TestPublicBrandAssetHasExactDimensionsAndBoundedSize(t *testing.T) {
	root := publicSurfaceRoot(t)
	assertPNGAsset(t, filepath.Join(root, "assets/reconc.png"), 1774, 887, 1_000_000)
	assertBoundedFile(t, filepath.Join(root, "assets/reconc-visual-philosophy.md"), 8_000)
}

func TestREADMELocalLinksAndAnchorsResolve(t *testing.T) {
	root := publicSurfaceRoot(t)
	readme := readPublicSurfaceFile(t, root, "README.md")
	links := markdownLinkPattern.FindAllStringSubmatch(readme, -1)
	links = append(links, htmlImagePattern.FindAllStringSubmatch(readme, -1)...)
	for _, match := range links {
		if len(match) < 2 || isRemoteLink(match[1]) {
			continue
		}
		assertLocalLink(t, root, "README.md", match[1])
	}
}

func TestCanonicalDailyLoopMatchesEveryTeachingSurface(t *testing.T) {
	root := publicSurfaceRoot(t)
	tokens := []string{"reconc session-briefing . --json", "reconc check .", "reconc next .", "reconc done ."}
	for _, surface := range []struct {
		path, start, end string
	}{
		{path: "README.md", start: "Then use the daily loop:", end: "An AI agent, not the user"},
		{path: "docs/documentation.md", start: "Then use the canonical daily loop:", end: "`session-briefing --json` is"},
		{path: "docs/commands.md", start: "four-command daily", end: "Everything below is"},
		{path: "skills/reconc/SKILL.md", start: "## Daily Agent Loop", end: "## Evidence Rules"},
	} {
		body := readPublicSurfaceFile(t, root, surface.path)
		assertOrderedSectionTokens(t, surface.path, body, surface.start, surface.end, tokens)
	}
}

func TestKnownPublicSurfaceDriftStaysClosed(t *testing.T) {
	root := publicSurfaceRoot(t)
	files := []string{"README.md", "AGENTS.md", "install.sh", "docs/documentation.md", "docs/commands.md", ".github/releases/reconc-v0.6.0.md"}
	stale := []string{"this is the one shell script", "all eight agent runtimes", "all nine hook platforms", "41 subcommands", "42 subcommands", "43 subcommands"}
	for _, path := range files {
		body := readPublicSurfaceFile(t, root, path)
		for _, phrase := range stale {
			if strings.Contains(body, phrase) {
				t.Errorf("%s contains stale public claim %q", path, phrase)
			}
		}
	}
}

func publicSurfaceRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func readPublicSurfaceFile(t *testing.T, root, relative string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(body)
}

func assertPNGAsset(t *testing.T, path string, width, height int, maxBytes int64) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(file)
	closeErr := file.Close()
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if closeErr != nil {
		t.Fatalf("close %s: %v", path, closeErr)
	}
	if config.Width != width || config.Height != height {
		t.Errorf("%s dimensions = %dx%d, want %dx%d", path, config.Width, config.Height, width, height)
	}
	assertBoundedFile(t, path, maxBytes)
}

func assertBoundedFile(t *testing.T, path string, maxBytes int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		t.Errorf("%s size = %d bytes, expected 1..%d", path, info.Size(), maxBytes)
	}
}

func isRemoteLink(target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:")
}

func assertLocalLink(t *testing.T, root, source, target string) {
	t.Helper()
	pathPart, anchor, _ := strings.Cut(target, "#")
	path := filepath.Join(root, filepath.Dir(source), filepath.FromSlash(pathPart))
	if pathPart == "" {
		path = filepath.Join(root, source)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("%s link %q does not resolve: %v", source, target, err)
		return
	}
	if anchor != "" && !markdownAnchors(string(body))[anchor] {
		t.Errorf("%s link %q has no matching heading", source, target)
	}
}

func markdownAnchors(body string) map[string]bool {
	anchors := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		anchors[markdownSlug(heading)] = true
	}
	return anchors
}

func markdownSlug(value string) string {
	var clean strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == ' ' || char == '-' {
			clean.WriteRune(char)
		}
	}
	return strings.Join(strings.Fields(clean.String()), "-")
}

func assertOrderedSectionTokens(t *testing.T, path, body, start, end string, tokens []string) {
	t.Helper()
	startIndex := strings.Index(body, start)
	if startIndex < 0 {
		t.Fatalf("%s omits section start %q", path, start)
	}
	section := body[startIndex+len(start):]
	if endIndex := strings.Index(section, end); endIndex >= 0 {
		section = section[:endIndex]
	}
	position := 0
	for _, token := range tokens {
		next := strings.Index(section[position:], token)
		if next < 0 {
			t.Fatalf("%s daily loop omits or reorders %q", path, token)
		}
		position += next + len(token)
	}
}
