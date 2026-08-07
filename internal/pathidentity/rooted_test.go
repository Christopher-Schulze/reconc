package pathidentity_test

import (
	"testing"

	"reconc.dev/reconc/internal/pathidentity"
)

// TestRootedDecidesTheSameWayOnEveryPlatform is the point of the helper. The
// standard library answers for the running operating system, so a repository
// policy naming `/etc/passwd` is refused on Unix and silently resolved against
// the repository on Windows. Every case here must hold on both.
func TestRootedDecidesTheSameWayOnEveryPlatform(t *testing.T) {
	rooted := []string{
		"/etc/passwd",
		"/",
		`\etc\passwd`,
		`\`,
		`C:\Windows\System32\drivers\etc\hosts`,
		"C:/Windows",
		"c:relative",
		`\\server\share\file`,
		"//server/share/file",
		"  /leading-space-is-still-rooted",
		// Windows reports a volume for any character before a colon, so this
		// is rooted everywhere rather than relative on one platform only.
		"1:file",
	}
	for _, value := range rooted {
		if !pathidentity.Rooted(value) {
			t.Errorf("Rooted(%q) = false, want true on every platform", value)
		}
	}

	relative := []string{
		"scripts/check.sh",
		`scripts\check.sh`,
		"build/schema-report.json",
		"refs/heads/main",
		"a",
		"",
		"   ",
		"not-a-drive:file",
	}
	for _, value := range relative {
		if pathidentity.Rooted(value) {
			t.Errorf("Rooted(%q) = true, want false on every platform", value)
		}
	}
}

func TestEscapesLexicallyReadsBothSeparators(t *testing.T) {
	escaping := []string{
		"..",
		"../outside",
		"a/../../outside",
		`..\outside`,
		`a\..\..\outside`,
		"refs/../../../etc/passwd",
	}
	for _, value := range escaping {
		if !pathidentity.EscapesLexically(value) {
			t.Errorf("EscapesLexically(%q) = false, want true", value)
		}
	}

	contained := []string{
		"scripts/check.sh",
		"a/b/c",
		"..hidden",
		"a/..hidden/b",
		"dot..dot",
		"",
	}
	for _, value := range contained {
		if pathidentity.EscapesLexically(value) {
			t.Errorf("EscapesLexically(%q) = true, want false", value)
		}
	}
}
