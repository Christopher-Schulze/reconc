package presets

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
)

// withRECONCHome points the loader at a fresh temp directory for the
// duration of the test, restoring the previous env var afterwards.
func withRECONCHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(HomeEnvVar, dir)
	return dir
}

func TestListReturnsBundledPresetsSorted(t *testing.T) {
	withRECONCHome(t) // empty user dir
	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) < 3 {
		t.Fatalf("expected at least 3 bundled presets, got %d: %v", len(got), got)
	}
	names := make([]string, len(got))
	for i, p := range got {
		names[i] = p.Name
	}
	want := []string{"default", "docs-sync", "strict"}
	for _, w := range want {
		found := false
		for _, n := range names {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected bundled preset %q in list, got %v", w, names)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Name >= got[i].Name {
			t.Errorf("List() not sorted: %q >= %q", got[i-1].Name, got[i].Name)
		}
	}
}

func TestParseManifestAppliesBoundedYAMLAdmission(t *testing.T) {
	aliases := "base: &base\n  id: one\nrules:\n" + strings.Repeat("  - *base\n", 1025)
	if _, err := parseManifest("bounded", aliases); err == nil || !strings.Contains(err.Error(), "yaml aliases") {
		t.Fatalf("preset alias amplification error = %v", err)
	}
}

func TestListMarksBundledSource(t *testing.T) {
	withRECONCHome(t)
	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range got {
		if p.Source != SourceBundled {
			t.Errorf("preset %q should be marked bundled, got %q", p.Name, p.Source)
		}
	}
}

func TestLoadBundledDefault(t *testing.T) {
	withRECONCHome(t)
	content, err := Load("default")
	if err != nil {
		t.Fatalf("Load(\"default\"): %v", err)
	}
	if !strings.Contains(content, "preset-default-generated-read-only") {
		t.Errorf("bundled default preset content missing expected rule id")
	}
}

func TestLoadStrictAndDocsSync(t *testing.T) {
	withRECONCHome(t)
	for _, name := range []string{"strict", "docs-sync"} {
		t.Run(name, func(t *testing.T) {
			content, err := Load(name)
			if err != nil {
				t.Fatalf("Load(%q): %v", name, err)
			}
			if !strings.Contains(content, "rules:") {
				t.Errorf("preset %q missing rules: section", name)
			}
		})
	}
}

func TestLoadEmptyNameReturnsError(t *testing.T) {
	withRECONCHome(t)
	_, err := Load("")
	if err == nil {
		t.Fatal("expected error for empty preset name")
	}
	var pe *rerrors.PresetError
	if !stderrors.As(err, &pe) {
		t.Errorf("expected *PresetError, got %T", err)
	}
}

func TestLoadUnknownReturnsPresetNotFoundError(t *testing.T) {
	withRECONCHome(t)
	_, err := Load("definitely-not-a-bundled-preset")
	if err == nil {
		t.Fatal("expected error for unknown preset")
	}
	var pnf *rerrors.PresetNotFoundError
	if !stderrors.As(err, &pnf) {
		t.Errorf("expected *PresetNotFoundError, got %T", err)
	}
}

func TestPresetNamesRejectTraversalAndNonCanonicalSpelling(t *testing.T) {
	withRECONCHome(t)
	for _, name := range []string{"../secret", "nested/name", "name.yml", "Uppercase", "-leading"} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(name); err == nil {
				t.Fatalf("unsafe preset name %q was accepted", name)
			}
			if _, _, err := Path(name); err == nil {
				t.Fatalf("unsafe preset path %q was accepted", name)
			}
		})
	}
}

