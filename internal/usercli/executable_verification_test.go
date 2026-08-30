package usercli

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func copyExecutableFixture(t *testing.T, source, target string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	_, copyErr := io.CopyBuffer(output, input, make([]byte, binaryCopyBufferBytes))
	closeOutputErr := output.Close()
	closeInputErr := input.Close()
	if copyErr != nil || closeOutputErr != nil || closeInputErr != nil {
		t.Fatalf("copy executable fixture: copy=%v close-output=%v close-input=%v", copyErr, closeOutputErr, closeInputErr)
	}
}

func TestInspectCurrentReusesTargetDigestAfterOpenedIdentityProof(t *testing.T) {
	source, err := currentExecutable()
	if err != nil {
		t.Fatal(err)
	}
	installDirectory := t.TempDir()
	pathDirectory := t.TempDir()
	target := filepath.Join(installDirectory, executableName())
	resolved := filepath.Join(pathDirectory, executableName())
	copyExecutableFixture(t, source, target)
	if err := os.Link(target, resolved); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", pathDirectory)

	contentHashCalls := 0
	contentHashBytes := int64(0)
	status, err := inspectCurrentWithOperations("", fileSHA256, func(target, resolved string) (executableTargetSnapshot, error) {
		return inspectExecutableTargetWithHooks(target, resolved, executableTargetInspectionHooks{
			contentHashed: func(_ string, bytesRead int64) {
				contentHashCalls++
				contentHashBytes += bytesRead
			},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Current || !status.Ready || !samePath(status.ResolvedPath, resolved) {
		t.Fatalf("opened-identity status = %+v", status)
	}
	if contentHashCalls != 1 || contentHashBytes != targetInfo.Size() {
		t.Fatalf("target content hashes=%d bytes=%d, want one full %d-byte hash", contentHashCalls, contentHashBytes, targetInfo.Size())
	}
}

func TestInspectCurrentRejectsTargetIdentitySwapAfterHash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit deterministic replacement of this open executable fixture")
	}
	source, err := currentExecutable()
	if err != nil {
		t.Fatal(err)
	}
	installDirectory := t.TempDir()
	target := filepath.Join(installDirectory, executableName())
	copyExecutableFixture(t, source, target)
	t.Setenv("RECONC_INSTALL_DIR", installDirectory)
	t.Setenv("PATH", installDirectory)

	var swapErr error
	status, err := inspectCurrentWithOperations("", fileSHA256, func(target, resolved string) (executableTargetSnapshot, error) {
		return inspectExecutableTargetWithHooks(target, resolved, executableTargetInspectionHooks{
			afterHash: func() {
				swapErr = os.Rename(target, target+".hashed")
				if swapErr == nil {
					swapErr = os.WriteFile(target, []byte("replacement"), 0o755)
				}
			},
		})
	})
	if swapErr != nil {
		t.Fatal(swapErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if status.Current || status.Ready || len(status.Diagnostics) == 0 || status.Diagnostics[0].Name != "target-checksum" {
		t.Fatalf("identity-swapped target status = %+v", status)
	}
}
