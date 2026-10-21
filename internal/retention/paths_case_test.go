package retention

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"reconc.dev/reconc/internal/pathidentity"
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
	if first != second || !filepath.IsAbs(first) {
		t.Fatalf("memoized result drifted: %q vs %q", first, second)
	}
}

// Where the filesystem identity is the stored spelling, a path that already
// is its identity needs no ancestor listing; a case variant still heals.
func TestCanonicalizePathCaseSkipsTheWalkForStoredSpellings(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("only macOS and Windows report the stored spelling as identity")
	}
	base, err := pathidentity.ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "Stored", "Spelling")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	reads := 0
	previous := readCaseDirectory
	t.Cleanup(func() { readCaseDirectory = previous })
	readCaseDirectory = func(path string, maxEntries int) ([]os.DirEntry, error) {
		reads++
		return previous(path, maxEntries)
	}
	if got := CanonicalizePathCase(repo); got != repo || reads != 0 {
		t.Fatalf("stored spelling = %q after %d ancestor listings, want %q after none", got, reads, repo)
	}
	variant := filepath.Join(base, "stored", "spelling")
	if _, err := os.Stat(variant); err != nil {
		t.Skip("case-sensitive volume")
	}
	if got := CanonicalizePathCase(variant); got != repo || reads == 0 {
		t.Fatalf("case variant = %q after %d listings, want %q through the walk", got, reads, repo)
	}
}

// Memoized spellings are confirmed against the filesystem on every hit, so a
// long-lived process follows case-only renames and later-created paths with
// one canonical spelling for every variant.
func TestCanonicalizePathCaseFollowsCaseOnlyChanges(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, "Alpha")
	renamed := filepath.Join(base, "ALPHA")
	if err := os.Mkdir(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(renamed); err != nil {
		t.Skip("case-sensitive volume")
	}
	if got := CanonicalizePathCase(old); got != old {
		t.Fatalf("before rename = %q, want %q", got, old)
	}
	if err := os.Rename(old, renamed); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{old, renamed} {
		if got := CanonicalizePathCase(spelling); got != renamed {
			t.Fatalf("after rename %q = %q, want %q", spelling, got, renamed)
		}
	}
	missing := filepath.Join(base, "later")
	if got := CanonicalizePathCase(missing); got != missing {
		t.Fatalf("missing path = %q, want unchanged", got)
	}
	created := filepath.Join(base, "LATER")
	if err := os.Mkdir(created, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CanonicalizePathCase(missing); got != created {
		t.Fatalf("after creation %q = %q, want %q", missing, got, created)
	}
	if caseSensitiveComponents(renamed) {
		t.Fatal("a component reachable through its swapped case was reported case-sensitive")
	}
}

// Off macOS and Windows a spelling is memoized only when no component is
// reachable through its swapped case; the probe must match the volume.
func TestCaseSensitiveComponentsMatchesTheVolume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Probe")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(path), "pROBE"))
	if got, want := caseSensitiveComponents(path), err != nil; got != want {
		t.Fatalf("case-sensitive components = %t, want %t (variant lookup: %v)", got, want, err)
	}
}
