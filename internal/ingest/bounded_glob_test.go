package ingest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBoundedPolicyGlobReturnsDeterministicRegularMatches(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.yml", "a.yml", "ignored.txt"} {
		if err := os.WriteFile(filepath.Join(root, "policies", name), []byte("rules: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := boundedPolicyGlob(root, "policies/*.yml")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || filepath.Base(got[0]) != "a.yml" || filepath.Base(got[1]) != "b.yml" {
		t.Fatalf("bounded matches = %#v", got)
	}
}

func TestBoundedPolicyGlobMatchesEscapedSyntax(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "policies", "file[1].yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := boundedPolicyGlob(root, `policies/file\[1\].yml`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "file[1].yml" {
		t.Fatalf("escaped match = %#v", got)
	}
}

func TestBoundedPolicyGlobRejectsDirectoryAndMatchCaps(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxPolicyGlobMatches; index++ {
		name := filepath.Join(root, "policies", "rule"+strconv.Itoa(index)+".yml")
		if err := os.WriteFile(name, []byte("rules: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := boundedPolicyGlob(root, "policies/*.yml")
	if err == nil || (!strings.Contains(err.Error(), "matched entries") && !strings.Contains(err.Error(), "directory entries")) {
		t.Fatalf("match cap error = %v", err)
	}
}

func TestValidatePolicyGlobPatternsBoundsGrammar(t *testing.T) {
	if err := validatePolicyGlobPatterns([]string{"policies/*.yml"}); err != nil {
		t.Fatal(err)
	}
	if err := validatePolicyGlobPatterns([]string{strings.Repeat("x", maxPolicyGlobPatternBytes+1)}); err == nil {
		t.Fatal("oversized pattern must fail")
	}
	if err := validatePolicyGlobPatterns([]string{"policies/**/rules.yml"}); err != nil {
		t.Fatalf("double-star remains a valid literal-segment glob: %v", err)
	}
	if err := validatePolicyGlobPatterns([]string{`policies/file\[1\].yml`}); err != nil {
		t.Fatalf("escaped glob syntax must be host-independent: %v", err)
	}
	if err := validatePolicyGlobPatterns([]string{`policies/trailing\`}); err == nil {
		t.Fatal("malformed trailing glob escape must fail")
	}
}
