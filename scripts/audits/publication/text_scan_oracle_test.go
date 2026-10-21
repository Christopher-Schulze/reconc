package main

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand/v2"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// referenceAuditText is the unfiltered per-line regular-expression scan the
// prefiltered auditText must reproduce exactly, finding for finding.
func referenceAuditText(path, body string) []auditFinding {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	findings := []auditFinding{}
	privateKey := regexp.MustCompile("-----BEGIN (?:RSA |OPENSSH |EC )?" + "PRIVATE KEY-----")
	legacyTrailer := regexp.MustCompile(regexp.QuoteMeta("Claude" + "-Session:"))
	words := regexp.MustCompile(`[A-Za-z0-9]+`)
	for index, line := range lines {
		lineNumber := index + 1
		for _, word := range words.FindAllString(line, -1) {
			digest := sha256.Sum256([]byte(strings.ToLower(word)))
			if label, found := forbiddenWordDigests[hex.EncodeToString(digest[:])]; found {
				findings = append(findings, auditFinding{Rule: "content/private-name", Path: path, Line: lineNumber, Detail: "contains " + label})
			}
		}
		for _, rule := range []struct {
			id      string
			pattern *regexp.Regexp
			detail  string
		}{
			{"content/private-path", privatePathPattern, "contains an absolute user or mounted-volume path"},
			{"content/session-url", sessionURLPattern, "contains an agent session or share URL"},
			{"content/session-trailer", legacyTrailer, "contains a legacy agent-session commit trailer"},
			{"content/credential-url", credentialURLPattern, "contains credentials embedded in a URL"},
			{"content/access-token", accessTokenPattern, "contains a token-shaped credential"},
			{"content/key-assignment", keyAssignmentPattern, "contains a literal secret assignment"},
			{"content/private-key", privateKey, "contains a private-key header"},
		} {
			if rule.pattern.MatchString(line) {
				findings = append(findings, auditFinding{Rule: rule.id, Path: path, Line: lineNumber, Detail: rule.detail})
			}
		}
	}
	return findings
}

// The prefilters only skip lines no rule can match: Unicode case folding
// (long s, Kelvin sign), every case variant, and fragments near each
// pattern's boundary produce the same findings as the full scan.
func TestAuditTextMatchesTheUnfilteredScan(t *testing.T) {
	word := "fixtureprivateword"
	sum := sha256.Sum256([]byte(word))
	forbiddenWordDigests[hex.EncodeToString(sum[:])] = "fixture-name"
	forbiddenWords[sum] = "fixture-name"
	t.Cleanup(func() {
		delete(forbiddenWordDigests, hex.EncodeToString(sum[:]))
		delete(forbiddenWords, sum)
	})
	fragments := []string{
		"FixturePrivateWord", "fixtureprivatewords", "x" + word, word + "9",
		"/" + "Users/dev", "/u\u017f" + "ers/dev/x", "/HO" + "ME/dev", "/hOmE/", "/" + "Volumes/disk/a", "/VOLUME\u017f/d",
		"C:\\" + "Users\\dev", "c:\\U\u017f" + "ERS\\x", "D:\\users\\",
		"https://claude.ai/code/" + "session_ab-1", "HTTPS://CHATGPT.COM/" + "c/xy", "http://chatgpt.com/" + "codex/tasks/t1",
		"http://user:" + "pw@host", "https://a:b" + "@c", "ftp://u:p@h",
		"AKIA" + strings.Repeat("Q", 16), "ASIA" + strings.Repeat("7", 16), "ghp_" + strings.Repeat("a", 20), "ghs_" + strings.Repeat("Z", 21),
		"github_pat_" + strings.Repeat("b", 20), "sk-" + strings.Repeat("c", 20), "sk-proj-" + strings.Repeat("d", 20),
		"xai-" + strings.Repeat("e", 20), "AIza" + strings.Repeat("f", 30), "xAKIA", "gh", "sk-short",
		"OPENAI_API_" + "KEY", "openai_api_\u212aey", "ANTHROPIC_API_KEY", "xai_api_key", "AWS_SECRET_ACCESS_" + "KEY",
		"aw\u017f_\u017fecret_acce\u017f\u017f_key", "GITHUB_TOKEN", "github_to\u212aen", "= 'abcdefghijklmnop'", ":abcdefghijklmnopq",
		"-----BEGIN " + "PRIVATE KEY-----", "-----BEGIN RSA " + "PRIVATE KEY-----", "-----BEGIN EC PRIVATE KEY", "Claude" + "-Session:", "claude-session:",
		"plain", "words", "tasks/done/395-prevent-task.md", "\u00e9t\u00e9", "\u017f", "\u212a",
	}
	separators := []string{" ", "", "\t", "\"", "'", "/", ":", "=", "@", "\\", "-", "_"}
	random := rand.New(rand.NewPCG(623, 20))
	var body strings.Builder
	for line := 0; line < 6000; line++ {
		for part := random.IntN(5) + 1; part > 0; part-- {
			body.WriteString(fragments[random.IntN(len(fragments))])
			body.WriteString(separators[random.IntN(len(separators))])
		}
		if random.IntN(7) == 0 {
			body.WriteString("\r")
		}
		body.WriteString("\n")
	}
	got := auditText("oracle.txt", body.String())
	want := referenceAuditText("oracle.txt", body.String())
	if len(want) < 1000 {
		t.Fatalf("the oracle body produced only %d findings", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		for index := range min(len(got), len(want)) {
			if got[index] != want[index] {
				t.Fatalf("finding %d = %+v, want %+v (%d vs %d findings)", index, got[index], want[index], len(got), len(want))
			}
		}
		t.Fatalf("%d findings, want %d", len(got), len(want))
	}
}
