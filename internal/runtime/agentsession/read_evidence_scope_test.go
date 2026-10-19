package agentsession

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestReadEvidenceFilterResolvesTheRepositoryOnce(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resolutions := 0
	previous := resolveReadEvidenceRoot
	t.Cleanup(func() { resolveReadEvidenceRoot = previous })
	resolveReadEvidenceRoot = func(path string) (string, error) {
		resolutions++
		return previous(path)
	}
	paths := []string{"relative/doc.md", filepath.Join(filepath.Dir(repo), "outside.md")}
	for index := 0; index < 100; index++ {
		paths = append(paths, filepath.Join(repo, fmt.Sprintf("docs/file%03d.md", index)))
	}
	kept := filterRepoScopedReadPaths(repo, paths)
	if len(kept) != 101 || kept[0] != "relative/doc.md" {
		t.Fatalf("kept %d read paths, want the relative and every in-repository path: %v", len(kept), kept[:2])
	}
	for _, path := range kept {
		if path == paths[1] {
			t.Fatalf("outside read path was kept: %s", path)
		}
	}
	if resolutions != 1 {
		t.Fatalf("repository root resolved %d times, want once per batch", resolutions)
	}
}
