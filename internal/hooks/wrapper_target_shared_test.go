package hooks

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSharedCheckoutWithdrawsTheWrapperPin(t *testing.T) {
	host, err := GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	otherOS := "linux"
	if runtime.GOOS == "linux" {
		otherOS = "darwin"
	}
	other, err := GenerateWrapperTarget(otherOS, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	for _, artifact := range []*Artifact{host, other} {
		binary := filepath.Join(repo, filepath.FromSlash(artifact.Content[:len(artifact.Content)-1]))
		if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pin := filepath.Join(repo, filepath.FromSlash(WrapperTargetPath))
	if err := os.MkdirAll(filepath.Dir(pin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pin, []byte(other.Content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureWrapper(repo, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pin); !os.IsNotExist(err) {
		t.Fatalf("a shared multi-platform checkout kept a wrapper pin: %v", err)
	}
}
