package usercli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifyPATHReceiptIdentityRejectsAuthorityDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, owned string)
		want   string
	}{
		{
			name: "PATH precedence changed",
			mutate: func(t *testing.T, _ string) {
				t.Helper()
				shadowDirectory := t.TempDir()
				writeIdentityExecutable(t, filepath.Join(shadowDirectory, executableName()), "shadow")
				t.Setenv("PATH", shadowDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
			},
			want: "is not the receipt-owned binary",
		},
		{
			name: "binary replaced",
			mutate: func(t *testing.T, owned string) {
				t.Helper()
				writeIdentityExecutable(t, owned, "replacement")
			},
			want: "checksum changed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("RECONC_HOME", t.TempDir())
			ownedDirectory := t.TempDir()
			owned := filepath.Join(ownedDirectory, executableName())
			writeIdentityExecutable(t, owned, "owned")
			t.Setenv("PATH", ownedDirectory)
			writeIdentityReceipt(t, owned, "1.0.0")
			test.mutate(t, owned)

			if _, err := VerifyPATHReceiptIdentity(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyPATHReceiptIdentity error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyPATHReceiptIdentityRejectsMissingReceiptAndSymlinkedOwner(t *testing.T) {
	t.Run("missing receipt", func(t *testing.T) {
		t.Setenv("RECONC_HOME", t.TempDir())
		binaryDirectory := t.TempDir()
		writeIdentityExecutable(t, filepath.Join(binaryDirectory, executableName()), "unowned")
		t.Setenv("PATH", binaryDirectory)
		if _, err := VerifyPATHReceiptIdentity(); err == nil || !strings.Contains(err.Error(), "load installation receipt") {
			t.Fatalf("missing receipt error = %v", err)
		}
	})

	t.Run("symlinked receipt owner", func(t *testing.T) {
		t.Setenv("RECONC_HOME", t.TempDir())
		target := filepath.Join(t.TempDir(), executableName())
		writeIdentityExecutable(t, target, "owned")
		binaryDirectory := t.TempDir()
		owned := filepath.Join(binaryDirectory, executableName())
		if err := os.Symlink(target, owned); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Setenv("PATH", binaryDirectory)
		writeIdentityReceiptForPath(t, owned, target, "1.0.0")
		if _, err := VerifyPATHReceiptIdentity(); err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
			t.Fatalf("symlinked owner error = %v", err)
		}
	})
}

func TestVerifyReceiptIdentityAcceptsCurrentInstallAndUpgrade(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	binaryDirectory := t.TempDir()
	owned := filepath.Join(binaryDirectory, executableName())
	writeIdentityExecutable(t, owned, "version-one")
	t.Setenv("PATH", binaryDirectory)
	writeIdentityReceipt(t, owned, "1.0.0")
	first, err := VerifyPATHReceiptIdentity()
	if err != nil {
		t.Fatal(err)
	}

	writeIdentityExecutable(t, owned, "version-two")
	writeIdentityReceipt(t, owned, "2.0.0")
	upgraded, err := VerifyPATHReceiptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if first.ExecutablePath != upgraded.ExecutablePath || first.ArtifactSHA256 == upgraded.ArtifactSHA256 {
		t.Fatalf("upgrade identities: first=%+v upgraded=%+v", first, upgraded)
	}
}

func TestVerifyRunningReceiptIdentityBindsCurrentProcess(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	running, err := currentExecutable()
	if err != nil {
		t.Fatal(err)
	}
	writeIdentityReceipt(t, running, "test")
	identity, err := VerifyRunningReceiptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(identity.ExecutablePath, running) {
		t.Fatalf("running identity = %+v, want %s", identity, running)
	}
}

func writeIdentityExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeIdentityReceipt(t *testing.T, binary, version string) {
	t.Helper()
	writeIdentityReceiptForPath(t, binary, binary, version)
}

func writeIdentityReceiptForPath(t *testing.T, binary, digestSource, version string) {
	t.Helper()
	digest, err := fileSHA256(digestSource)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := NewReceipt(ReceiptInput{
		Manager: ManagerSource, Channel: ChannelSource, Version: version,
		SourceRepository: "local-source", ArtifactName: filepath.Base(binary),
		ArtifactSHA256: digest, BinaryPath: binary, GOOS: runtimeGOOS(),
		GOARCH: runtimeGOARCH(), SourceDigest: unavailableSourceDigest,
		ProvenanceState: ProvenanceSourceLocal, InstalledAt: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteReceipt(receipt); err != nil {
		t.Fatal(err)
	}
}
