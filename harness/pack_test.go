package harness

import (
	"bytes"
	"os"
	"strings"
	"sync"
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

func TestAdvancedPackCacheReturnsDetachedCopies(t *testing.T) {
	first, err := Advanced("0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	wantCapability := first.Manifest.Capabilities[0]
	wantPath := first.Manifest.Files[0].Path
	wantBody := append([]byte(nil), first.Files[0].Body...)
	first.Manifest.Capabilities[0] = "mutated"
	first.Manifest.Files[0].Path = "mutated"
	first.Files[0].File.Path = "mutated"
	first.Files[0].Body[0] ^= 1

	second, err := Advanced("0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if second.Manifest.Capabilities[0] != wantCapability || second.Manifest.Files[0].Path != wantPath ||
		second.Files[0].File.Path != wantPath || !bytes.Equal(second.Files[0].Body, wantBody) {
		t.Fatal("caller mutation escaped into the cached advanced pack")
	}
}

func TestAdvancedPackCacheSupportsConcurrentDetachedLoads(t *testing.T) {
	const workers = 16
	var wait sync.WaitGroup
	errors := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			pack, err := Advanced("0.9.0")
			if err == nil {
				pack.Files[0].Body[0] ^= 1
			}
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	pack, err := Advanced("0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := harnesspack.VerifyFile(pack.Files[0].File, pack.Files[0].Body); err != nil {
		t.Fatalf("concurrent caller mutation changed cached bytes: %v", err)
	}
}
