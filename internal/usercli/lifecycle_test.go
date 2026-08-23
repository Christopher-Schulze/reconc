package usercli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/buildprovenance"
	"reconc.dev/reconc/internal/schema"
)

func TestSemanticVersionOrderingAndValidation(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  int
	}{
		{left: "1.0.0", right: "1.0.0", want: 0},
		{left: "1.0.0-preview.2", right: "1.0.0-preview.10", want: -1},
		{left: "1.0.0-preview.1", right: "1.0.0", want: -1},
		{left: "1.0.0", right: "1.0.0-preview.1", want: 1},
		{left: "1.0.0-preview.1", right: "1.0.0-preview.1.1", want: -1},
		{left: "1.0.0-preview.1.1", right: "1.0.0-preview.1", want: 1},
		{left: "1.0.0-alpha", right: "1.0.0-beta", want: -1},
		{left: "1.0.0-1", right: "1.0.0-alpha", want: -1},
		{left: "1.0.0-alpha", right: "1.0.0-1", want: 1},
		{left: "1.0.0-preview.18446744073709551617", right: "1.0.0-preview.18446744073709551616", want: 1},
		{left: "1.0.0-preview.18446744073709551616", right: "1.0.0-preview.18446744073709551617", want: -1},
		{left: "1.1.0", right: "1.0.9", want: 1},
		{left: "1.0.1", right: "1.0.0", want: 1},
		{left: "2.0.0", right: "1.99.99", want: 1},
	}
	for _, test := range tests {
		t.Run(test.left+"_"+test.right, func(t *testing.T) {
			got, err := compareVersionStrings(test.left, test.right)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("compareVersionStrings(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
			}
		})
	}
	for _, invalid := range []string{"v1.0.0", "1.0", "01.0.0", "1.0.0+build", "1.0.0-01"} {
		if _, err := parseSemanticVersion(invalid); err == nil {
			t.Fatalf("invalid semantic version %q passed", invalid)
		}
	}
	if _, err := compareVersionStrings("development", "1.0.0"); err == nil ||
		!strings.Contains(err.Error(), "running version") {
		t.Fatalf("invalid running version error = %v", err)
	}
	if _, err := compareVersionStrings("1.0.0", "development"); err == nil {
		t.Fatal("invalid target version passed ordering")
	}
}

