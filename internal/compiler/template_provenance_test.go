package compiler

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTemplateProvenancePortableAcrossHomes(t *testing.T) {
	var previous []byte
	for range 2 {
		home := t.TempDir()
		t.Setenv("RECONC_HOME", home)
		directory := filepath.Join(home, "templates")
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "portable.yml"), []byte("kind: deny_write\nmode: block\npaths: ['generated/**']\nmessage: protected\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		repo := t.TempDir()
		if err := os.WriteFile(filepath.Join(repo, ".reconc.yml"), []byte("rules:\n  - id: generated\n    template: portable\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, encoded, err := RenderRepoPolicy(repo, "test")
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte(home)) || bytes.Contains(encoded, []byte(repo)) {
			t.Fatal("portable lock exposes installation paths")
		}
		if previous != nil && !bytes.Equal(previous, encoded) {
			t.Fatal("identical template inputs produced different portable locks")
		}
		previous = encoded
	}
}
