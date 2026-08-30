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

func TestSanitizeTextRedactsShortUnixAndWindowsOperatorIdentitiesAtBoundaries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USER", "xy")
	t.Setenv("USERNAME", "ab")
	input := `unix xy and XY; windows ab and AB; proxy xy2; grab; xy_value; ab-name; C:\Users\AB\proof.json`
	got := sanitizeText("", input)
	for _, forbidden := range []string{"unix xy", "and XY", "windows ab", "and AB", `C:\Users\AB`} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitizeText leaked operator identity %q: %s", forbidden, got)
		}
	}
	for _, expected := range []string{"unix <user> and <user>", "windows <user> and <user>", "proxy xy2", "grab", "xy_value", "ab-name", "<external>"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("sanitizeText corrupted or omitted %q: %s", expected, got)
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
	if assignment != "go" {
		t.Fatalf("assignment command identity = %q", assignment)
	}
	full := sha256.Sum256([]byte(`go test ./... --token="first secret"`))
	if got := hashString(first); got == hex.EncodeToString(full[:]) {
		t.Fatal("command identity unexpectedly equals raw command hash")
	}
}

func TestCommandDescriptionUsesEffectiveShellExecutable(t *testing.T) {
	t.Setenv("USER", "fixture-user")
	deep := "go test"
	for range commandAnalysisDepth + 2 {
		deep = "echo $(" + deep + ")"
	}
	tests := []struct {
		name     string
		command  string
		summary  string
		identity string
	}{
		{name: "simple", command: "/usr/local/bin/go test ./...", summary: "go [arguments redacted]", identity: "go"},
		{name: "assignment", command: `PRIVATE_SECRET="hidden" go test`, summary: "go [arguments redacted]", identity: "go"},
		{name: "wrappers", command: `env TOKEN="hidden" sudo -u root timeout 5s /usr/local/bin/go test`, summary: "go [arguments redacted]", identity: "go"},
		{name: "quoted executable", command: `'/opt/tools/test runner' --token hidden`, summary: `'test runner' [arguments redacted]`, identity: "test runner"},
		{name: "assignment-shaped executable", command: `'PRIVATE=x'`, summary: `'PRIVATE=x'`, identity: "PRIVATE=x"},
		{name: "reserved executable", command: `'<compound command>'`, summary: `'<compound command>'`, identity: "<compound command>"},
		{name: "compound", command: `cd subdir && go test`, summary: "<compound command>", identity: "\x00proof-command-uncertain\x00compound"},
		{name: "pipeline", command: `go test | tee result.log`, summary: "<compound command>", identity: "\x00proof-command-uncertain\x00compound"},
		{name: "nested shell", command: `sh -c 'go test'`, summary: "<compound command>", identity: "\x00proof-command-uncertain\x00compound"},
		{name: "dynamic", command: `"$RUNNER" test`, summary: "<dynamic executable>", identity: "\x00proof-command-uncertain\x00dynamic_command"},
		{name: "unsupported wrapper", command: `env -S 'go test'`, summary: "<dynamic executable>", identity: "\x00proof-command-uncertain\x00dynamic_command"},
		{name: "unparsable", command: `go 'unterminated`, summary: "<unparsable command>", identity: "\x00proof-command-uncertain\x00unparsable"},
		{name: "too large", command: strings.Repeat("x", 1<<20), summary: "<command too large>", identity: "\x00proof-command-uncertain\x00too_large"},
		{name: "nesting limit", command: deep, summary: "<command nesting limit>", identity: "\x00proof-command-uncertain\x00nesting_depth"},
		{name: "ambiguous executable", command: `' test'`, summary: "<ambiguous executable>", identity: "\x00proof-command-uncertain\x00ambiguous_executable"},
		{name: "assignment only", command: `PRIVATE_SECRET="hidden"`, summary: "<no executable>", identity: "\x00proof-command-uncertain\x00no_executable"},
		{name: "empty", command: " \t\n", summary: "<empty command>", identity: "\x00proof-command-uncertain\x00empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			description := describeCommand(test.command)
			if description.summary != test.summary || description.identity != test.identity {
				t.Fatalf("describeCommand() = %#v, want summary=%q identity=%q", description, test.summary, test.identity)
			}
			if got := commandIdentity(description.summary); got != description.identity {
				t.Fatalf("summary identity = %q, want %q", got, description.identity)
			}
			if strings.Contains(description.summary, "hidden") || strings.Contains(description.identity, "hidden") {
				t.Fatalf("command description leaked an argument or assignment value: %#v", description)
			}
		})
	}
}

func TestCommandUncertaintyIdentitiesAreDomainSeparated(t *testing.T) {
	identities := []string{
		describeCommand("").identity,
		describeCommand("TOKEN=hidden").identity,
		describeCommand("go test && npm test").identity,
		describeCommand(`"$RUNNER" test`).identity,
		describeCommand("go 'unterminated").identity,
		describeCommand(`' test'`).identity,
		describeCommand("go test").identity,
		describeCommand(`'<compound command>'`).identity,
	}
	seen := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		if _, exists := seen[identity]; exists {
			t.Fatalf("command identity collision: %q", identity)
		}
		seen[identity] = struct{}{}
	}
}

func FuzzCommandDescriptionIsDeterministicBoundedAndVerifiable(f *testing.F) {
	f.Add(`env TOKEN="hidden" go test ./...`)
	f.Add(`go test && npm test`)
	f.Add(`"$RUNNER" --token hidden`)
	f.Add(`go 'unterminated`)
	f.Add(`env/`)
	f.Fuzz(func(t *testing.T, command string) {
		first := describeCommand(command)
		second := describeCommand(command)
		if first != second {
			t.Fatalf("command description is not deterministic: %#v != %#v", first, second)
		}
		if !utf8.ValidString(first.summary) || len(first.summary) > maxTextBytes+len("...[bounded]") {
			t.Fatalf("command summary is invalid or oversized: %q", first.summary)
		}
		if got := commandIdentity(first.summary); got != first.identity {
			t.Fatalf("summary identity = %q, want %q for %q", got, first.identity, first.summary)
		}
	})
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