func TestUserPresetOverridesBundled(t *testing.T) {
	home := withRECONCHome(t)
	presetsDir := filepath.Join(home, "presets")
	if err := os.MkdirAll(presetsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	override := "rules:\n  - id: my-override\n    kind: deny_write\n    paths: ['x/**']\n    mode: warn\n    message: x\n"
	if err := os.WriteFile(filepath.Join(presetsDir, "default.yml"), []byte(override), 0o644); err != nil {
		t.Fatalf("write user preset: %v", err)
	}

	content, err := Load("default")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(content, "my-override") {
		t.Errorf("user preset content should win, got: %s", content)
	}
}

func TestListIncludesUserPresets(t *testing.T) {
	home := withRECONCHome(t)
	presetsDir := filepath.Join(home, "presets")
	if err := os.MkdirAll(presetsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(presetsDir, "my-custom.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range got {
		if p.Name == "my-custom" {
			if p.Source != SourceUser {
				t.Errorf("user preset should be marked user, got %q", p.Source)
			}
			return
		}
	}
	t.Error("user preset my-custom missing from List()")
}

func TestListFailsClosedOnInvalidUserPresetFilename(t *testing.T) {
	home := withRECONCHome(t)
	presetsDir := filepath.Join(home, "presets")
	if err := os.MkdirAll(presetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(presetsDir, "Invalid.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil {
		t.Fatal("invalid user preset filename was silently accepted")
	}
}

func TestPathBundled(t *testing.T) {
	withRECONCHome(t)
	path, src, err := Path("default")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if src != SourceBundled {
		t.Errorf("expected bundled source, got %q", src)
	}
	if path != "packs/default.yml" {
		t.Errorf("expected packs/default.yml, got %s", path)
	}
}

func TestPathUser(t *testing.T) {
	home := withRECONCHome(t)
	presetsDir := filepath.Join(home, "presets")
	if err := os.MkdirAll(presetsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	wanted := filepath.Join(presetsDir, "mine.yml")
	if err := os.WriteFile(wanted, []byte("rules: []\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	path, src, err := Path("mine")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if src != SourceUser {
		t.Errorf("expected user source, got %q", src)
	}
	if path != wanted {
		t.Errorf("expected %s, got %s", wanted, path)
	}
}

func TestPathUnknownReturnsNotFound(t *testing.T) {
	withRECONCHome(t)
	_, _, err := Path("does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown preset")
	}
	var pnf *rerrors.PresetNotFoundError
	if !stderrors.As(err, &pnf) {
		t.Errorf("expected *PresetNotFoundError, got %T", err)
	}
}

func TestHomeRespectsEnvVar(t *testing.T) {
	override := filepath.Join(t.TempDir(), "custom", "path")
	t.Setenv(HomeEnvVar, override)
	if got := Home(); got != override {
		t.Errorf("expected %s, got %s", override, got)
	}
}

func TestHomeFallsBackToHomeDotReconc(t *testing.T) {
	t.Setenv(HomeEnvVar, "")
	got := Home()
	if filepath.Base(got) != ".reconc" {
		t.Errorf("expected final path component .reconc, got %s", got)
	}
}

func TestHomeResolutionFailureNeverFallsBackRelative(t *testing.T) {
	t.Setenv(HomeEnvVar, "")
	original := resolveUserHome
	resolveUserHome = func() (string, error) { return "", stderrors.New("home unavailable") }
	t.Cleanup(func() { resolveUserHome = original })
	if home, err := ResolveHome(); err == nil || home != "" {
		t.Fatalf("ResolveHome() = %q, %v; want explicit failure", home, err)
	}
	if home := Home(); home != "" {
		t.Fatalf("Home() = %q; failure must not become a relative path", home)
	}
	if _, err := List(); err == nil || !strings.Contains(err.Error(), "home unavailable") {
		t.Fatalf("List did not propagate home failure: %v", err)
	}
}

func TestUserPresetRootRejectsSymlinkBeforeBundledFallback(t *testing.T) {
	home := withRECONCHome(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(home, "presets")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Load("default"); err == nil || !strings.Contains(err.Error(), "non-symlink directory") {
		t.Fatalf("symlinked user preset root was accepted: %v", err)
	}
}

func TestUserDirectoryRejectsTraversal(t *testing.T) {
	withRECONCHome(t)
	if _, err := UserDirectory("../presets"); err == nil {
		t.Fatal("user directory traversal was accepted")
	}
}
