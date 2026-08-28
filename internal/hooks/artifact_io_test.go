package hooks

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/atomicfile"
)

func TestManagedArtifactPublicationRejectsConcurrentEditsAndReplacement(t *testing.T) {
	t.Run("unchanged", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
			t.Fatal(err)
		}
		snapshot, err := readManagedArtifactSnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		action, err := writeGeneratedArtifact(path, "before", false, snapshot)
		if err != nil || action != "unchanged" {
			t.Fatalf("unchanged publication: action=%q err=%v", action, err)
		}
	})

	t.Run("same identity changed bytes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte("before"), 0o640); err != nil {
			t.Fatal(err)
		}
		snapshot, err := readManagedArtifactSnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("after!"), 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := writeGeneratedArtifact(path, "publish", false, snapshot); !errors.Is(err, atomicfile.ErrCurrentChanged) {
			t.Fatalf("changed bytes publication error = %v", err)
		}
		body, err := os.ReadFile(path)
		if err != nil || string(body) != "after!" {
			t.Fatalf("concurrent bytes changed: body=%q err=%v", body, err)
		}
	})

	t.Run("same bytes replaced identity", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, "config.json")
		if err := os.WriteFile(path, []byte("stable"), 0o640); err != nil {
			t.Fatal(err)
		}
		snapshot, err := readManagedArtifactSnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		replacement := filepath.Join(directory, "replacement.json")
		if err := os.WriteFile(replacement, []byte("stable"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
		if _, err := writeGeneratedArtifact(path, "publish", false, snapshot); !errors.Is(err, atomicfile.ErrCurrentChanged) {
			t.Fatalf("replacement publication error = %v", err)
		}
		body, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(body, []byte("stable")) {
			t.Fatalf("replacement bytes changed: body=%q err=%v", body, err)
		}
	})

	t.Run("missing target appeared", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		snapshot, err := readManagedArtifactSnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("user edit"), 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := writeGeneratedArtifact(path, "publish", false, snapshot); !errors.Is(err, atomicfile.ErrCurrentChanged) {
			t.Fatalf("appeared target publication error = %v", err)
		}
		body, err := os.ReadFile(path)
		if err != nil || string(body) != "user edit" {
			t.Fatalf("concurrent creation changed: body=%q err=%v", body, err)
		}
	})
}

func TestEveryInstallableKindRejectsStalePublicationSnapshot(t *testing.T) {
	for _, kind := range InstallableKinds() {
		t.Run(kind, func(t *testing.T) {
			artifact, err := Generate(kind)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "artifact")
			if err := os.WriteFile(path, []byte("authorized\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			snapshot, err := readManagedArtifactSnapshot(path)
			if err != nil {
				t.Fatal(err)
			}
			concurrent := []byte("concurrent\n")
			if err := os.WriteFile(path, concurrent, 0o644); err != nil {
				t.Fatal(err)
			}
			mode := os.FileMode(0o644)
			if artifact.Executable {
				mode = 0o755
			}
			if _, err := publishManagedArtifact(path, []byte(artifact.Content), mode, snapshot); !errors.Is(err, atomicfile.ErrCurrentChanged) {
				t.Fatalf("stale publication error = %v", err)
			}
			body, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(body, concurrent) {
				t.Fatalf("concurrent bytes changed: body=%q err=%v", body, err)
			}
		})
	}
}
