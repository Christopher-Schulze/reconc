package impactlab

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestImpactFileDecodersRejectSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available on Windows")
	}
	repo := makeImpactRepo(t)
	corpus := simpleCorpus(t, repo)
	corpusBody, err := MarshalCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := NewDeltaManifest(nil)
	if err != nil {
		t.Fatal(err)
	}
	manifestBody, err := MarshalDeltaManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}

	for name, fixture := range map[string]struct {
		body   []byte
		decode func(string) error
	}{
		"corpus": {
			body: corpusBody,
			decode: func(path string) error {
				_, decodeErr := DecodeCorpusFile(path)
				return decodeErr
			},
		},
		"delta manifest": {
			body: manifestBody,
			decode: func(path string) error {
				_, decodeErr := DecodeDeltaManifestFile(path)
				return decodeErr
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target.json")
			link := filepath.Join(root, "input.json")
			if err := os.WriteFile(target, fixture.body, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if err := fixture.decode(link); err == nil || !strings.Contains(err.Error(), "not a symlink") {
				t.Fatalf("symlink error = %v", err)
			}
		})
	}
}
