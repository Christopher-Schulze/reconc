package usercli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/buildprovenance"
	"reconc.dev/reconc/internal/schema"
)

func TestReceiptRoundTripIsStrictAndSelfDigested(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	receipt := testReceipt(t, "1.2.3", ManagerSource)

	changed, path, err := WriteReceipt(receipt)
	if err != nil || !changed {
		t.Fatalf("write receipt: changed=%t path=%s err=%v", changed, path, err)
	}
	loaded, loadedPath, err := LoadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if loadedPath != path || loaded.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("loaded receipt = %+v path=%s, want digest=%s path=%s", loaded, loadedPath, receipt.ReceiptDigest, path)
	}
	changed, _, err = WriteReceipt(receipt)
	if err != nil || changed {
		t.Fatalf("idempotent receipt write: changed=%t err=%v", changed, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("receipt mode = %s, want a regular file", info.Mode())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode = %o, want 600", info.Mode().Perm())
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	document["unknown"] = true
	unknown, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadReceipt(); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown receipt field error = %v", err)
	}
}

func TestReceiptRejectsTamperTrailingDataAndOversize(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(t *testing.T, body []byte) []byte
		want string
	}{
		{
			name: "tampered digest",
			edit: func(t *testing.T, body []byte) []byte {
				t.Helper()
				return []byte(strings.Replace(string(body), `"version": "1.2.3"`, `"version": "1.2.4"`, 1))
			},
			want: "digest mismatch",
		},
		{
			name: "trailing value",
			edit: func(t *testing.T, body []byte) []byte {
				t.Helper()
				return append(body, []byte("{}\n")...)
			},
			want: "trailing JSON value",
		},
		{
			name: "oversized",
			edit: func(t *testing.T, _ []byte) []byte {
				t.Helper()
				return []byte(strings.Repeat("x", maxInstallationReceipt+1))
			},
			want: "exceeds",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("RECONC_HOME", t.TempDir())
			receipt := testReceipt(t, "1.2.3", ManagerSource)
			_, path, err := WriteReceipt(receipt)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, test.edit(t, body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := LoadReceipt(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadReceipt error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReceiptWriterSerializesConcurrentPublication(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	const writers = 12
	receipts := make([]*Receipt, writers)
	for index := range receipts {
		receipts[index] = testReceipt(t, string(rune('a'+index)), ManagerSource)
	}
	var wait sync.WaitGroup
	errors := make(chan error, writers)
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(receipt *Receipt) {
			defer wait.Done()
			_, _, err := WriteReceipt(receipt)
			errors <- err
		}(receipts[index])
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent receipt write: %v", err)
		}
	}
	loaded, _, err := LoadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Version) != 1 || loaded.Version[0] < 'a' || loaded.Version[0] >= 'a'+writers {
		t.Fatalf("concurrent winner is invalid: %+v", loaded)
	}
}

func TestReceiptStateRejectsSymlinkAndUnwritableParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX state-directory permission and symlink contract")
	}
	t.Run("symlink", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("RECONC_HOME", home)
		target := t.TempDir()
		if err := os.Symlink(target, filepath.Join(home, installationStateDirName)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := WriteReceipt(testReceipt(t, "1.2.3", ManagerSource)); err == nil ||
			!strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlink state error = %v", err)
		}
	})
	t.Run("receipt file symlink", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("RECONC_HOME", home)
		_, path, err := WriteReceipt(testReceipt(t, "1.2.3", ManagerSource))
		if err != nil {
			t.Fatal(err)
		}
		target := path + ".target"
		if err := os.Rename(path, target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadReceipt(); err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
			t.Fatalf("symlink receipt error = %v", err)
		}
	})
	t.Run("unwritable parent", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("RECONC_HOME", home)
		if err := os.Chmod(home, 0o500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(home, 0o700)
		if _, _, err := WriteReceipt(testReceipt(t, "1.2.3", ManagerSource)); err == nil {
			t.Fatal("receipt write unexpectedly succeeded in an unwritable state parent")
		}
	})
}

func TestRemoveReceiptRejectsConcurrentOwnershipChange(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	first := testReceipt(t, "1.2.3", ManagerSource)
	second := testReceipt(t, "1.2.4", ManagerSource)
	if _, _, err := WriteReceipt(first); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteReceipt(second); err != nil {
		t.Fatal(err)
	}
	if err := RemoveReceiptIfOwned(first); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale receipt removal error = %v", err)
	}
	loaded, _, err := LoadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ReceiptDigest != second.ReceiptDigest {
		t.Fatalf("concurrent receipt was removed or replaced: %+v", loaded)
	}
}

