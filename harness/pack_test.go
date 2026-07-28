package harness

import (
	"os"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/harnesspack"
)

func TestAdvancedPackMatchesCanonicalManifest(t *testing.T) {
	pack, err := Advanced("0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if pack.Manifest.Name != "advanced" || pack.Manifest.Version != "1.0.0" ||
		pack.Manifest.TotalBytes <= 0 {
		t.Fatalf("advanced pack identity = %+v", pack.Manifest)
	}
	manifestBody, err := os.ReadFile("advanced-pack-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := harnesspack.DecodeManifest(manifestBody)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Manifest.Digest != manifest.Digest || len(pack.Files) != len(manifest.Files) {
		t.Fatalf("embedded pack digest %s does not match canonical manifest %s", pack.Manifest.Digest, manifest.Digest)
	}
	for _, file := range pack.Files {
		if strings.HasSuffix(file.File.Path, "/coverage.out") ||
			strings.HasSuffix(file.File.Path, "/coverage.html") {
			t.Fatalf("generated coverage artifact entered advanced pack: %s", file.File.Path)
		}
	}
}

func TestAdvancedPackRejectsIncompatibleProduct(t *testing.T) {
	if _, err := Advanced("1.0.0"); err == nil {
		t.Fatal("incompatible product loaded the advanced pack")
	}
}
