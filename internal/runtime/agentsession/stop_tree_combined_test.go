package agentsession

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStopDirectoryIdentityAndGenerationMatchesSeparateWalks(t *testing.T) {
	trees := map[string]func(root string) error{
		"files and directories": func(root string) error {
			if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
				return err
			}
			for _, name := range []string{"one.go", "a/two.go", "a/b/three.go"} {
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(name), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
		"symlink after files": func(root string) error {
			if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("a"), 0o644); err != nil {
				return err
			}
			return os.Symlink("a.go", filepath.Join(root, "b-link"))
		},
		"empty": func(string) error { return nil },
	}
	for name, build := range trees {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "tree")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := build(root); err != nil {
				t.Fatal(err)
			}
			var separateBytes, combinedBytes int64
			wantIdentity := stopDirectoryContentHashObserved(root, func(size int64) { separateBytes += size })
			wantGeneration, wantReliable := stopDirectoryGeneration(root)
			identity, generation, reliable := stopDirectoryIdentityAndGeneration(root, func(size int64) { combinedBytes += size })
			if identity != wantIdentity || generation != wantGeneration || reliable != wantReliable || combinedBytes != separateBytes {
				t.Fatalf("combined = (%q, %q, %v, %d), separate = (%q, %q, %v, %d)",
					identity, generation, reliable, combinedBytes, wantIdentity, wantGeneration, wantReliable, separateBytes)
			}
		})
	}
	missing := filepath.Join(t.TempDir(), "missing")
	wantIdentity := stopDirectoryContentHashObserved(missing, nil)
	wantGeneration, wantReliable := stopDirectoryGeneration(missing)
	if identity, generation, reliable := stopDirectoryIdentityAndGeneration(missing, nil); identity != wantIdentity ||
		generation != wantGeneration || reliable != wantReliable {
		t.Fatalf("missing tree combined = (%q, %q, %v), separate = (%q, %q, %v)", identity, generation, reliable, wantIdentity, wantGeneration, wantReliable)
	}
}
