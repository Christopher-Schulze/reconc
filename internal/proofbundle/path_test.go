package proofbundle

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func windowsUserBackslashPath() string {
	return `C:` + `\Users\` + `alice` + `\source`
}

func windowsUserSlashPath() string {
	return "C:" + "/Users/" + "alice" + "/source"
}

func windowsUserExtendedPath() string {
	return `\\?\` + `C:` + `\Users\` + `alice` + `\source`
}

func windowsUserSpacedPath() string {
	return `C:` + `\Users\` + `Alice Smith` + `\src`
}

func TestSanitizePathsRedactsForeignAbsoluteDialects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-project")
	inside := filepath.Join(root, "docs", "proof.md")
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "host-local", input: inside, want: "docs/proof.md"},
		{name: "windows-relative", input: `docs\windows.md`, want: "docs/windows.md"},
		{name: "parent", input: "../outside", want: proofExternalPath},
		{name: "posix-abs", input: "/etc/passwd", want: proofExternalPath},
		{name: "windows-drive-backslash", input: windowsUserBackslashPath(), want: proofExternalPath},
		{name: "windows-drive-slash", input: windowsUserSlashPath(), want: proofExternalPath},
		{name: "unc-backslash", input: `\\server\share\file.go`, want: proofExternalPath},
		{name: "unc-slash", input: "//server/share/file.go", want: proofExternalPath},
		{name: "extended", input: windowsUserExtendedPath(), want: proofExternalPath},
		{name: "device", input: `\\.\pipe\reconc`, want: proofExternalPath},
		{name: "drive-relative", input: `C:secret.go`, want: proofExternalPath},
		{name: "dot", input: ".", want: proofExternalPath},
		{name: "spaces", input: windowsUserSpacedPath(), want: proofExternalPath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sanitizePaths(root, []string{test.input})
			if !reflect.DeepEqual(got, []string{test.want}) {
				t.Fatalf("sanitizePaths(%q) = %#v, want %#v", test.input, got, []string{test.want})
			}
			if !portableProofPath(got[0]) {
				t.Fatalf("sanitized path is not portable: %q", got[0])
			}
		})
	}
}

func TestPortableProofPathRejectsForeignAbsoluteForms(t *testing.T) {
	valid := []string{proofExternalPath, "docs/proof.md", "docs/windows.md"}
	for _, value := range valid {
		if !portableProofPath(value) {
			t.Errorf("portableProofPath(%q) = false, want true", value)
		}
	}
	invalid := []string{
		"", ".", "..", "../outside", "/etc/passwd", windowsUserBackslashPath(),
		windowsUserSlashPath(), `\\server\share`, "//server/share/file",
		`\\?\C:\Windows`, `\\.\pipe\name`, "C:secret.go", "docs\\windows.md",
	}
	for _, value := range invalid {
		if portableProofPath(value) {
			t.Errorf("portableProofPath(%q) = true, want false", value)
		}
	}
}

func TestSanitizeTextRedactsForeignAbsoluteSpansWithoutEatingURLs(t *testing.T) {
	got := sanitizeText("", "see https://example.com/docs and "+windowsUserSlashPath()+" plus //server/share/x")
	if strings.Contains(got, "alice") || strings.Contains(got, "server/share") {
		t.Fatalf("foreign path leaked: %s", got)
	}
	if !strings.Contains(got, "https://example.com/docs") {
		t.Fatalf("URL was over-redacted: %s", got)
	}
	if !strings.Contains(got, proofExternalPath) {
		t.Fatalf("foreign path was not redacted: %s", got)
	}
}

func TestSanitizePathsIdempotentForPortableOutput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-project")
	inputs := []string{
		filepath.Join(root, "a.go"), "docs/b.go", `docs\c.go`,
		windowsUserSlashPath(), `\\server\share\x`, "/etc/passwd",
	}
	first := sanitizePaths(root, inputs)
	second := sanitizePaths(root, first)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("sanitizePaths is not idempotent: first=%#v second=%#v", first, second)
	}
}

func TestVerifyRejectsForeignAbsolutePathFields(t *testing.T) {
	bundle := validProofBundle()
	bundle.Candidate.DirtyPaths = []string{windowsUserSlashPath()}
	if err := Verify(bundle); err == nil {
		t.Fatal("bundle with foreign absolute dirty path passed verification")
	}
}

func FuzzSanitizeProofPathPortableAndIdempotent(f *testing.F) {
	root := "/tmp/private-project"
	for _, seed := range []string{
		"/etc/passwd", windowsUserBackslashPath(), windowsUserSlashPath(), `\\server\share\x`,
		"//server/share/x", `\\?\C:\Windows`, "docs/file.go", `docs\file.go`,
		"../outside", ".", "C:secret.go", "https://example.com/docs",
	} {
		f.Add(root, seed)
	}
	f.Fuzz(func(t *testing.T, root, value string) {
		got := sanitizePaths(root, []string{value})
		for _, item := range got {
			if !utf8.ValidString(item) {
				t.Fatalf("sanitized path is not valid UTF-8: %q", item)
			}
			if !portableProofPath(item) {
				t.Fatalf("sanitizePaths(%q) emitted non-portable %q", value, item)
			}
		}
		again := sanitizePaths(root, got)
		if !reflect.DeepEqual(got, again) {
			t.Fatalf("sanitizePaths is not idempotent: %#v then %#v", got, again)
		}
	})
}
