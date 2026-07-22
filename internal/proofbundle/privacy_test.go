package proofbundle

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSanitizePathsUsesPortableRelativeIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-project")
	inside := filepath.Join(root, "docs", "proof.md")
	got := sanitizePaths(root, []string{inside, `docs\windows.md`, "../outside", inside})
	want := []string{"<external>", "docs/proof.md", "docs/windows.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitizePaths = %#v, want %#v", got, want)
	}
}

func TestSanitizeTextRedactsIdentityAbsolutePathsAndSecrets(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-project")
	windowsPrivatePath := `C:` + `\Users\` + `alice`
	input := root + " private-project /etc/passwd " + windowsPrivatePath + " token=abc123 github_pat_1234567890"
	got := sanitizeText(root, input)
	for _, forbidden := range []string{root, "private-project", "/etc/passwd", windowsPrivatePath, "abc123", "github_pat_"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("sanitizeText leaked %q: %s", forbidden, got)
		}
	}
	for _, expected := range []string{".", "<repo>", "<external>", "token=<redacted>"} {
		if !strings.Contains(got, expected) {
			t.Errorf("sanitizeText omitted %q: %s", expected, got)
		}
	}
}

func TestSanitizersRemainBoundedAndUnique(t *testing.T) {
	values := make([]string, maxItems+40)
	for index := range values {
		values[index] = strings.Repeat("x", maxTextBytes+100) + string(rune('a'+index%26))
	}
	got := sanitizeValues("", values)
	if len(got) > maxItems {
		t.Fatalf("sanitized list has %d items, max %d", len(got), maxItems)
	}
	for _, value := range got {
		if len(value) > maxTextBytes+len("...[bounded]") {
			t.Fatalf("sanitized value exceeds bound: %d", len(value))
		}
	}
}
