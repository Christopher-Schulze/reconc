package usercli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStrictReleaseAssetDiscoveryRejectsAmbiguousInventory(t *testing.T) {
	const version = "1.2.3"
	tag := "reconc-v" + version
	target := targetArtifact(version)
	asset := func(name string, size int64) githubAsset {
		return githubAsset{
			Name:               name,
			Size:               size,
			BrowserDownloadURL: releaseDownloadBase + "/" + tag + "/" + name,
		}
	}
	valid := githubRelease{
		TagName: tag,
		Assets: []githubAsset{
			asset(target, 7),
			asset(releaseManifestName, 11),
			asset(releaseChecksumsName, 13),
		},
	}
	selected, manifest, checksums, err := githubAssets(valid, target)
	if err != nil || selected.Name != target || manifest.Name != releaseManifestName || checksums.Name != releaseChecksumsName {
		t.Fatalf("valid strict asset discovery = target=%+v manifest=%+v checksums=%+v err=%v", selected, manifest, checksums, err)
	}

	tooMany := valid
	tooMany.Assets = make([]githubAsset, maxReleaseAssets+1)
	if _, _, _, err := githubAssets(tooMany, target); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("oversized release inventory error = %v", err)
	}
	duplicate := valid
	duplicate.Assets = append(append([]githubAsset(nil), valid.Assets...), valid.Assets[0])
	if _, _, _, err := githubAssets(duplicate, target); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate release asset error = %v", err)
	}
	mutable := valid
	mutable.Assets = append([]githubAsset(nil), valid.Assets...)
	mutable.Assets[0].BrowserDownloadURL = "https://example.test/latest"
	if _, _, _, err := githubAssets(mutable, target); err == nil || !strings.Contains(err.Error(), "noncanonical") {
		t.Fatalf("mutable release URL error = %v", err)
	}
	missing := valid
	missing.Assets = valid.Assets[:2]
	if _, _, _, err := githubAssets(missing, target); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("incomplete release inventory error = %v", err)
	}
}

func TestStrictReleaseManifestAndChecksumInventoryStayBoundTogether(t *testing.T) {
	digest := strings.Repeat("a", 64)
	manifest := ReleaseManifest{
		FormatVersion: releaseManifestFormat,
		Repository:    releaseRepository,
		Tag:           "reconc-v1.2.3",
		Version:       "1.2.3",
		Assets:        []ReleaseAsset{{Name: "reconc-test", SHA256: digest, Size: 7}},
	}
	manifestBody, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReleaseManifest(manifestBody)
	if err != nil || decoded.Version != manifest.Version {
		t.Fatalf("decode valid strict manifest = %+v, %v", decoded, err)
	}
	if _, err := decodeReleaseManifest(append(manifestBody, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing release-manifest document passed")
	}
	unknown := append([]byte(nil), manifestBody[:len(manifestBody)-1]...)
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	if _, err := decodeReleaseManifest(unknown); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown release-manifest field error = %v", err)
	}

	manifestSum := sha256.Sum256(manifestBody)
	checksums := []byte(digest + "  reconc-test\n" +
		hex.EncodeToString(manifestSum[:]) + "  " + releaseManifestName + "\n")
	if err := validateChecksumInventory(manifest, manifestBody, checksums); err != nil {
		t.Fatal(err)
	}
	if err := validateChecksumInventory(manifest, manifestBody, []byte(digest+"  reconc-test\n")); err == nil ||
		!strings.Contains(err.Error(), "inventory") {
		t.Fatalf("incomplete checksum inventory error = %v", err)
	}
	wrong := []byte(strings.Repeat("b", 64) + "  reconc-test\n" +
		hex.EncodeToString(manifestSum[:]) + "  " + releaseManifestName + "\n")
	if err := validateChecksumInventory(manifest, manifestBody, wrong); err == nil ||
		!strings.Contains(err.Error(), "disagrees") {
		t.Fatalf("checksum disagreement error = %v", err)
	}

	parsed, err := parseChecksumManifest(checksums)
	if err != nil || len(parsed) != 2 {
		t.Fatalf("parsed checksum inventory = %v, %v", parsed, err)
	}
	for _, body := range [][]byte{
		[]byte("broken\n"),
		[]byte(digest + "  ../escape\n"),
		[]byte(digest + "  duplicate\n" + digest + "  duplicate\n"),
	} {
		if _, err := parseChecksumManifest(body); err == nil {
			t.Fatalf("unsafe checksum inventory passed: %q", body)
		}
	}
	if asset, ok := releaseAssetByName(manifest, "reconc-test"); !ok || asset.SHA256 != digest {
		t.Fatalf("manifest asset lookup = %+v, %v", asset, ok)
	}
	if _, ok := releaseAssetByName(manifest, "missing"); ok {
		t.Fatal("missing manifest asset lookup passed")
	}
}

func TestOnlineReleaseInventoryMatchesManifestExactly(t *testing.T) {
	manifest := ReleaseManifest{
		Assets: []ReleaseAsset{{Name: "reconc-test", SHA256: strings.Repeat("a", 64), Size: 7}},
	}
	valid := githubRelease{Assets: []githubAsset{
		{Name: "reconc-test", Size: 7},
		{Name: releaseManifestName, Size: 11},
		{Name: releaseChecksumsName, Size: 13},
	}}
	if err := validateOnlineInventory(valid, manifest); err != nil {
		t.Fatal(err)
	}
	extra := valid
	extra.Assets = append(append([]githubAsset(nil), valid.Assets...), githubAsset{Name: "extra", Size: 1})
	if err := validateOnlineInventory(extra, manifest); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("extra online inventory error = %v", err)
	}
	unmanifested := valid
	unmanifested.Assets = append([]githubAsset(nil), valid.Assets...)
	unmanifested.Assets[0].Name = "other"
	if err := validateOnlineInventory(unmanifested, manifest); err == nil || !strings.Contains(err.Error(), "unmanifested") {
		t.Fatalf("unmanifested online asset error = %v", err)
	}
	wrongSize := valid
	wrongSize.Assets = append([]githubAsset(nil), valid.Assets...)
	wrongSize.Assets[0].Size = 8
	if err := validateOnlineInventory(wrongSize, manifest); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("online asset size error = %v", err)
	}
}

