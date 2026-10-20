package runtime

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"reconc.dev/reconc/internal/pathidentity"
)

func TestRepoReadNormalizationDropsOnlyForeignAbsoluteReads(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "a.go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "escape")); err != nil {
		t.Fatal(err)
	}
	root, err := pathidentity.ResolveExisting(repo)
	if err != nil {
		t.Fatal(err)
	}
	reads := []string{
		filepath.Join(repo, "src", "a.go"),
		filepath.Join(outside, "elsewhere.go"),
		filepath.Join(repo, "escape", "linked.go"),
		"src/relative.go",
		"",
		filepath.Join(repo, "missing", "new.go"),
	}
	got, err := normalizeRepoReadPathsWithResolver(reads, root, pathidentity.NewProspectiveResolver())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"src/a.go", "src/relative.go", "missing/new.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repo read normalization = %q, want %q", got, want)
	}
	if _, err := normalizePathsWithResolver(reads, root, pathidentity.NewProspectiveResolver()); err == nil {
		t.Fatal("strict normalization accepted a foreign absolute read")
	}
	if _, err := normalizeRepoReadPathsWithResolver([]string{"../outside.go"}, root, pathidentity.NewProspectiveResolver()); err == nil {
		t.Fatal("a relative read escaping the repository must still fail")
	}
}
