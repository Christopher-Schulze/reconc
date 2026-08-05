package hooks

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureWrapperPublishesExactHostTarget(t *testing.T) {
	artifact, err := GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	repo := t.TempDir()
	binaryPath := filepath.Join(repo, filepath.FromSlash(artifact.Content[:len(artifact.Content)-1]))
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	action, err := ensureWrapper(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if action != "created" {
		t.Fatalf("wrapper action=%q", action)
	}
	body, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(WrapperTargetPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != artifact.Content {
		t.Fatalf("target receipt=%q, want %q", body, artifact.Content)
	}
}

func TestEnsureWrapperTargetRefusesForeignReceiptWithoutForce(t *testing.T) {
	artifact, err := GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	repo := t.TempDir()
	binaryPath := filepath.Join(repo, filepath.FromSlash(artifact.Content[:len(artifact.Content)-1]))
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(repo, filepath.FromSlash(WrapperTargetPath))
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureWrapper(repo, false); err == nil {
		t.Fatal("foreign target receipt was overwritten without force")
	}
	if _, err := ensureWrapper(repo, true); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != artifact.Content {
		t.Fatalf("forced repair=%q, want %q", body, artifact.Content)
	}
}

func TestEnsureWrapperTargetRejectsSymlinkedBinary(t *testing.T) {
	artifact, err := GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	repo := t.TempDir()
	foreign := filepath.Join(t.TempDir(), "reconc")
	if err := os.WriteFile(foreign, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(repo, filepath.FromSlash(artifact.Content[:len(artifact.Content)-1]))
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, binaryPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := ensureWrapperTarget(repo, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(WrapperTargetPath))); !os.IsNotExist(err) {
		t.Fatalf("symlinked binary produced direct target: %v", err)
	}
}
