//go:build darwin

package pathidentity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A listed parent resolves like any other directory: only the parents that
// hold the requested paths are listed, never every ancestor up to the root,
// whose listings can be large shared directories such as /private/tmp.
func TestWithListedParentsListsOnlyTheRequestedParents(t *testing.T) {
	root, err := ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(directory, "one.go"), filepath.Join(directory, "two.go")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	resolver := NewProspectiveResolver()
	if err := resolver.WithListedParents(func() error {
		for _, path := range paths {
			if got, err := resolver.Resolve(path); err != nil || got != path {
				t.Errorf("resolution of %s = %q, %v", path, got, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for listed := range resolver.spellings.directories {
		if listed != root && !strings.HasPrefix(listed, root+string(filepath.Separator)) {
			t.Errorf("listed %s outside the requested tree %s", listed, root)
		}
	}
}
