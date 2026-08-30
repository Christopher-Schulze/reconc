package hooks

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if action.wrapperAction != "created" || action.targetAction != "created" {
		t.Fatalf("wrapper outcome=%+v", action)
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
	if _, err := ensureWrapperTarget(repo, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(WrapperTargetPath))); !os.IsNotExist(err) {
		t.Fatalf("symlinked binary produced direct target: %v", err)
	}
}

func TestInstallReportsPostWriteWrapperSetupFailures(t *testing.T) {
	injected := errors.New("injected wrapper-target failure")
	for _, test := range []struct {
		name               string
		seedManagedWrapper bool
		configure          func(wrapperTargetOperations) wrapperTargetOperations
		wantWrapperAction  string
		wantTargetAction   string
	}{
		{
			name: "target inspection after wrapper creation",
			configure: func(operations wrapperTargetOperations) wrapperTargetOperations {
				operations.lstat = func(string) (os.FileInfo, error) { return nil, injected }
				return operations
			},
			wantWrapperAction: "created",
		},
		{
			name:               "target publication after wrapper update",
			seedManagedWrapper: true,
			configure: func(operations wrapperTargetOperations) wrapperTargetOperations {
				operations.writeArtifact = func(string, string, bool, managedArtifactSnapshot) (string, error) {
					return "", injected
				}
				return operations
			},
			wantWrapperAction: "updated",
		},
		{
			name: "target verification after publication",
			configure: func(operations wrapperTargetOperations) wrapperTargetOperations {
				operations.writeArtifact = func(path, content string, executable bool, snapshot managedArtifactSnapshot) (string, error) {
					action, err := writeGeneratedArtifact(path, content, executable, snapshot)
					if err != nil {
						return action, err
					}
					return action, injected
				}
				return operations
			},
			wantWrapperAction: "created",
			wantTargetAction:  "created",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			artifact := installHostWrapperBinary(t, repo)
			if test.seedManagedWrapper {
				wrapperPath := filepath.Join(repo, filepath.FromSlash(WrapperPath))
				if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
					t.Fatal(err)
				}
				oldManaged := "#!/bin/sh\n# Managed by Reconc. Repo-local agent hook wrapper.\nexit 0\n"
				if err := os.WriteFile(wrapperPath, []byte(oldManaged), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			operations := test.configure(wrapperTargetOperations{
				lstat:         os.Lstat,
				readSnapshot:  readManagedArtifactSnapshot,
				writeArtifact: writeGeneratedArtifact,
			})
			report, err := installWithWrapper(KindClaudeCode, repo, false, func(root string, force bool) (wrapperInstallOutcome, error) {
				return ensureWrapperWithTarget(root, force, func(root string, force bool) (string, error) {
					return ensureWrapperTargetWithOperations(root, force, operations)
				})
			})
			if !errors.Is(err, injected) {
				t.Fatalf("install error = %v", err)
			}
			if report == nil || !report.Partial || report.Action != "not-installed" ||
				report.WrapperPath != WrapperPath || report.WrapperAction != test.wantWrapperAction ||
				report.WrapperTargetAction != test.wantTargetAction {
				t.Fatalf("partial report = %+v", report)
			}
			wantTargetPath := ""
			if test.wantTargetAction != "" {
				wantTargetPath = WrapperTargetPath
			}
			if report.WrapperTargetPath != wantTargetPath {
				t.Fatalf("wrapper target path = %q, want %q", report.WrapperTargetPath, wantTargetPath)
			}
			if !strings.Contains(report.NextAction, "rerun `reconc hook install claude-code ") {
				t.Fatalf("next action = %q", report.NextAction)
			}
			wrapper, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(WrapperPath)))
			if err != nil || string(wrapper) != GenerateWrapper().Content {
				t.Fatalf("published wrapper mismatch: err=%v", err)
			}
			retry, err := Install(KindClaudeCode, repo, false)
			if err != nil {
				t.Fatalf("idempotent retry: %v", err)
			}
			if retry.WrapperAction != "unchanged" || retry.Action != "created" {
				t.Fatalf("retry report = %+v", retry)
			}
			target, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(WrapperTargetPath)))
			if err != nil || string(target) != artifact.Content {
				t.Fatalf("retry target receipt = %q, err=%v", target, err)
			}
		})
	}
}

func installHostWrapperBinary(t *testing.T, repo string) *Artifact {
	t.Helper()
	artifact, err := GenerateWrapperTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	binaryRelative := strings.TrimSuffix(artifact.Content, "\n")
	binaryPath := filepath.Join(repo, filepath.FromSlash(binaryRelative))
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return artifact
}
