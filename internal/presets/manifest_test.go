package presets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledManifestsBindCapabilitiesToRealRules(t *testing.T) {
	withRECONCHome(t)
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, metadata := range list {
		if metadata.Source != SourceBundled {
			continue
		}
		if metadata.Manifest == nil {
			t.Errorf("bundled preset %s has no explicit manifest", metadata.Name)
		}
	}
}

func TestValidateSelectionRejectsSymmetricConflictDeterministically(t *testing.T) {
	home := withRECONCHome(t)
	dir := filepath.Join(home, "presets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifestPreset(t, dir, "alpha", []string{"beta"})
	writeManifestPreset(t, dir, "beta", nil)
	err := ValidateSelection([]string{"beta", "alpha"})
	if err == nil || !strings.Contains(err.Error(), "alpha <-> beta") {
		t.Fatalf("expected sorted symmetric conflict, got %v", err)
	}
}

func TestSuggestForStacksProposesButDoesNotSelect(t *testing.T) {
	withRECONCHome(t)
	suggestions, err := SuggestForStacks([]string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	if !metadataContains(suggestions, "go-assurance") {
		t.Fatalf("expected go-assurance suggestion, got %+v", suggestions)
	}
	if metadataContains(suggestions, "default") {
		t.Fatal("generic wildcard pack must not be inferred from stack detection")
	}
}

func TestSuggestForPortableStacksReturnsSpecificPacks(t *testing.T) {
	withRECONCHome(t)
	suggestions, err := SuggestForStacks([]string{"python", "rust"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"python-assurance", "rust-assurance"} {
		if !metadataContains(suggestions, expected) {
			t.Fatalf("expected %s suggestion, got %+v", expected, suggestions)
		}
	}
}

func TestSuggestForAdditionalPortableStacksReturnsSpecificPacks(t *testing.T) {
	withRECONCHome(t)
	suggestions, err := SuggestForStacks([]string{"shell", "cpp", "java", "php", "csharp"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"shell-assurance", "cpp-assurance", "java-assurance", "php-assurance", "csharp-assurance"} {
		if !metadataContains(suggestions, expected) {
			t.Fatalf("expected %s suggestion, got %+v", expected, suggestions)
		}
	}
}

func writeManifestPreset(t *testing.T, dir, name string, conflicts []string) {
	t.Helper()
	conflictYAML := "[]"
	if len(conflicts) > 0 {
		conflictYAML = "[\"" + strings.Join(conflicts, "\", \"") + "\"]"
	}
	body := "pack:\n" +
		"  format_version: \"1\"\n" +
		"  name: " + name + "\n" +
		"  summary: test pack\n" +
		"  stacks: [test]\n" +
		"  capabilities:\n" +
		"    - id: cap\n" +
		"      inputs: [source]\n" +
		"      evidence: [write-path]\n" +
		"      rules: [rule]\n" +
		"  conflicts: " + conflictYAML + "\n" +
		"rules:\n" +
		"  - id: rule\n" +
		"    kind: deny_write\n" +
		"    mode: warn\n" +
		"    paths: [generated/**]\n" +
		"    message: test\n"
	if err := os.WriteFile(filepath.Join(dir, name+PresetSuffix), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func metadataContains(values []Metadata, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}
