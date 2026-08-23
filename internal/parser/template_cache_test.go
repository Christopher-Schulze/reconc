package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/presets"
)

func TestTemplateCacheRejectsMidCompileReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv(presets.HomeEnvVar, home)
	directory := filepath.Join(home, "templates")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "cached.yml")
	write := func(message string) {
		t.Helper()
		body := "description: cached\nkind: deny_write\npaths: ['generated/**']\nmessage: " + message + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	bundle := &ingest.SourceBundle{Sources: []policy.PolicySource{
		{Kind: policy.SourcePolicyFile, Path: "first.yml", Content: "rules:\n  - id: first\n    template: cached\n"},
		{Kind: policy.SourcePolicyFile, Path: "second.yml", Content: "rules:\n  - id: second\n    template: cached\n"},
	}}
	decodeCalls := 0
	_, err := parseRuleDocumentsWithDecoder(bundle, func(source policy.PolicySource) (*parserSourceDocument, error) {
		decodeCalls++
		if decodeCalls == 2 {
			write("second")
		}
		return decodeRuleSourceDocumentBounded(source)
	})
	if err == nil || !strings.Contains(err.Error(), "changed during policy compilation") {
		t.Fatalf("template replacement error = %v", err)
	}
}
