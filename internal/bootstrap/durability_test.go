package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/atomicfile"
)

func TestBootstrapArtifactDurabilityBarriers(t *testing.T) {
	t.Run("nested hardlink publication", func(t *testing.T) {
		root := t.TempDir()
		calls := installBootstrapDirectorySyncProbe(t, 0)
		artifact := desiredArtifact{
			component: "durability-test",
			path:      "one/two/owned.txt",
			mode:      0o644,
			content:   []byte("owned\n"),
		}
		record, directories, err := publishArtifact(
			root,
			artifact,
			artifact.path,
			bytesSHA256(artifact.content),
			strings.Repeat("a", 64),
		)
		defer record.close()
		defer closeCreatedDirectoryIdentities(directories)
		if err != nil {
			t.Fatal(err)
		}
		if *calls != 5 {
			t.Fatalf("directory durability barriers = %d, want 5", *calls)
		}
	})

	t.Run("exclusive copy fallback", func(t *testing.T) {
		root := t.TempDir()
		calls := installBootstrapDirectorySyncProbe(t, 0)
		artifact := desiredArtifact{
			component: "durability-test",
			path:      "owned.txt",
			mode:      0o644,
			content:   []byte("owned\n"),
		}
		hooks := publicationHooks{link: func(*os.Root, string, string) error {
			return errors.New("hardlink unsupported")
		}}
		record, directories, err := publishArtifactWithHooks(
			root,
			artifact,
			artifact.path,
			bytesSHA256(artifact.content),
			strings.Repeat("b", 64),
			hooks,
		)
		defer record.close()
		defer closeCreatedDirectoryIdentities(directories)
		if err != nil {
			t.Fatal(err)
		}
		if *calls != 3 {
			t.Fatalf("copy-fallback durability barriers = %d, want 3", *calls)
		}
	})

	for _, test := range []struct {
		name   string
		failAt int
	}{
		{name: "stage create", failAt: 1},
		{name: "target create", failAt: 2},
		{name: "stage cleanup", failAt: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			installBootstrapDirectorySyncProbe(t, test.failAt)
			artifact := desiredArtifact{
				component: "durability-test",
				path:      "owned.txt",
				mode:      0o644,
				content:   []byte("owned\n"),
			}
			record, directories, err := publishArtifact(
				root,
				artifact,
				artifact.path,
				bytesSHA256(artifact.content),
				strings.Repeat("c", 64),
			)
			defer record.close()
			defer closeCreatedDirectoryIdentities(directories)
			if err == nil || !strings.Contains(err.Error(), "injected directory sync failure") {
				t.Fatalf("fault at barrier %d: record=%+v err=%v", test.failAt, record, err)
			}
			if _, statErr := os.Lstat(filepath.Join(root, artifact.path)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("fault at barrier %d left target: %v", test.failAt, statErr)
			}
			stages, globErr := filepath.Glob(filepath.Join(root, ".owned.txt.reconc-bootstrap-*.tmp"))
			if globErr != nil || len(stages) != 0 {
				t.Fatalf("fault at barrier %d left stages: %v, %v", test.failAt, stages, globErr)
			}
		})
	}
}

func installBootstrapDirectorySyncProbe(t *testing.T, failAt int) *int {
	t.Helper()
	original := bootstrapDirectorySync
	calls := 0
	bootstrapDirectorySync = func(directory *os.Root) error {
		calls++
		if calls == failAt {
			return errors.New("injected directory sync failure")
		}
		return nil
	}
	t.Cleanup(func() { bootstrapDirectorySync = original })
	return &calls
}

func TestSyncDirectoryRejectsNilRoot(t *testing.T) {
	if err := atomicfile.SyncDirectory(nil); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("nil-root sync error = %v", err)
	}
}
