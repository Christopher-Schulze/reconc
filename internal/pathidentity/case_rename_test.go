package pathidentity

import (
	"os"
	"path/filepath"
	"testing"
)

// A case-only rename keeps an entry's inode, mode, size, and modification
// time but changes its inode change time, so a reused observation of the
// renamed directory or file is revalidated to its new spelling.
func TestResolveAfterCaseOnlyRenameUsesTheNewSpelling(t *testing.T) {
	root, err := ResolveExisting(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Docs", "guide.md"), []byte("guide"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "docs")); err != nil {
		t.Skip("the temporary file system is case-sensitive")
	}
	resolver := NewProspectiveResolver()
	for _, rename := range []struct{ requested, before, after string }{
		{filepath.Join(root, "docs"), filepath.Join(root, "Docs"), filepath.Join(root, "docs")},
		{filepath.Join(root, "docs", "GUIDE.md"), filepath.Join(root, "docs", "guide.md"), filepath.Join(root, "docs", "GUIDE.md")},
	} {
		got, err := resolver.Resolve(rename.requested)
		if err != nil {
			t.Fatal(err)
		}
		if got != rename.before {
			t.Fatalf("resolution of %s before the rename = %q, want %q", rename.requested, got, rename.before)
		}
		if err := os.Rename(rename.before, rename.after); err != nil {
			t.Fatal(err)
		}
		if got, err = resolver.Resolve(rename.requested); err != nil || got != rename.after {
			t.Fatalf("resolution of %s after a case-only rename = %q, %v; want %q", rename.requested, got, err, rename.after)
		}
	}
}
