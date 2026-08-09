package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHookWrapperDevBinarySkipsPlatformDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper execution is covered on POSIX hosts")
	}
	repo := t.TempDir()
	wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte(GenerateWrapper().Content), 0o755); err != nil {
		t.Fatal(err)
	}
	devBinary := filepath.Join(repo, ".build", "bin", "reconc")
	if err := os.MkdirAll(filepath.Dir(devBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(devBinary, []byte("#!/bin/sh\nprintf 'dev|%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "uname-called")
	binDir := t.TempDir()
	uname := filepath.Join(binDir, "uname")
	if err := os.WriteFile(uname, []byte("#!/bin/sh\nprintf called > \"$UNAME_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", wrapper, "codex-stop", repo)
	command.Env = append(os.Environ(), "PATH="+binDir, "UNAME_MARKER="+marker)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("wrapper: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "dev|hook runtime codex-stop") {
		t.Fatalf("development binary did not win: %s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("development hotpath must not invoke uname: %v", err)
	}
}

func TestHookWrapperDirectTargetSkipsPlatformDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper execution is covered on POSIX hosts")
	}
	repo, wrapper, marker := setupDirectTargetWrapper(t)
	command := exec.Command("sh", wrapper, "codex-stop", repo)
	command.Env = append(os.Environ(), "PATH="+fakeUnamePath(t, marker), "UNAME_MARKER="+marker)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("wrapper: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "direct|hook runtime codex-stop") {
		t.Fatalf("direct target did not win: %s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("direct target hotpath must not invoke uname: %v", err)
	}
}

func TestHookWrapperRoutesWorkerSentinelWithoutRuntimeEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper execution is covered on POSIX hosts")
	}
	repo, wrapper, marker := setupDirectTargetWrapper(t)
	command := exec.Command("sh", wrapper, "__worker_v1__", repo)
	command.Env = append(os.Environ(), "PATH="+fakeUnamePath(t, marker), "UNAME_MARKER="+marker)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("worker wrapper: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "direct|hook worker") {
		t.Fatalf("worker sentinel entered ordinary runtime dispatch: %s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("worker direct target must not invoke uname: %v", err)
	}
}

func TestHookWrapperTamperedTargetFallsBackToPlatformResolver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper execution is covered on POSIX hosts")
	}
	repo, wrapper, marker := setupDirectTargetWrapper(t)
	targetPath := filepath.Join(repo, filepath.FromSlash(WrapperTargetPath))
	if err := os.WriteFile(targetPath, []byte("../../foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", wrapper, "codex-stop", repo)
	command.Env = append(os.Environ(), "PATH="+fakeUnamePath(t, marker), "UNAME_MARKER="+marker)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("wrapper recovery: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "direct|hook runtime codex-stop") {
		t.Fatalf("recovery resolver did not find stable binary: %s", output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("tampered receipt did not enter recovery resolver: %v", err)
	}
}

func TestHookWrapperInvalidDirectTargetFallsBackToPlatformResolver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper execution is covered on POSIX hosts")
	}
	tests := []struct {
		name   string
		mutate func(t *testing.T, repo string, artifact *Artifact)
	}{
		{
			name: "missing target",
			mutate: func(t *testing.T, repo string, artifact *Artifact) {
				t.Helper()
				installVersionedRecoveryBinary(t, repo, artifact)
				if err := os.Remove(wrapperTargetBinaryPath(repo, artifact)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "non-executable target",
			mutate: func(t *testing.T, repo string, artifact *Artifact) {
				t.Helper()
				installVersionedRecoveryBinary(t, repo, artifact)
				if err := os.Chmod(wrapperTargetBinaryPath(repo, artifact), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlinked receipt",
			mutate: func(t *testing.T, repo string, artifact *Artifact) {
				t.Helper()
				targetPath := filepath.Join(repo, filepath.FromSlash(WrapperTargetPath))
				if err := os.Remove(targetPath); err != nil {
					t.Fatal(err)
				}
				foreign := filepath.Join(t.TempDir(), "hook-target")
				if err := os.WriteFile(foreign, []byte(artifact.Content), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(foreign, targetPath); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, wrapper, marker := setupDirectTargetWrapper(t)
			artifact, err := GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, repo, artifact)
			command := exec.Command("sh", wrapper, "codex-stop", repo)
			command.Env = append(os.Environ(), "PATH="+fakeUnamePath(t, marker), "UNAME_MARKER="+marker)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("wrapper recovery: %v: %s", err, output)
			}
			if !strings.Contains(string(output), "recovery|hook runtime codex-stop") &&
				!strings.Contains(string(output), "direct|hook runtime codex-stop") {
				t.Fatalf("recovery resolver did not find a valid binary: %s", output)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("invalid direct target did not enter recovery resolver: %v", err)
			}
		})
	}
}

func BenchmarkHookWrapperDirectTarget(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("POSIX wrapper execution is covered on POSIX hosts")
	}
	repo, wrapper, _ := setupDirectTargetWrapper(b)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		command := exec.Command("sh", wrapper, "codex-stop", repo)
		if output, err := command.CombinedOutput(); err != nil {
			b.Fatalf("wrapper: %v: %s", err, output)
		}
	}
}

func BenchmarkHookLaunchComponents(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("POSIX wrapper execution is covered on POSIX hosts")
	}
	repo, wrapper, _ := setupDirectTargetWrapper(b)
	target, err := GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		b.Skip(err)
	}
	binary := filepath.Join(repo, filepath.FromSlash(strings.TrimSpace(target.Content)))
	b.Run("shell-start", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			if err := exec.Command("sh", "-c", ":").Run(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("binary-start", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			if err := exec.Command(binary).Run(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("direct-target", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			if output, err := exec.Command("sh", wrapper, "codex-stop", repo).CombinedOutput(); err != nil {
				b.Fatalf("direct wrapper: %v: %s", err, output)
			}
		}
	})
	b.Run("recovery-resolver", func(b *testing.B) {
		if err := os.Remove(filepath.Join(repo, filepath.FromSlash(WrapperTargetPath))); err != nil && !os.IsNotExist(err) {
			b.Fatal(err)
		}
		for index := 0; index < b.N; index++ {
			if output, err := exec.Command("sh", wrapper, "codex-stop", repo).CombinedOutput(); err != nil {
				b.Fatalf("recovery wrapper: %v: %s", err, output)
			}
		}
	})
}

func setupDirectTargetWrapper(tb testing.TB) (string, string, string) {
	tb.Helper()
	artifact, err := GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		tb.Skip(err)
	}
	repo := tb.TempDir()
	wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte(GenerateWrapper().Content), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(artifact.TargetPath)), []byte(artifact.Content), 0o644); err != nil {
		tb.Fatal(err)
	}
	binaryPath := filepath.Join(repo, filepath.FromSlash(strings.TrimSpace(artifact.Content)))
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nprintf 'direct|%s\\n' \"$*\"\n"), 0o755); err != nil {
		tb.Fatal(err)
	}
	return repo, wrapper, filepath.Join(tb.TempDir(), "uname-called")
}

