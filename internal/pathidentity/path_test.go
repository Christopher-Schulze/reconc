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
