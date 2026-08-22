package proofbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
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
	for _, forbidden := range []string{root, "/etc/passwd", windowsPrivatePath, "abc123", "github_pat_"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("sanitizeText leaked %q: %s", forbidden, got)
		}
	}
	for _, expected := range []string{".", "private-project", "<external>", "token=<redacted>"} {
		if !strings.Contains(got, expected) {
			t.Errorf("sanitizeText omitted %q: %s", expected, got)
		}
	}
}

func TestSanitizeTextDoesNotRedactRepositoryBasenameTokens(t *testing.T) {
	root := filepath.Join(t.TempDir(), "go")
	input := root + "/docs/proof.md; go test ./...; docs are evidence"
	got := sanitizeText(root, input)
	for _, expected := range []string{"./docs/proof.md", "go test", "docs are evidence"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("sanitizeText(%q) omitted %q: %s", input, expected, got)
		}
	}
}

func TestSanitizeTextUsesAdversarialPrivacyCorpus(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-project")
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home", "alice"))
	t.Setenv("USER", "alice")
	corpus := []struct {
		name  string
		input string
		want  []string
		deny  []string
	}{
		{
			name:  "quoted assignments",
			input: `TOKEN = "quoted secret value" password:'single quoted secret'`,
			want:  []string{"TOKEN=<redacted>", "password=<redacted>"},
			deny:  []string{"quoted secret value", "single quoted secret"},
		},
		{
			name: "provider tokens",
			input: strings.Join([]string{
				"gh" + "p_12345678901234567890", "gl" + "pat-1234567890123456",
				"xo" + "xb-123456789012-123456789012", "np" + "m_12345678901234567890",
				"py" + "pi-12345678901234567890", "AK" + "IA1234567890ABCDEF",
			}, " "),
			want: []string{"<redacted>"},
			deny: []string{"gh" + "p_", "gl" + "pat-", "xo" + "xb-", "np" + "m_", "py" + "pi-", "AK" + "IA"},
		},
		{
			name:  "jwt and bearer",
			input: `Bearer "eyJ1234567890.abcdefghijkl.zyxwvutsrqpon"`,
			want:  []string{"Bearer <redacted>"},
			deny:  []string{"eyJ1234567890", "abcdefghijkl", "zyxwvutsrqpon"},
		},
		{
			name:  "path and identity boundaries",
			input: root + "/file.go " + root + "-sibling/file.go /etc/passwd alice alice2",
			want:  []string{"./file.go", "<external>", "<user>", "alice2"},
			deny:  []string{root, "./-sibling", "<user>2"},
		},
	}
	for _, test := range corpus {
		t.Run(test.name, func(t *testing.T) {
			got := sanitizeText(root, test.input)
			if !utf8.ValidString(got) {
				t.Fatalf("sanitized text is not valid UTF-8: %q", got)
			}
			for _, expected := range test.want {
				if !strings.Contains(got, expected) {
					t.Errorf("sanitizeText omitted %q: %s", expected, got)
				}
			}
			for _, forbidden := range test.deny {
				if strings.Contains(got, forbidden) {
					t.Errorf("sanitizeText leaked %q: %s", forbidden, got)
				}
			}
		})
	}
}

func TestSanitizeTextDoesNotSplitUTF8AtBound(t *testing.T) {
	got := sanitizeText("", strings.Repeat("ä", maxTextBytes/2+1))
	if !utf8.ValidString(got) {
		t.Fatalf("bounded sanitizer returned invalid UTF-8")
	}
	if !strings.HasSuffix(got, "...[bounded]") || len(got) > maxTextBytes+len("...[bounded]") {
		t.Fatalf("bounded sanitizer output = %q", got)
	}
}

func TestCommandIdentityNeverCommitsArguments(t *testing.T) {
	first := commandIdentity(`go test ./... --token="first secret"`)
	second := commandIdentity(`go test ./... --token="second secret"`)
	if first != "go" || second != "go" || first != second {
		t.Fatalf("command identity = %q and %q, want stable executable identity", first, second)
	}
	assignment := commandIdentity(`PRIVATE_SECRET="raw secret" go test`)
	if assignment != "<environment-prefixed-command>" {
		t.Fatalf("assignment command identity = %q", assignment)
	}
	full := sha256.Sum256([]byte(`go test ./... --token="first secret"`))
	if got := hashString(first); got == hex.EncodeToString(full[:]) {
		t.Fatal("command identity unexpectedly equals raw command hash")
	}
}

func FuzzSanitizeTextBoundedAndValid(f *testing.F) {
	f.Add("/private/repo", "token=secret /private/repo/file.go")
	f.Add("", "Bearer 'quoted value'")
	f.Add("/tmp/private-project", "private-project-sibling")
	f.Fuzz(func(t *testing.T, root, value string) {
		got := sanitizeText(root, value)
		if !utf8.ValidString(got) {
			t.Fatalf("sanitized text is not valid UTF-8")
		}
		if len(got) > maxTextBytes+len("...[bounded]") {
			t.Fatalf("sanitized text exceeds bound: %d", len(got))
		}
	})
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