func wrapperTargetBinaryPath(repo string, artifact *Artifact) string {
	return filepath.Join(repo, filepath.FromSlash(strings.TrimSpace(artifact.Content)))
}

func installVersionedRecoveryBinary(t *testing.T, repo string, artifact *Artifact) {
	t.Helper()
	directory := filepath.Dir(wrapperTargetBinaryPath(repo, artifact))
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	name := "reconc-v0.9.5-" + runtime.GOOS + "-" + runtime.GOARCH + extension
	body := []byte("#!/bin/sh\nprintf 'recovery|%s\\n' \"$*\"\n")
	if err := os.WriteFile(filepath.Join(directory, name), body, 0o755); err != nil {
		t.Fatal(err)
	}
}

func fakeUnamePath(tb testing.TB, marker string) string {
	tb.Helper()
	unameValue := ""
	switch runtime.GOOS {
	case "darwin":
		unameValue = "Darwin " + runtime.GOARCH
	case "linux":
		unameValue = "Linux " + runtime.GOARCH
	default:
		tb.Skip("unsupported POSIX wrapper host")
	}
	binDir := tb.TempDir()
	body := "#!/bin/sh\nprintf called > \"$UNAME_MARKER\"\nprintf '%s\\n' '" + unameValue + "'\n"
	if err := os.WriteFile(filepath.Join(binDir, "uname"), []byte(body), 0o755); err != nil {
		tb.Fatal(err)
	}
	return binDir
}
