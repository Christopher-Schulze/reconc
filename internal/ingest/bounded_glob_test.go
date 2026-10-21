package ingest

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Policy discovery reads policies/ once for both fragment patterns, and an
// include expansion reads a directory its patterns share once while keeping
// one match list per pattern.
func TestPolicyGlobsReadASharedDirectoryOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.yml", "b.yaml", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, "policies", name), []byte("rules: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reads := map[string]int{}
	original := readPolicyDirectory
	readPolicyDirectory = func(path string, maxEntries int) ([]os.DirEntry, error) {
		reads[path]++
		return original(path, maxEntries)
	}
	t.Cleanup(func() { readPolicyDirectory = original })
	discovery, err := DiscoverPolicyRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(discovery.PolicyPaths, ",") != "policies/a.yml,policies/b.yaml" {
		t.Fatalf("policy paths = %#v", discovery.PolicyPaths)
	}
	if got := reads[filepath.Join(root, "policies")]; got != 1 {
		t.Fatalf("discovery read policies/ %d times, want 1", got)
	}
	clear(reads)
	expanded, err := ExpandPolicyIncludePatternsWithContext(context.Background(), root, []string{"policies/*.yaml", "policies/*.yml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded) != 2 || len(expanded[0]) != 1 || filepath.Base(expanded[0][0]) != "b.yaml" ||
		len(expanded[1]) != 1 || filepath.Base(expanded[1][0]) != "a.yml" {
		t.Fatalf("expanded = %#v", expanded)
	}
	if got := reads[filepath.Join(root, "policies")]; got != 1 {
		t.Fatalf("expansion read policies/ %d times, want 1", got)
	}
}

func TestBoundedPolicyGlobHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := boundedPolicyGlobWithContext(ctx, t.TempDir(), "policies/*.yml"); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("canceled glob error = %v", err)
	}
}

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
