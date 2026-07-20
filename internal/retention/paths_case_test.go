package retention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanonicalizePathCaseHealsSpellingVariants proves spelling variants of
// the same directory resolve to one canonical form on case-insensitive
// filesystems, and that genuinely different or missing paths are never
// rewritten.
func TestCanonicalizePathCaseHealsSpellingVariants(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "MixedCase", "RepoRoot")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("canonical input is identity", func(t *testing.T) {
		if got := CanonicalizePathCase(repo); got != repo {
			t.Fatalf("canonical spelling must stay identical: got %q want %q", got, repo)
		}
	})

	t.Run("relative input unchanged", func(t *testing.T) {
		if got := CanonicalizePathCase("relative/path"); got != "relative/path" {
			t.Fatalf("relative input must be unchanged, got %q", got)
		}
	})

	t.Run("missing path unchanged", func(t *testing.T) {
		missing := filepath.Join(base, "does-not-exist")
		if got := CanonicalizePathCase(missing); got != missing {
			t.Fatalf("missing path must be unchanged, got %q", got)
		}
	})

	variant := filepath.Join(base, "mixedcase", "reporoot")
	variantInfo, err := os.Stat(variant)
	if err != nil {
		// Case-sensitive filesystem: the variant genuinely does not exist and
		// the contract is identity, which the missing-path case already
		// proves. Nothing further can alias here.
		return
	}
	repoInfo, err := os.Stat(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(variantInfo, repoInfo) {
		t.Fatalf("filesystem reports distinct files for case variants; expected alias or ENOENT")
	}

	t.Run("variant heals to on-disk spelling", func(t *testing.T) {
		got := CanonicalizePathCase(variant)
		if got != repo {
			t.Fatalf("case variant must heal to on-disk spelling: got %q want %q", got, repo)
		}
	})

	t.Run("project buckets merge across spellings", func(t *testing.T) {
		stateRoot := t.TempDir()
		if ProjectDir(stateRoot, repo) != ProjectDir(stateRoot, variant) {
			t.Fatal("case variants of the same checkout must share one project bucket")
		}
	})

	t.Run("distinct directories keep distinct buckets", func(t *testing.T) {
		stateRoot := t.TempDir()
		other := filepath.Join(base, "OtherRepo")
		if err := os.MkdirAll(other, 0o755); err != nil {
			t.Fatal(err)
		}
		if ProjectDir(stateRoot, repo) == ProjectDir(stateRoot, other) {
			t.Fatal("different checkouts must never share a bucket")
		}
	})
}

// TestCanonicalizePathCaseCacheReturnsStableResults proves memoization never
// changes the observable result.
func TestCanonicalizePathCaseCacheReturnsStableResults(t *testing.T) {
	dir := t.TempDir()
	first := CanonicalizePathCase(dir)
	second := CanonicalizePathCase(dir)
	if first != second || !strings.HasPrefix(first, string(filepath.Separator)) {
		t.Fatalf("memoized result drifted: %q vs %q", first, second)
	}
}