func TestLocalReleaseInventoryRejectsFilesystemAndDigestDrift(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		directory, manifest, checksums := localInventoryFixture(t)
		if err := validateLocalInventory(directory, manifest, checksums); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing asset", func(t *testing.T) {
		directory, manifest, checksums := localInventoryFixture(t)
		if err := os.Remove(filepath.Join(directory, manifest.Assets[0].Name)); err != nil {
			t.Fatal(err)
		}
		if err := validateLocalInventory(directory, manifest, checksums); err == nil || !strings.Contains(err.Error(), "missing or irregular") {
			t.Fatalf("missing local asset error = %v", err)
		}
	})
	t.Run("size mismatch", func(t *testing.T) {
		directory, manifest, checksums := localInventoryFixture(t)
		manifest.Assets[0].Size++
		if err := validateLocalInventory(directory, manifest, checksums); err == nil || !strings.Contains(err.Error(), "size mismatch") {
			t.Fatalf("local asset size error = %v", err)
		}
	})
	t.Run("checksum inventory mismatch", func(t *testing.T) {
		directory, manifest, _ := localInventoryFixture(t)
		checksums := []byte(strings.Repeat("b", 64) + "  " + manifest.Assets[0].Name + "\n")
		if err := validateLocalInventory(directory, manifest, checksums); err == nil || !strings.Contains(err.Error(), "checksum identity") {
			t.Fatalf("local checksum identity error = %v", err)
		}
	})
	t.Run("asset content mismatch", func(t *testing.T) {
		directory, manifest, _ := localInventoryFixture(t)
		manifest.Assets[0].SHA256 = strings.Repeat("b", 64)
		checksums := []byte(manifest.Assets[0].SHA256 + "  " + manifest.Assets[0].Name + "\n")
		if err := validateLocalInventory(directory, manifest, checksums); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("local asset content error = %v", err)
		}
	})
	t.Run("unexpected entry", func(t *testing.T) {
		directory, manifest, checksums := localInventoryFixture(t)
		if err := os.WriteFile(filepath.Join(directory, "unexpected"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateLocalInventory(directory, manifest, checksums); err == nil || !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected local entry error = %v", err)
		}
	})
	t.Run("irregular entry", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows symlink creation requires optional developer privileges")
		}
		directory, manifest, checksums := localInventoryFixture(t)
		if err := os.Symlink(manifest.Assets[0].Name, filepath.Join(directory, "trusted_root.jsonl")); err != nil {
			t.Fatal(err)
		}
		if err := validateLocalInventory(directory, manifest, checksums); err == nil || !strings.Contains(err.Error(), "real regular file") {
			t.Fatalf("irregular local entry error = %v", err)
		}
	})
}

func TestReleaseTransportAndMetadataEdgesFailClosed(t *testing.T) {
	redirectCheck := lifecycleHTTPClient.CheckRedirect
	httpsRequest := &http.Request{URL: &url.URL{Scheme: "https"}}
	if err := redirectCheck(httpsRequest, nil); err != nil {
		t.Fatal(err)
	}
	if err := redirectCheck(httpsRequest, make([]*http.Request, 6)); err == nil || !strings.Contains(err.Error(), "five redirects") {
		t.Fatalf("redirect bound error = %v", err)
	}
	httpRequest := &http.Request{URL: &url.URL{Scheme: "http"}}
	if err := redirectCheck(httpRequest, nil); err == nil || !strings.Contains(err.Error(), "outside HTTPS") {
		t.Fatalf("non-HTTPS redirect error = %v", err)
	}

	if _, _, err := loadReleaseManifest(t.TempDir()); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory manifest error = %v", err)
	}
	oversized := filepath.Join(t.TempDir(), releaseManifestName)
	if err := os.WriteFile(oversized, make([]byte, maxReleaseMetadataBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadReleaseManifest(oversized); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized manifest error = %v", err)
	}
	oversizedChecksums := filepath.Join(t.TempDir(), releaseChecksumsName)
	if err := os.WriteFile(oversizedChecksums, make([]byte, maxReleaseMetadataBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReleaseMetadata(oversizedChecksums, "local SHA256SUMS"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized checksum metadata error = %v", err)
	}
	if allDigits("") {
		t.Fatal("empty semantic-version identifier reported numeric")
	}
	if _, err := versionFromTag("reconc-vinvalid"); err == nil {
		t.Fatal("invalid release-tag version passed")
	}
	if _, err := parseSemanticVersion("18446744073709551616.0.0"); err == nil || !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("semantic-version overflow error = %v", err)
	}
}

func localInventoryFixture(t *testing.T) (string, ReleaseManifest, []byte) {
	t.Helper()
	directory := t.TempDir()
	name := "reconc-test"
	body := []byte("payload")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	manifest := ReleaseManifest{
		Assets: []ReleaseAsset{{Name: name, SHA256: digest, Size: int64(len(body))}},
	}
	for path, content := range map[string][]byte{
		name:                 body,
		releaseManifestName:  []byte("{}\n"),
		releaseChecksumsName: []byte(digest + "  " + name + "\n"),
	} {
		if err := os.WriteFile(filepath.Join(directory, path), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory, manifest, []byte(digest + "  " + name + "\n")
}
