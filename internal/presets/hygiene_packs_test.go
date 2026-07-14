package presets

import (
	"strings"
	"testing"
)

func TestBundledHygienePacksExposeNativeGates(t *testing.T) {
	withRECONCHome(t)
	checks := map[string]string{
		"agent":        "type: source_hygiene",
		"go-assurance": "type: go_format",
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
