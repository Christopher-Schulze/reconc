package presets

import (
	"strings"
	"testing"
)

func TestBundledHygienePacksExposeNativeGates(t *testing.T) {
	withRECONCHome(t)
	checks := map[string]string{
		"agent":            "type: source_hygiene",
		"cpp-assurance":    "type: source_hygiene",
		"csharp-assurance": "type: source_hygiene",
		"go-assurance":     "type: go_format",
		"java-assurance":   "type: source_hygiene",
		"php-assurance":    "type: source_hygiene",
		"python-assurance": "type: source_hygiene",
		"rust-assurance":   "type: source_hygiene",
		"shell-assurance":  "type: source_hygiene",
	}
	for pack, expected := range checks {
		content, err := Load(pack)
		if err != nil {
			t.Fatalf("Load(%q): %v", pack, err)
		}
		if !strings.Contains(content, expected) {
			t.Errorf("bundled pack %q is missing %q", pack, expected)
		}
	}
}
