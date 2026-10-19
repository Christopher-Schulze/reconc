package templates

import (
	"slices"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

// The template protects every repository-local hook artifact the registry can
// install plus the host settings that can switch an installed hook off; a new
// host or path must extend it.
func TestHostHookConfigTemplateCoversEveryRepositoryHookArtifact(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	template, err := Resolve("host-hook-config-read-only")
	if err != nil {
		t.Fatal(err)
	}
	if template.Body["kind"] != "deny_write" || template.Body["mode"] != "block" {
		t.Fatalf("template is not a blocking deny_write rule: %#v", template.Body)
	}
	raw, _ := template.Body["paths"].([]interface{})
	paths := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			paths = append(paths, text)
		}
	}
	want := []string{
		hooks.WrapperPath, hooks.DSHPatchPath,
		// Every path the wrapper resolves a binary from: replacing any of them
		// replaces the program every hook runs.
		hooks.WrapperTargetPath, "tools/reconc/dist/**", "dist/reconc-*",
		".claude/settings.local.json",
		".github/copilot/settings.json", ".github/copilot/settings.local.json",
		".omp/config.yml", ".omp/settings.json",
	}
	for _, platform := range hooks.Platforms() {
		for _, path := range []string{platform.TargetPath, platform.ScaffoldPath, platform.Activation.EnablePath} {
			if path != "" && !strings.HasPrefix(path, "~") {
				want = append(want, path)
			}
		}
	}
	want = append(want, hooks.WrapperDevelopmentBinaryPaths...)
	for _, path := range want {
		if !slices.Contains(paths, path) {
			t.Errorf("host-hook-config-read-only omits %s", path)
		}
	}
}
