package pathidentity

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func listedParentsFixture(t testing.TB) (string, []string) {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"src/pkg", "src/pkg/nested", "other"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"src/pkg/a.go", "src/pkg/b.go", "src/pkg/nested/c.go", "other/Readme.md"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths := []string{
		filepath.Join(root, "src", "pkg", "a.go"),
		filepath.Join(root, "src", "pkg", "b.go"),
		filepath.Join(root, "src", "pkg", "nested"),
		filepath.Join(root, "src", "pkg", "nested", "c.go"),
		filepath.Join(root, "src", "pkg", "missing.go"),
		filepath.Join(root, "src", "pkg", "gone", "deeper.go"),
		filepath.Join(root, "other", "Readme.md"),
		filepath.Join(root, "other", "README.md"),
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(root, "src", "pkg", "a.go"), filepath.Join(root, "src", "pkg", "link.go")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, "src", "pkg"), filepath.Join(root, "alias")); err != nil {
			t.Fatal(err)
		}
		paths = append(paths,
			filepath.Join(root, "src", "pkg", "link.go"),
			filepath.Join(root, "alias", "b.go"),
			filepath.Join(root, "alias", "nested", "c.go"),
		)
	}
	return root, paths
}

// Listed parents change how often directories are inspected, never the
// identity a path resolves to.
func TestWithListedParentsResolvesLikeResolve(t *testing.T) {
	_, paths := listedParentsFixture(t)
	for _, path := range paths {
		want, wantErr := NewProspectiveResolver().Resolve(path)
		var got string
		var gotErr error
		listed := NewProspectiveResolver()
		if err := listed.WithListedParents(func() error {
			for _, sibling := range paths {
				if _, err := listed.Resolve(sibling); err != nil {
					return err
				}
			}
			got, gotErr = listed.Resolve(path)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got != want || (gotErr == nil) != (wantErr == nil) {
			t.Errorf("listed resolution of %s = %q, %v; want %q, %v", path, got, gotErr, want, wantErr)
		}
	}
}

// The trust of a listed parent ends with its call: a directory replaced
// afterwards resolves to its new identity.
func TestWithListedParentsTrustEndsWithTheCall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory symlink replacement needs POSIX symlinks")
	}
	root, _ := listedParentsFixture(t)
	resolver := NewProspectiveResolver()
	path := filepath.Join(root, "src", "pkg", "a.go")
	var before string
	if err := resolver.WithListedParents(func() error {
		var err error
		before, err = resolver.Resolve(path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "moved")
	if err := os.Rename(filepath.Join(root, "src", "pkg"), moved); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "replacement")
	if err := os.Mkdir(replacement, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replacement, "a.go"), []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacement, filepath.Join(root, "src", "pkg")); err != nil {
		t.Fatal(err)
	}
	after, err := resolver.Resolve(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := NewProspectiveResolver().Resolve(path)
	if err != nil {
		t.Fatal(err)
	}
	if after != want || after == before {
		t.Fatalf("resolution after the listed call = %q (before %q), want the replacement %q", after, before, want)
	}
}

func BenchmarkResolveRecordedReads(b *testing.B) {
	root := b.TempDir()
	var paths []string
	for directory := range 40 {
		for file := range 25 {
			path := filepath.Join(root, "src", fmt.Sprintf("module%02d", directory), fmt.Sprintf("file%02d.go", file))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("package module\n"), 0o644); err != nil {
				b.Fatal(err)
			}
			paths = append(paths, path)
		}
	}
	resolve := func(b *testing.B, resolver *ProspectiveResolver) {
		for _, path := range paths {
			if _, err := resolver.Resolve(path); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Run("per-path", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			resolve(b, NewProspectiveResolver())
		}
	})
	b.Run("listed-parents", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			resolver := NewProspectiveResolver()
			if err := resolver.WithListedParents(func() error {
				resolve(b, resolver)
				return nil
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
