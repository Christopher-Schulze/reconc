package extractor

import (
	"strings"
	"testing"
)

func TestExtractEmpty(t *testing.T) {
	if got := Extract(""); len(got) != 0 {
		t.Errorf("expected no suggestions for empty input, got %+v", got)
	}
}

func TestExtractReadOnlyPattern(t *testing.T) {
	prose := "Don't edit generated/**. It's build output."
	got := Extract(prose)
	if len(got) == 0 {
		t.Fatal("expected at least one suggestion from 'don't edit' prose")
	}
	found := false
	for _, s := range got {
		if s.Kind == "deny_write" {
			for _, p := range s.Paths {
				if p == "generated/**" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected deny_write for generated/**, got: %+v", got)
	}
}

func TestExtractSecretsPattern(t *testing.T) {
	prose := "Never commit .env files."
	got := Extract(prose)
	found := false
	for _, s := range got {
		if s.ID == "extract-no-secrets" && s.Mode == "block" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected extract-no-secrets (block mode), got: %+v", got)
	}
}

func TestExtractRunBeforeCommitPattern(t *testing.T) {
	prose := "Run `go test ./...` before committing."
	got := Extract(prose)
	found := false
	for _, s := range got {
		if s.Kind == "require_command" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected require_command, got: %+v", got)
	}
}

func TestExtractCIGreenPattern(t *testing.T) {
	prose := "CI must be green before merging."
	got := Extract(prose)
	found := false
	for _, s := range got {
		if s.Kind == "require_claim" && len(s.Claims) > 0 && s.Claims[0] == "ci-green" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ci-green require_claim, got: %+v", got)
	}
}

func TestExtractDedupes(t *testing.T) {
	prose := "Don't edit generated/**.\nDon't edit generated/**.\n"
	got := Extract(prose)
	count := 0
	for _, s := range got {
		if strings.HasPrefix(s.ID, "extract-read-only-") {
			count++
		}
	}
	if count > 1 {
		t.Errorf("expected deduplication; got %d read-only entries", count)
	}
}

func TestExtractRejectsNonPaths(t *testing.T) {
	// "don't edit this" is too vague to produce a rule.
	got := Extract("Don't edit this.")
	for _, s := range got {
		if s.Kind == "deny_write" {
			t.Errorf("should not infer rule from 'don't edit this': %+v", s)
		}
	}
}

func TestExtractCitesLine(t *testing.T) {
	prose := "line 1\nDon't edit src/main.go.\nline 3"
	got := Extract(prose)
	for _, s := range got {
		if s.Kind == "deny_write" && len(s.Evidence) > 0 {
			if !strings.Contains(s.Evidence[0], "line 2") {
				t.Errorf("expected citation to line 2, got: %s", s.Evidence[0])
			}
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"generated/**":   "generated",
		".env":           "env",
		"src/main.go":    "src-main-go",
		"Cmd with space": "cmd-with-space",
		"":               "rule",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q): got %q want %q", in, got, want)
		}
	}
}

func TestIsPlausiblePath(t *testing.T) {
	good := []string{"generated/**", ".env", "src/main.go", "docs/**", "*.md"}
	bad := []string{"this", "anything", "the code", "them"}
	for _, g := range good {
		if !isPlausiblePath(g) {
			t.Errorf("%q should be plausible", g)
		}
	}
	for _, b := range bad {
		if isPlausiblePath(b) {
			t.Errorf("%q should NOT be plausible", b)
		}
	}
}
