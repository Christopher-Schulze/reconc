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

func TestAdvancedPackUsesBuildIdentityWithoutProductRange(t *testing.T) {
	for _, identity := range []string{"dev", "dev+0123456789ab", "dev+0123456789ab-dirty", "12.34.56"} {
		t.Run(identity, func(t *testing.T) {
			pack, err := Advanced(identity)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range pack.Files {
				if err := harnesspack.VerifyFile(file.File, file.Body); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	for _, identity := range []string{"", "unavailable", "dev+invalid", "1.0.0-01"} {
		if _, err := Advanced(identity); err == nil {
			t.Fatalf("invalid build identity %q loaded the advanced pack", identity)
		}
	}
}

func TestBundledPackRejectsDamagedArchive(t *testing.T) {
	corrupted := bytes.Clone(advancedArchive)
	corrupted[len(corrupted)/2] ^= 1
	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "empty"},
		{name: "truncated", body: advancedArchive[:len(advancedArchive)-8]},
		{name: "corrupted", body: corrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := harnesspack.LoadBundledArchive(test.body); err == nil {
				t.Fatal("damaged bundled archive was accepted")
			}
		})
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