func TestOnlineReleaseSelectionUsesFixedImmutableIdentity(t *testing.T) {
	previousClient := lifecycleHTTPClient
	t.Cleanup(func() { lifecycleHTTPClient = previousClient })
	asset := targetArtifact("1.2.3")
	digest := strings.Repeat("a", 64)
	manifest := ReleaseManifest{
		FormatVersion: releaseManifestFormat,
		Repository:    releaseRepository,
		Tag:           "reconc-v1.2.3",
		Version:       "1.2.3",
		Assets:        []ReleaseAsset{{Name: asset, SHA256: digest, Size: 123}},
	}
	manifestBody, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestSum := sha256.Sum256(manifestBody)
	checksumsBody := digest + "  " + asset + "\n" +
		hex.EncodeToString(manifestSum[:]) + "  " + releaseManifestName + "\n"
	releaseBody := fmt.Sprintf(`{
		"tag_name":"reconc-v1.2.3",
		"draft":false,
		"prerelease":false,
		"assets":[
			{"name":%q,"size":123,"browser_download_url":%q},
			{"name":"release-manifest.json","size":%d,"browser_download_url":%q},
			{"name":"SHA256SUMS","size":%d,"browser_download_url":%q}
		]
	}`,
		asset,
		releaseDownloadBase+"/reconc-v1.2.3/"+asset,
		len(manifestBody),
		releaseDownloadBase+"/reconc-v1.2.3/"+releaseManifestName,
		len(checksumsBody),
		releaseDownloadBase+"/reconc-v1.2.3/SHA256SUMS",
	)
	lifecycleHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch request.URL.String() {
		case releaseAPIBase + "/releases/latest":
			body = releaseBody
		case releaseDownloadBase + "/reconc-v1.2.3/" + releaseManifestName:
			body = string(manifestBody)
		case releaseDownloadBase + "/reconc-v1.2.3/SHA256SUMS":
			body = checksumsBody
		default:
			return nil, fmt.Errorf("unexpected request %s", request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	selected, err := selectOnlineRelease(context.Background(), "", ChannelStable)
	if err != nil {
		t.Fatal(err)
	}
	if selected.manifest.Repository != releaseRepository ||
		selected.manifest.Tag != "reconc-v1.2.3" ||
		selected.asset.Name != asset || selected.asset.SHA256 != digest {
		t.Fatalf("selected release = %+v", selected)
	}
}

func TestOnlineReleaseSelectionRejectsMutableAssetURL(t *testing.T) {
	previousClient := lifecycleHTTPClient
	t.Cleanup(func() { lifecycleHTTPClient = previousClient })
	asset := targetArtifact("1.2.3")
	body := fmt.Sprintf(`{
		"tag_name":"reconc-v1.2.3",
		"draft":false,
		"prerelease":false,
		"assets":[
			{"name":%q,"size":123,"browser_download_url":"https://example.test/latest"},
			{"name":"SHA256SUMS","size":90,"browser_download_url":%q}
		]
	}`, asset, releaseDownloadBase+"/reconc-v1.2.3/SHA256SUMS")
	lifecycleHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	if _, err := selectOnlineRelease(context.Background(), "", ChannelStable); err == nil ||
		!strings.Contains(err.Error(), "noncanonical") {
		t.Fatalf("mutable URL error = %v", err)
	}
}

func TestOnlineReleaseDiscoveryCoversExactPreviewAndBounds(t *testing.T) {
	previousClient := lifecycleHTTPClient
	t.Cleanup(func() { lifecycleHTTPClient = previousClient })

	t.Run("exact", func(t *testing.T) {
		lifecycleHTTPClient = jsonLifecycleClient(`{"tag_name":"reconc-v1.2.3"}`)
		release, err := discoverOnlineRelease(context.Background(), "1.2.3", ChannelExact)
		if err != nil || release.TagName != "reconc-v1.2.3" {
			t.Fatalf("exact release = %+v, %v", release, err)
		}
	})
	t.Run("preview", func(t *testing.T) {
		lifecycleHTTPClient = jsonLifecycleClient(`[
			{"tag_name":"reconc-v1.2.3-preview.0","draft":true,"prerelease":true},
			{"tag_name":"reconc-v1.2.3-preview.1","draft":false,"prerelease":true}
		]`)
		release, err := discoverOnlineRelease(context.Background(), "", ChannelPreview)
		if err != nil || release.TagName != "reconc-v1.2.3-preview.1" {
			t.Fatalf("preview release = %+v, %v", release, err)
		}
	})
	t.Run("preview absent", func(t *testing.T) {
		lifecycleHTTPClient = jsonLifecycleClient(`[{"tag_name":"reconc-v1.2.3","prerelease":false}]`)
		if _, err := discoverOnlineRelease(context.Background(), "", ChannelPreview); err == nil ||
			!strings.Contains(err.Error(), "no non-draft preview") {
			t.Fatalf("missing preview error = %v", err)
		}
	})
	t.Run("preview list bounded", func(t *testing.T) {
		releases := make([]githubRelease, maxReleaseList+1)
		body, err := json.Marshal(releases)
		if err != nil {
			t.Fatal(err)
		}
		lifecycleHTTPClient = jsonLifecycleClient(string(body))
		if _, err := discoverOnlineRelease(context.Background(), "", ChannelPreview); err == nil ||
			!strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized preview list error = %v", err)
		}
	})
	if _, err := discoverOnlineRelease(context.Background(), "", Channel("nightly")); err == nil {
		t.Fatal("unsupported discovery channel passed")
	}
}

func TestBoundedReleaseDownloadRejectsTransportAndBodyFailures(t *testing.T) {
	previousClient := lifecycleHTTPClient
	t.Cleanup(func() { lifecycleHTTPClient = previousClient })
	if _, err := downloadBounded(context.Background(), "http://example.test", 10); err == nil {
		t.Fatal("non-HTTPS release URL passed")
	}
	lifecycleHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("missing")),
			Header:     make(http.Header),
		}, nil
	})}
	if _, err := downloadBounded(context.Background(), "https://example.test", 10); err == nil ||
		!strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("HTTP status error = %v", err)
	}
	lifecycleHTTPClient = jsonLifecycleClient("too long")
	if _, err := downloadBounded(context.Background(), "https://example.test", 2); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("bounded body error = %v", err)
	}
}

