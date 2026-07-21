package presets

import (
	"strings"
	"testing"
)

func TestBundledHygienePacksExposeNativeGates(t *testing.T) {
	withRECONCHome(t)
	checks := map[string]string{
		"cpp-assurance":        "type: source_hygiene",
		"csharp-assurance":     "type: source_hygiene",
		"elixir-assurance":     "type: source_hygiene",
		"go-assurance":         "type: go_format",
		"java-assurance":       "type: source_hygiene",
		"nextjs-assurance":     "type: source_hygiene",
		"php-assurance":        "type: source_hygiene",
		"powershell-assurance": "type: source_hygiene",
		"python-assurance":     "type: source_hygiene",
		"rust-assurance":       "type: source_hygiene",
		"shell-assurance":      "type: source_hygiene",
		"svelte-assurance":     "type: source_hygiene",
		"typescript-assurance": "type: source_hygiene",
		"zig-assurance":        "type: source_hygiene",
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

func TestGenericPacksDoNotGuessStackCommandsOrSourceTypes(t *testing.T) {
	withRECONCHome(t)
	for _, pack := range []string{"agent", "default"} {
		content, err := Load(pack)
		if err != nil {
			t.Fatalf("Load(%q): %v", pack, err)
		}
		for _, forbidden := range []string{"require_command", "type: source_hygiene", "go test", "npm ", "pytest", "cargo "} {
			if strings.Contains(content, forbidden) {
				t.Errorf("stack-neutral pack %q contains %q", pack, forbidden)
			}
		}
	}
}
