package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiscoverInventoryCoversReleaseDependencyLicenses(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	target := runtime.GOOS + "/" + runtime.GOARCH
	inventory, err := discoverInventory(root, "go", []string{target})
	if err != nil {
		t.Fatal(err)
	}
	body, err := renderNotices(inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"github.com/modelcontextprotocol/go-sdk@v1.7.0",
		"go@go",
		"File: LICENSE",
	} {
		if !bytes.Contains(body, []byte(text)) {
			t.Fatalf("notice output omits %q", text)
		}
	}
	if bytes.Contains(body, []byte(filepath.Clean(os.TempDir()))) {
		t.Fatal("notice output leaked a local temporary path")
	}
}

func TestCollectLicenseFilesRejectsMissingAndSymlinkedNotice(t *testing.T) {
	missing := t.TempDir()
	if _, err := collectLicenseFiles(missing); err == nil {
		t.Fatal("module without a license file was accepted")
	}
	target := filepath.Join(t.TempDir(), "license-target")
	if err := os.WriteFile(target, []byte("MIT License\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkRoot := t.TempDir()
	if err := os.Symlink(target, filepath.Join(linkRoot, "LICENSE")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation is unavailable")
		}
		t.Fatal(err)
	}
	if _, err := collectLicenseFiles(linkRoot); err == nil {
		t.Fatal("symlinked license file was accepted")
	}
}

func TestRenderNoticesIsDeterministicAndRetainsExactLicenseText(t *testing.T) {
	inventory := noticeInventory{
		Targets: []string{"linux/amd64"},
		Components: []noticeComponent{{
			Identity: "example.invalid/module@v1.0.0",
			Files: []noticeFile{{
				Name: "LICENSE", Digest: strings.Repeat("a", 64), Body: []byte("exact text"),
			}},
		}},
	}
	first, err := renderNotices(inventory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderNotices(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.Contains(first, []byte("exact text\n")) {
		t.Fatal("notice rendering is not deterministic or changed license text")
	}
}

func TestNormalizeTargetsAndModuleDecodeRejectAmbiguity(t *testing.T) {
	if _, err := normalizeTargets([]string{"darwin/arm64", "darwin/arm64"}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", "darwin", "darwin/ARM64", "../darwin/arm64"} {
		if _, err := normalizeTargets([]string{target}); err == nil {
			t.Fatalf("invalid target %q was accepted", target)
		}
	}
	conflict := strings.Join([]string{
		`{"Module":{"Path":"example.invalid/m","Version":"v1.0.0","Dir":"/a"}}`,
		`{"Module":{"Path":"example.invalid/m","Version":"v1.0.0","Dir":"/b"}}`,
	}, "\n")
	if _, err := decodeListedModules(strings.NewReader(conflict)); err == nil {
		t.Fatal("one module identity resolving to multiple directories was accepted")
	}
	replacement := `{"Module":{"Path":"example.invalid/m","Version":"v1.0.0","Dir":"/a","Replace":{"Path":"../local"}}}`
	if _, err := decodeListedModules(strings.NewReader(replacement)); err == nil {
		t.Fatal("module replacement was accepted")
	}
}

func FuzzDecodeListedModules(f *testing.F) {
	f.Add(`{"ImportPath":"example","Module":{"Path":"example.invalid/m","Version":"v1.0.0","Dir":"/tmp/m"}}`)
	f.Add(`{"ImportPath":"unsafe","Module":{"Path":"example.invalid/m","Replace":{"Path":"../local"}}}`)
	f.Fuzz(func(t *testing.T, body string) {
		_, _ = decodeListedModules(strings.NewReader(body))
	})
}