func TestDirectOfflineUpdateAndUninstallLifecycle(t *testing.T) {
	if !supportedDirectTarget() {
		t.Skip("unsupported direct release target")
	}
	root := repositoryRoot(t)
	currentBinary := buildReleaseBinary(t, root, "1.0.0")
	updateBinary := buildReleaseBinary(t, root, "1.1.0")
	installDirectory := t.TempDir()
	target := filepath.Join(installDirectory, executableName())
	copyFileForTest(t, currentBinary, target, 0o755)
	home := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	t.Setenv("RECONC_ATTESTATION_TOOL", "missing-gh-for-test")
	writeDirectTestReceipt(t, target, "1.0.0")

	releaseDirectory := t.TempDir()
	manifest := writeLocalRelease(t, releaseDirectory, updateBinary, "1.1.0")
	enableSuccessfulOfflineAttestation(t, releaseDirectory, manifest.Assets[0].Name)
	check, err := CheckUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != LifecycleUpdateAvailable || check.TargetVersion == nil || *check.TargetVersion != "1.1.0" || check.Changed {
		t.Fatalf("update check = %+v", check)
	}
	if digest, err := fileSHA256(target); err != nil || digest == manifest.Assets[0].SHA256 {
		t.Fatalf("read-only check changed target: digest=%s err=%v", digest, err)
	}

	applied, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != LifecycleUpdated || !applied.Changed {
		t.Fatalf("update apply = %+v", applied)
	}
	output, err := exec.Command(target, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "reconc 1.1.0" {
		t.Fatalf("updated binary = %q err=%v", output, err)
	}
	receipt, _, err := LoadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Version != "1.1.0" || receipt.ArtifactSHA256 != manifest.Assets[0].SHA256 {
		t.Fatalf("updated receipt = %+v", receipt)
	}

	removed, err := Uninstall(context.Background(), "1.1.0", UninstallRequest{PurgeState: true})
	if err != nil {
		t.Fatal(err)
	}
	if removed.Status != LifecycleUninstalled || !removed.Changed {
		t.Fatalf("uninstall = %+v", removed)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("uninstalled target still exists: %v", err)
	}
	if _, _, err := LoadReceipt(); !os.IsNotExist(err) {
		t.Fatalf("uninstalled receipt still exists: %v", err)
	}
}

func TestSameVersionDifferentArtifactUpdatesExactInstallationByDefault(t *testing.T) {
	if !supportedDirectTarget() {
		t.Skip("unsupported direct release target")
	}
	root := repositoryRoot(t)
	currentBinary := buildReleaseBinaryWithSourceDigest(t, root, "1.0.0", strings.Repeat("b", 64))
	replacementBinary := buildReleaseBinaryWithSourceDigest(t, root, "1.0.0", strings.Repeat("c", 64))
	installDirectory := t.TempDir()
	target := filepath.Join(installDirectory, executableName())
	copyFileForTest(t, currentBinary, target, 0o755)
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	writeDirectTestReceiptForChannel(t, target, "1.0.0", ChannelExact)

	releaseDirectory := t.TempDir()
	manifest := writeLocalRelease(t, releaseDirectory, replacementBinary, "1.0.0")
	enableSuccessfulOfflineAttestation(t, releaseDirectory, manifest.Assets[0].Name)
	report, err := CheckUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != LifecycleUpdateAvailable || report.Changed {
		t.Fatalf("same-version replacement check = %+v", report)
	}
	receipt, _, err := LoadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ArtifactSHA256 == manifest.Assets[0].SHA256 {
		t.Fatal("test fixture did not produce distinct artifact identities")
	}
	applied, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != LifecycleUpdated || !applied.Changed {
		t.Fatalf("same-version replacement apply = %+v", applied)
	}
	output, err := exec.Command(target, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "reconc 1.0.0" {
		t.Fatalf("same-version replacement binary = %q err=%v", output, err)
	}
	receipt, _, err = LoadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Version != "1.0.0" || receipt.ArtifactSHA256 != manifest.Assets[0].SHA256 {
		t.Fatalf("same-version replacement receipt = %+v", receipt)
	}
	if receipt.Channel != ChannelStable {
		t.Fatalf("same-version replacement channel = %q", receipt.Channel)
	}

	writeDirectTestReceiptForChannel(t, target, "1.0.0", ChannelPreview)
	previewReport, err := CheckUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	if previewReport.Status != LifecycleRefused ||
		!strings.Contains(previewReport.NextAction, "--channel stable") {
		t.Fatalf("implicit preview channel change = %+v", previewReport)
	}
}

func TestDirectDowngradeRequiresExplicitIntent(t *testing.T) {
	if !supportedDirectTarget() {
		t.Skip("unsupported direct release target")
	}
	root := repositoryRoot(t)
	currentBinary := buildReleaseBinary(t, root, "1.1.0")
	oldBinary := buildReleaseBinary(t, root, "1.0.0")
	installDirectory := t.TempDir()
	target := filepath.Join(installDirectory, executableName())
	copyFileForTest(t, currentBinary, target, 0o755)
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	writeDirectTestReceipt(t, target, "1.1.0")
	releaseDirectory := t.TempDir()
	writeLocalRelease(t, releaseDirectory, oldBinary, "1.0.0")

	report, err := ApplyUpdate(context.Background(), "1.1.0", UpdateRequest{FromDir: releaseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != LifecycleRefused || report.Changed ||
		!strings.Contains(report.NextAction, "--allow-downgrade") {
		t.Fatalf("downgrade refusal = %+v", report)
	}
	output, err := exec.Command(target, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "reconc 1.1.0" {
		t.Fatalf("downgrade refusal changed binary: %q err=%v", output, err)
	}
}

func TestRequiredOfflineAttestationFailsWithoutChangingDirectBinary(t *testing.T) {
	if !supportedDirectTarget() {
		t.Skip("unsupported direct release target")
	}
	root := repositoryRoot(t)
	currentBinary := buildReleaseBinary(t, root, "1.0.0")
	updateBinary := buildReleaseBinary(t, root, "1.1.0")
	installDirectory := t.TempDir()
	target := filepath.Join(installDirectory, executableName())
	copyFileForTest(t, currentBinary, target, 0o755)
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	t.Setenv("RECONC_ATTESTATION_TOOL", "missing-gh-for-test")
	t.Setenv("RECONC_REQUIRE_ATTESTATION", "1")
	writeDirectTestReceipt(t, target, "1.0.0")
	before, err := fileSHA256(target)
	if err != nil {
		t.Fatal(err)
	}
	releaseDirectory := t.TempDir()
	writeLocalRelease(t, releaseDirectory, updateBinary, "1.1.0")

	report, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDirectory})
	if err != nil {
		t.Fatal(err)
	}
	after, hashErr := fileSHA256(target)
	if report.Status != LifecycleFailed || report.Changed || hashErr != nil || after != before {
		t.Fatalf("required attestation failure = %+v before=%s after=%s hashErr=%v", report, before, after, hashErr)
	}
}

func TestPurgeStateUnknownEntryFailsBeforeUninstallMutation(t *testing.T) {
	installDirectory := t.TempDir()
	home := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	installed, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(home, installationStateDirName, "foreign")
	if err := os.WriteFile(unknown, []byte("owned by user"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := Uninstall(context.Background(), "test", UninstallRequest{PurgeState: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != LifecycleRefused || report.Changed {
		t.Fatalf("unknown purge state = %+v", report)
	}
	if _, err := os.Stat(installed.Status.TargetPath); err != nil {
		t.Fatalf("purge refusal removed binary: %v", err)
	}
	if _, _, err := LoadReceipt(); err != nil {
		t.Fatalf("purge refusal removed receipt: %v", err)
	}
}

func TestLocalReleaseRejectsUnknownAndSymlinkEntries(t *testing.T) {
	if !supportedDirectTarget() {
		t.Skip("unsupported direct release target")
	}
	root := repositoryRoot(t)
	binary := buildReleaseBinary(t, root, "1.2.3")
	for _, test := range []struct {
		name string
		add  func(t *testing.T, directory string)
	}{
		{
			name: "unknown",
			add: func(t *testing.T, directory string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(directory, "unexpected"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink asset",
			add: func(t *testing.T, directory string) {
				t.Helper()
				if runtime.GOOS == "windows" {
					t.Skip("Windows symlink creation requires optional developer privileges")
				}
				asset := targetArtifact("1.2.3")
				if err := os.Remove(filepath.Join(directory, asset)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(binary, filepath.Join(directory, asset)); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeLocalRelease(t, directory, binary, "1.2.3")
			test.add(t, directory)
			if _, err := selectLocalRelease(directory, "", ChannelStable); err == nil {
				t.Fatal("malicious local release passed")
			}
		})
	}
}

func TestLifecycleReportEncodingIsOneBoundedDocument(t *testing.T) {
	report := &LifecycleReport{
		Schema: schemaURLForTest(), FormatVersion: LifecycleFormatVersion,
		Operation: "update.check", Status: LifecycleCurrent,
		CurrentVersion: "1.0.0", Checks: []DiagnosticCheck{},
		Actions: []DiagnosticAction{}, NextAction: "Already current.",
	}
	body, err := EncodeLifecycle(report)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var decoded LifecycleReport
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != LifecycleCurrent {
		t.Fatalf("decoded report = %+v", decoded)
	}
	if _, err := EncodeLifecycle(nil); err == nil {
		t.Fatal("nil lifecycle report passed")
	}
}

func TestReleaseSelectionAndValidationFailureContracts(t *testing.T) {
	if _, err := selectedChannel(UpdateRequest{Version: "1.0.0", Channel: ChannelStable}); err == nil {
		t.Fatal("combined exact version and channel passed")
	}
	if _, err := selectedChannel(UpdateRequest{Version: "v1"}); err == nil {
		t.Fatal("invalid exact version passed")
	}
	if _, err := selectedChannel(UpdateRequest{Channel: Channel("nightly")}); err == nil {
		t.Fatal("unknown channel passed")
	}
	if channel, err := selectedChannel(UpdateRequest{Version: "1.0.0"}); err != nil || channel != ChannelExact {
		t.Fatalf("exact channel = %q, %v", channel, err)
	}

	for _, test := range []struct {
		name       string
		version    string
		draft      bool
		prerelease bool
		channel    Channel
		want       string
	}{
		{name: "draft", version: "1.0.0", draft: true, channel: ChannelStable, want: "draft"},
		{name: "flag mismatch", version: "1.0.0-preview.1", channel: ChannelPreview, want: "flag"},
		{name: "stable prerelease", version: "1.0.0-preview.1", prerelease: true, channel: ChannelStable, want: "stable"},
		{name: "preview stable", version: "1.0.0", channel: ChannelPreview, want: "preview"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateReleaseClass(test.version, test.draft, test.prerelease, test.channel)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateReleaseClass error = %v", err)
			}
		})
	}

	if _, err := versionFromTag("v1.0.0"); err == nil {
		t.Fatal("foreign tag passed")
	}
	if _, err := checksumForAsset([]byte("broken\n"), "asset"); err == nil {
		t.Fatal("malformed checksum entry passed")
	}
	if _, err := checksumForAsset([]byte(strings.Repeat("a", 64)+"  other\n"), "asset"); err == nil {
		t.Fatal("missing checksum entry passed")
	}
	duplicate := strings.Repeat("a", 64) + "  asset\n" + strings.Repeat("b", 64) + "  asset\n"
	if _, err := checksumForAsset([]byte(duplicate), "asset"); err == nil {
		t.Fatal("duplicate checksum entry passed")
	}
}

func TestReleaseManifestRejectsIdentityInventoryAndOrderingDrift(t *testing.T) {
	validAsset := ReleaseAsset{Name: "asset", SHA256: strings.Repeat("a", 64), Size: 1}
	valid := ReleaseManifest{
		FormatVersion: releaseManifestFormat,
		Repository:    releaseRepository,
		Tag:           "reconc-v1.0.0",
		Version:       "1.0.0",
		Assets:        []ReleaseAsset{validAsset},
	}
	if err := validateReleaseManifest(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ReleaseManifest)
		want   string
	}{
		{name: "identity", mutate: func(manifest *ReleaseManifest) {
			manifest.Repository = "other/repository"
		}, want: "identity"},
		{name: "tag", mutate: func(manifest *ReleaseManifest) {
			manifest.Version = "1.0.1"
		}, want: "disagree"},
		{name: "empty", mutate: func(manifest *ReleaseManifest) {
			manifest.Assets = nil
		}, want: "1 to"},
		{name: "invalid asset", mutate: func(manifest *ReleaseManifest) {
			manifest.Assets[0].Name = "../asset"
		}, want: "invalid release asset"},
		{name: "duplicate", mutate: func(manifest *ReleaseManifest) {
			manifest.Assets = []ReleaseAsset{validAsset, validAsset}
		}, want: "duplicate"},
		{name: "unsorted", mutate: func(manifest *ReleaseManifest) {
			first := validAsset
			first.Name = "z"
			second := validAsset
			second.Name = "a"
			manifest.Assets = []ReleaseAsset{first, second}
		}, want: "sorted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Assets = append([]ReleaseAsset(nil), valid.Assets...)
			test.mutate(&candidate)
			err := validateReleaseManifest(candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("manifest validation error = %v", err)
			}
		})
	}
}

func TestLifecyclePureHelpersCoverOrderingAndCommands(t *testing.T) {
	assets := []ReleaseAsset{{Name: "z"}, {Name: "a"}}
	sorted := sortedReleaseAssets(assets)
	if sorted[0].Name != "a" || assets[0].Name != "z" {
		t.Fatalf("sorted assets mutated input: sorted=%+v input=%+v", sorted, assets)
	}
	command := updateCommand(UpdateRequest{
		Version: "1.0.0", FromDir: "release dir", AllowDowngrade: true,
	})
	if !strings.Contains(command, "--version 1.0.0") ||
		!strings.Contains(command, "--from-dir 'release dir'") ||
		!strings.Contains(command, "--allow-downgrade") {
		t.Fatalf("update command = %q", command)
	}
	if got := updateCommand(UpdateRequest{Channel: ChannelPreview}); got != "reconc update --channel preview" {
		t.Fatalf("preview update command = %q", got)
	}
	if got := channelForRelease(selectedRelease{channel: ChannelExact}); got != ChannelExact {
		t.Fatalf("explicit release channel = %q", got)
	}
	if got := channelForRelease(selectedRelease{manifest: ReleaseManifest{Prerelease: true}}); got != ChannelPreview {
		t.Fatalf("inferred preview channel = %q", got)
	}
	if got := channelForRelease(selectedRelease{}); got != ChannelStable {
		t.Fatalf("inferred stable channel = %q", got)
	}
}

func TestAttestationVerificationFailsClosed(t *testing.T) {
	previousCommand := lifecycleCommand
	t.Cleanup(func() { lifecycleCommand = previousCommand })
	t.Setenv("RECONC_ATTESTATION_TOOL", os.Args[0])
	release := selectedRelease{manifest: ReleaseManifest{Tag: "reconc-v1.0.0"}}

	lifecycleCommand = lifecycleHelperCommandWithOutput("0", "verified")
	state, err := verifyAttestation(context.Background(), "candidate", release)
	if err != nil || state != ProvenanceGitHubVerified {
		t.Fatalf("required successful attestation = %q, %v", state, err)
	}

	lifecycleCommand = lifecycleHelperCommandWithOutput("4", "untrusted")
	if _, err := verifyAttestation(context.Background(), "candidate", release); err == nil ||
		!strings.Contains(err.Error(), "attestation verification failed") {
		t.Fatalf("required failed attestation error = %v", err)
	}

	release.localDir = t.TempDir()
	if _, err := verifyAttestation(context.Background(), "candidate", release); err == nil ||
		!strings.Contains(err.Error(), "bundle is required") {
		t.Fatalf("missing offline bundle error = %v", err)
	}
}

func TestLifecycleHelperProcess(t *testing.T) {
	if os.Getenv("RECONC_LIFECYCLE_HELPER") != "1" {
		return
	}
	output := os.Getenv("RECONC_LIFECYCLE_HELPER_OUTPUT")
	_, _ = fmt.Fprint(os.Stdout, output)
	var exitCode int
	if _, err := fmt.Sscanf(os.Getenv("RECONC_LIFECYCLE_HELPER_EXIT"), "%d", &exitCode); err != nil {
		os.Exit(99)
	}
	os.Exit(exitCode)
}

func lifecycleHelperCommandWithOutput(exitCode string, output string) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestLifecycleHelperProcess")
		command.Env = append(os.Environ(),
			"RECONC_LIFECYCLE_HELPER=1",
			"RECONC_LIFECYCLE_HELPER_EXIT="+exitCode,
			"RECONC_LIFECYCLE_HELPER_OUTPUT="+output,
		)
		return command
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonLifecycleClient(body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func buildReleaseBinary(t *testing.T, root string, version string) string {
	return buildReleaseBinaryWithSourceDigest(t, root, version, strings.Repeat("b", 64))
}

func buildReleaseBinaryWithSourceDigest(t *testing.T, root string, version string, sourceDigest string) string {
	t.Helper()
	marker, err := buildprovenance.FormatMarker(buildprovenance.Provenance{
		Version: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		SourceDigest: sourceDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), executableName())
	command := exec.Command(
		"go", "build", "-trimpath",
		"-ldflags", "-X main.Version="+version+" -X reconc.dev/reconc/buildprovenance.BuildMarker="+marker,
		"-o", output, "./cmd/reconc",
	)
	command.Dir = root
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	if body, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build release binary: %v\n%s", err, body)
	}
	return output
}

func writeDirectTestReceipt(t *testing.T, binary string, version string) {
	writeDirectTestReceiptForChannel(t, binary, version, ChannelStable)
}

func writeDirectTestReceiptForChannel(t *testing.T, binary string, version string, channel Channel) {
	t.Helper()
	digest, err := fileSHA256(binary)
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := buildprovenance.InspectBinary(binary)
	if err != nil {
		t.Fatal(err)
	}
	tag := "reconc-v" + version
	receipt, err := NewReceipt(ReceiptInput{
		Manager: ManagerDirect, Channel: channel, Version: version,
		SourceRepository: releaseRepository, ReleaseTag: &tag,
		ArtifactName: targetArtifact(version), ArtifactSHA256: digest,
		BinaryPath: binary, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		SourceDigest: provenance.SourceDigest, ProvenanceState: ProvenanceEmbeddedVerified,
		InstalledAt: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteReceipt(receipt); err != nil {
		t.Fatal(err)
	}
}

func writeLocalRelease(t *testing.T, directory string, binary string, version string) ReleaseManifest {
	t.Helper()
	assetName := targetArtifact(version)
	target := filepath.Join(directory, assetName)
	copyFileForTest(t, binary, target, 0o755)
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	manifest := ReleaseManifest{
		FormatVersion: releaseManifestFormat, Repository: releaseRepository,
		Tag: "reconc-v" + version, Version: version, Prerelease: strings.Contains(version, "-"),
		Assets: []ReleaseAsset{{Name: assetName, SHA256: digest, Size: int64(len(body))}},
	}
	manifestBody, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	manifestBody = append(manifestBody, '\n')
	if err := os.WriteFile(filepath.Join(directory, releaseManifestName), manifestBody, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestSum := sha256.Sum256(manifestBody)
	checksums := digest + "  " + assetName + "\n" +
		hex.EncodeToString(manifestSum[:]) + "  " + releaseManifestName + "\n"
	if err := os.WriteFile(filepath.Join(directory, releaseChecksumsName), []byte(checksums), 0o600); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func enableSuccessfulOfflineAttestation(t *testing.T, directory, assetName string) {
	t.Helper()
	previousCommand := lifecycleCommand
	t.Cleanup(func() { lifecycleCommand = previousCommand })
	t.Setenv("RECONC_ATTESTATION_TOOL", os.Args[0])
	attestationCommand := lifecycleHelperCommandWithOutput("0", "verified")
	lifecycleCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == os.Args[0] && len(args) >= 2 && args[0] == "attestation" && args[1] == "verify" {
			return attestationCommand(ctx, name, args...)
		}
		return exec.CommandContext(ctx, name, args...)
	}
	if err := os.WriteFile(filepath.Join(directory, assetName+".sigstore.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "trusted_root.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyFileForTest(t *testing.T, source string, destination string, mode os.FileMode) {
	t.Helper()
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, body, mode); err != nil {
		t.Fatal(err)
	}
}

func schemaURLForTest() string {
	return schema.GlobalLifecycleURL
}
