package pathidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProspectivePreservesMissingSuffix(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "not-created", "nested", " file.txt ")
	got, err := ResolveProspective(want)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := ResolveExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	if expected := filepath.Join(resolvedRoot, "not-created", "nested", " file.txt "); got != expected {
		t.Fatalf("resolved prospective path = %q, want %q", got, expected)
	}
}

func TestResolveProspectiveBatchMatchesIndependentResolution(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "shared", "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(root, "shared", "missing", "one.txt"),
		filepath.Join(root, "shared", "missing", "two.txt"),
		filepath.Join(root, "shared", "existing"),
		filepath.Join(root, "shared", "missing", "one.txt"),
	}
	got, err := ResolveProspectiveBatch(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(paths) {
		t.Fatalf("batch length = %d, want %d", len(got), len(paths))
	}
	for index, path := range paths {
		want, err := ResolveProspective(path)
		if err != nil {
			t.Fatalf("independent %q: %v", path, err)
		}
		if got[index] != want {
			t.Fatalf("batch[%d] = %q, independent = %q", index, got[index], want)
		}
	}
}

func TestProspectiveResolverRevalidatesReplacedAncestor(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	current := filepath.Join(root, "tree")
	if err := os.MkdirAll(filepath.Join(old, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old, current); err != nil {
		t.Fatal(err)
	}
	resolver := NewProspectiveResolver()
	first, err := resolver.Resolve(filepath.Join(current, "nested", "first.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(current, old); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(current, "replacement"), 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := resolver.Resolve(filepath.Join(current, "replacement", "second.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("resolver reused replaced ancestor identity: first=%q second=%q", first, second)
	}
	want, err := ResolveProspective(filepath.Join(current, "replacement", "second.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if second != want {
		t.Fatalf("replacement resolution = %q, want %q", second, want)
	}
}

func TestExistingAliasesResolveToOneIdentity(t *testing.T) {
	root := t.TempDir()
	aliases, err := ExistingAliases(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) == 0 {
		t.Fatal("existing path must expose at least one identity spelling")
	}
	want, err := ResolveExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range aliases {
		if _, err := os.Stat(alias); err != nil {
			t.Fatalf("alias %q does not identify an existing path: %v", alias, err)
		}
		got, err := ResolveExisting(alias)
		if err != nil {
			t.Fatalf("resolve alias %q: %v", alias, err)
		}
		if got != want {
			t.Fatalf("alias %q resolves to %q, want %q", alias, got, want)
		}
	}
}
