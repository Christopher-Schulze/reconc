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

func TestPublicREADMEListsEveryShippedAssurancePack(t *testing.T) {
	root := publicSurfaceRoot(t)
	readme := readPublicSurfaceFile(t, root, "README.md")
	entries, err := os.ReadDir(filepath.Join(root, "internal", "presets", "packs"))
	if err != nil {
		t.Fatal(err)
	}
	packCount := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "-assurance.yml") {
			continue
		}
		packCount++
		pack := strings.TrimSuffix(name, ".yml")
		if !strings.Contains(readme, "`"+pack+"`") {
			t.Errorf("README.md omits shipped assurance pack %q", pack)
		}
	}
	if packCount == 0 {
		t.Fatal("no shipped assurance packs found")
	}
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
	for _, path := range []string{"README.md", "docs/documentation.md", "docs/commands.md", "skills/reconc/SKILL.md"} {
		assertOrderedTokens(t, path, readPublicSurfaceFile(t, root, path), tokens)
	}
}

func TestPublicBrandImageHasExactDimensionsAndBoundedSize(t *testing.T) {
	root := publicSurfaceRoot(t)
	assertPNGAsset(t, filepath.Join(root, "assets/reconc.png"), 1774, 887, 1_000_000)
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

func assertOrderedTokens(t *testing.T, path, body string, tokens []string) {
	t.Helper()
	position := 0
	for _, token := range tokens {
		next := strings.Index(body[position:], token)
		if next < 0 {
			t.Fatalf("%s omits or reorders daily-loop command %q", path, token)
		}
		position += next + len(token)
	}
}
