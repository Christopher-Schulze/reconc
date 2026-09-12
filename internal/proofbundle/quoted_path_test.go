package proofbundle

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSanitizeEncodedQuotedPaths(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{`/srv/Private Client/report.txt`, proofExternalPath},
		{`/srv/Private "Quoted" Client/report.txt`, proofExternalPath},
		{`C:\Private Client\`, proofExternalPath},
		{`\\server\Private Share\report.txt`, proofExternalPath},
		{`file://private-host/Private Client/report.txt`, proofExternalPath},
		{`/srv/Grüße 客户/report.txt`, proofExternalPath},
		{`./docs/Private Client/report.txt`, `./docs/Private Client/report.txt`},
		{`https://example.com/docs?q=one`, `https://example.com/docs?q=one`},
	} {
		input, want := strconv.Quote(test.path), strconv.Quote(test.want)
		for depth := 0; depth < 4; depth++ {
			t.Run(input, func(t *testing.T) {
				got := sanitizeText("", "error: "+input+"; retry")
				if got != "error: "+want+"; retry" || sanitizeText("", got) != got {
					t.Fatalf("encoded path: %q, want stable %q", got, "error: "+want+"; retry")
				}
			})
			input, want = strconv.Quote(input), strconv.Quote(want)
			input, want = input[1:len(input)-1], want[1:len(want)-1]
		}
	}
}

func TestSanitizeTextRedactsCompleteQuotedPaths(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{"drive", `failed "C:\Work\Private Client\report.txt", retry`, `failed "<external>", retry`},
		{"drive-slash", `failed 'C:/Work/Private Client/report.txt'`, `failed '<external>'`},
		{"unc", `failed "\\server\Private Share\report.txt"`, `failed "<external>"`},
		{"posix", `failed '/srv/Private Client/report.txt'`, `failed '<external>'`},
		{"unicode", `failed "/srv/Grüße 客户/report.txt"`, `failed "<external>"`},
		{"escaped-quote", `failed "/srv/Private\" Client/report.txt"; retry`, `failed "<external>"; retry`},
		{"backtick", "failed `/srv/Private Client/report.txt`; retry", "failed `<external>`; retry"},
		{"even-backslashes", `failed "C:\Private Client\\"; retry`, `failed "<external>"; retry`},
		{"other-quote", `failed "/srv/Client's Private/report.txt"; retry`, `failed "<external>"; retry`},
		{"punctuation", `failed "/srv/Private, Client/(report):draft.txt"; retry`, `failed "<external>"; retry`},
		{"unterminated", `failed "/srv/Private Client/report.txt`, `failed "<external>`},
		{"multiple", `"C:\Work\Private Client\one" and '/srv/Other Client/two'`, `"<external>" and '<external>'`},
		{"file-uri", `read "file:///srv/Private Client/report.txt"`, `read "<external>"`},
		{"file-host", `read 'file://private-host/Private Share/report.txt'`, `read '<external>'`},
		{"file-uri-unquoted", `read file:///srv/private/report.txt, retry`, `read <external>, retry`},
		{"file-drive", `read "FILE:///C:/Private Client/report.txt"`, `read "<external>"`},
		{"web-url", `see "https://example.com/docs?q=one" and 'http://example.com/a'`, `see "https://example.com/docs?q=one" and 'http://example.com/a'`},
		{"relative", `read "./docs/Private Client/report.txt"`, `read "./docs/Private Client/report.txt"`},
		{"narrative", `it's missing: "/srv/Private Client/report.txt"; retry`, `it's missing: "<external>"; retry`},
		{"encoded-unterminated", `error: \"/srv/Private Client/report.txt`, `error: \"<external>`},
		{"encoded-object", `{\"path\":\"/srv/Private Client/report.txt\",\"status\":\"missing\"}`, `{\"path\":\"<external>\",\"status\":\"missing\"}`},
		{"encoded-multiple", `\"/srv/Private Client/one\" and \"/srv/Other Client/two\"`, `\"<external>\" and \"<external>\"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sanitizeText("", test.input)
			if got != test.want {
				t.Fatalf("sanitizeText(%q) = %q, want %q", test.input, got, test.want)
			}
			if again := sanitizeText("", got); again != got {
				t.Fatalf("second sanitization changed %q to %q", got, again)
			}
		})
	}
}

func TestSanitizeQuotedPathsAfterIdentityReplacement(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "operator-home"))
	tests := []struct{ name, root, input, want string }{
		{"repository", root, `read "` + filepath.Join(root, "docs", "Private Client", "report.txt") + `"`, `read "./docs/Private Client/report.txt"`},
		{"home", "", `read "` + filepath.Join(root, "operator-home", "Private Client", "report.txt") + `"`, `read "<home><external>"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sanitizeText(test.root, test.input)
			if got != test.want || sanitizeText(test.root, got) != got {
				t.Fatalf("identity replacement path = %q, want stable %q", got, test.want)
			}
		})
	}
}

func TestProofFileURIsNeverBecomePortablePathIdentities(t *testing.T) {
	for _, input := range []string{"file:///srv/private/report.txt", "file://private-host/share/report.txt", "FILE:/srv/private/report.txt"} {
		if got := sanitizePaths("", []string{input}); len(got) != 1 || got[0] != proofExternalPath {
			t.Errorf("file URI became a path identity: %q -> %v", input, got)
		}
		bundle := validProofBundle()
		bundle.Candidate.DirtyPaths = []string{input}
		bundle.Digest = mustDigest(bundle)
		if err := Verify(bundle); err == nil || !strings.Contains(err.Error(), "non-portable path") {
			t.Errorf("verifier did not reject file URI identity %q: %v", input, err)
		}
	}
}