func TestInstallWithReceiptRollsBackBinaryWhenReceiptPublicationFails(t *testing.T) {
	installDirectory := t.TempDir()
	home := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)
	target := filepath.Join(installDirectory, executableName())
	previous := []byte("previous executable")
	if err := os.WriteFile(target, previous, 0o755); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(home, installationStateDirName, receiptFileName)
	if err := os.MkdirAll(receiptPath, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test"}); err == nil ||
		!strings.Contains(err.Error(), "publish installation receipt") {
		t.Fatalf("receipt publication error = %v", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(previous) {
		t.Fatalf("binary rollback = %q, want %q", body, previous)
	}
}

func TestInstallDoesNotPublishReceiptUntilBarePathIdentityMatches(t *testing.T) {
	installDirectory := t.TempDir()
	home := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", t.TempDir())

	report, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status.Ready || report.Receipt != nil || report.ReceiptPath != "" {
		t.Fatalf("off-PATH install claimed receipt: %+v", report)
	}
	if _, _, err := LoadReceipt(); !os.IsNotExist(err) {
		t.Fatalf("off-PATH receipt load error = %v, want not-exist", err)
	}
}

func TestInstallDoesNotTreatAnIdenticalPATHCopyAsTheTarget(t *testing.T) {
	installDirectory := t.TempDir()
	shadowDirectory := t.TempDir()
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	source, err := currentExecutable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shadowDirectory, executableName()), body, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shadowDirectory)

	report, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status.Ready || report.Receipt != nil {
		t.Fatalf("identical PATH copy was accepted as the install target: %+v", report)
	}
	if _, _, err := LoadReceipt(); !os.IsNotExist(err) {
		t.Fatalf("identical shadow receipt load error = %v, want not-exist", err)
	}
}

func TestDirectReceiptRequiresMatchingEmbeddedReleaseIdentity(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "reconc")
	marker, err := buildprovenance.FormatMarker(buildprovenance.Provenance{
		Version: "1.2.3", GOOS: runtimeGOOS(), GOARCH: runtimeGOARCH(),
		SourceDigest: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("prefix"+marker+"suffix"), 0o755); err != nil {
		t.Fatal(err)
	}
	status := &Status{
		Ready: true, TargetPath: binary, ExpectedSHA256: strings.Repeat("b", 64),
	}
	input, err := directReceiptInput(
		status, "1.2.3", ChannelExact, "reconc-1.2.3-test", "reconc-v1.2.3",
		ProvenanceEmbeddedVerified, time.Unix(1, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := NewReceipt(input)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Manager != ManagerDirect || receipt.SourceDigest != strings.Repeat("a", 64) {
		t.Fatalf("direct receipt = %+v", receipt)
	}
	if _, err := directReceiptInput(
		status, "9.9.9", ChannelExact, "reconc-9.9.9-test", "reconc-v9.9.9",
		ProvenanceEmbeddedVerified, time.Unix(1, 0),
	); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched direct version error = %v", err)
	}
}

func testReceipt(t *testing.T, version string, manager Manager) *Receipt {
	t.Helper()
	releaseTag := "reconc-v" + version
	input := ReceiptInput{
		Manager: manager, Channel: ChannelExact, Version: version,
		SourceRepository: "Christopher-Schulze/reconc", ReleaseTag: &releaseTag,
		ArtifactName: "reconc-" + version, ArtifactSHA256: strings.Repeat("a", 64),
		BinaryPath: filepath.Join(t.TempDir(), "reconc"), GOOS: runtimeGOOS(),
		GOARCH: runtimeGOARCH(), SourceDigest: strings.Repeat("b", 64),
		ProvenanceState: ProvenanceEmbeddedVerified, InstalledAt: time.Unix(1, 0),
	}
	if manager == ManagerSource {
		input.Channel = ChannelSource
		input.SourceRepository = "local-source"
		input.ReleaseTag = nil
		input.ProvenanceState = ProvenanceSourceLocal
	}
	receipt, err := NewReceipt(input)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Schema != schema.InstallationReceiptURL {
		t.Fatalf("receipt schema = %s", receipt.Schema)
	}
	return receipt
}

func runtimeGOOS() string {
	return runtime.GOOS
}

func runtimeGOARCH() string {
	return runtime.GOARCH
}
