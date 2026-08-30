package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type hookVerificationTestTarget struct {
	file  *os.File
	write func([]byte) (int, error)
	close func() error
}

func (target *hookVerificationTestTarget) Write(body []byte) (int, error) {
	if target.write != nil {
		return target.write(body)
	}
	return target.file.Write(body)
}

func (target *hookVerificationTestTarget) Close() error {
	if target.close != nil {
		return target.close()
	}
	return target.file.Close()
}

func (target *hookVerificationTestTarget) Stat() (os.FileInfo, error) {
	return target.file.Stat()
}

func (target *hookVerificationTestTarget) Chmod(mode os.FileMode) error {
	return target.file.Chmod(mode)
}

func forcedHookVerificationCopyOps(
	open func(string, int, os.FileMode) (hookVerificationCopyTarget, error),
) hookVerificationCopyOps {
	if open == nil {
		open = func(path string, flag int, mode os.FileMode) (hookVerificationCopyTarget, error) {
			return os.OpenFile(path, flag, mode)
		}
	}
	return hookVerificationCopyOps{
		link:       func(string, string) error { return errors.New("cross-device link") },
		openTarget: open,
	}
}

func TestLinkOrCopyVerificationExecutableStreamsAndVerifiesFallback(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	target := filepath.Join(directory, "target")
	body := bytes.Repeat([]byte("reconc-streaming-verification\n"), 256*1024)
	if err := os.WriteFile(source, body, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := linkOrCopyVerificationExecutableWithOps(source, target, forcedHookVerificationCopyOps(nil)); err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sourceInfo, targetInfo) || targetInfo.Size() != sourceInfo.Size() ||
		(runtime.GOOS != "windows" && targetInfo.Mode().Perm() != 0o700) {
		t.Fatalf("streamed target identity/size/mode = same:%t size:%d mode:%04o", os.SameFile(sourceInfo, targetInfo), targetInfo.Size(), targetInfo.Mode().Perm())
	}
	targetBody, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(targetBody, body) {
		t.Fatal("streamed target content differs")
	}

	preexisting := filepath.Join(directory, "preexisting")
	if err := os.WriteFile(preexisting, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := linkOrCopyVerificationExecutableWithOps(source, preexisting, forcedHookVerificationCopyOps(nil)); err == nil || !strings.Contains(err.Error(), "create disposable") {
		t.Fatalf("exclusive target error = %v", err)
	}
	if got, err := os.ReadFile(preexisting); err != nil || string(got) != "owned" {
		t.Fatalf("preexisting target changed: %q, %v", got, err)
	}
}

func TestLinkOrCopyVerificationExecutableRejectsStreamingRacesAndIOFailures(t *testing.T) {
	tests := []struct {
		name                string
		open                func(source, target string) func(string, int, os.FileMode) (hookVerificationCopyTarget, error)
		want                string
		replacementSurvives bool
	}{
		{
			name: "short write",
			open: func(_, _ string) func(string, int, os.FileMode) (hookVerificationCopyTarget, error) {
				return func(path string, flag int, mode os.FileMode) (hookVerificationCopyTarget, error) {
					file, err := os.OpenFile(path, flag, mode)
					if err != nil {
						return nil, err
					}
					target := &hookVerificationTestTarget{file: file}
					target.write = func(body []byte) (int, error) {
						if len(body) == 0 {
							return 0, nil
						}
						return file.Write(body[:len(body)-1])
					}
					return target, nil
				}
			},
			want: "short write",
		},
		{
			name: "close failure",
			open: func(_, _ string) func(string, int, os.FileMode) (hookVerificationCopyTarget, error) {
				return func(path string, flag int, mode os.FileMode) (hookVerificationCopyTarget, error) {
					file, err := os.OpenFile(path, flag, mode)
					if err != nil {
						return nil, err
					}
					return &hookVerificationTestTarget{file: file, close: func() error {
						return errors.Join(file.Close(), errors.New("injected close failure"))
					}}, nil
				}
			},
			want: "injected close failure",
		},
		{
			name: "target content corruption",
			open: func(_, _ string) func(string, int, os.FileMode) (hookVerificationCopyTarget, error) {
				return func(path string, flag int, mode os.FileMode) (hookVerificationCopyTarget, error) {
					file, err := os.OpenFile(path, flag, mode)
					if err != nil {
						return nil, err
					}
					target := &hookVerificationTestTarget{file: file}
					target.write = func(body []byte) (int, error) {
						if len(body) > 0 {
							body[0] ^= 0xff
						}
						return file.Write(body)
					}
					return target, nil
				}
			},
			want: "checksum differs",
		},
		{
			name: "source snapshot mutation",
			open: func(source, _ string) func(string, int, os.FileMode) (hookVerificationCopyTarget, error) {
				return func(path string, flag int, mode os.FileMode) (hookVerificationCopyTarget, error) {
					file, err := os.OpenFile(path, flag, mode)
					if err != nil {
						return nil, err
					}
					return &hookVerificationTestTarget{file: file, close: func() error {
						closeErr := file.Close()
						mutationErr := os.WriteFile(source, []byte("replacement"), 0o755)
						return errors.Join(closeErr, mutationErr)
					}}, nil
				}
			},
			want: "changed while reading",
		},
		{
			name: "target identity swap",
			open: func(_, targetPath string) func(string, int, os.FileMode) (hookVerificationCopyTarget, error) {
				return func(path string, flag int, mode os.FileMode) (hookVerificationCopyTarget, error) {
					file, err := os.OpenFile(path, flag, mode)
					if err != nil {
						return nil, err
					}
					replacement := targetPath + ".replacement"
					if err := os.WriteFile(replacement, []byte("replacement"), 0o700); err != nil {
						return nil, err
					}
					return &hookVerificationTestTarget{file: file, close: func() error {
						closeErr := file.Close()
						renameErr := os.Rename(targetPath, targetPath+".original")
						if renameErr == nil {
							renameErr = os.Rename(replacement, targetPath)
						}
						return errors.Join(closeErr, renameErr)
					}}, nil
				}
			},
			want:                "changed identity",
			replacementSurvives: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			source := filepath.Join(directory, "source")
			target := filepath.Join(directory, "target")
			if err := os.WriteFile(source, bytes.Repeat([]byte("source"), 64*1024), 0o755); err != nil {
				t.Fatal(err)
			}
			operations := forcedHookVerificationCopyOps(test.open(source, target))
			err := linkOrCopyVerificationExecutableWithOps(source, target, operations)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if test.replacementSurvives {
				if body, readErr := os.ReadFile(target); readErr != nil || string(body) != "replacement" {
					t.Fatalf("replacement target was not preserved: body=%q err=%v", body, readErr)
				}
			} else if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
				t.Fatalf("failed created target survived cleanup: %v", statErr)
			}
		})
	}
}

func TestLinkOrCopyVerificationExecutableRejectsSourceIdentitySwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit deterministic replacement of this open source fixture")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	target := filepath.Join(directory, "target")
	replacement := filepath.Join(directory, "replacement")
	if err := os.WriteFile(source, bytes.Repeat([]byte("source"), 64*1024), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("replacement"), 0o755); err != nil {
		t.Fatal(err)
	}
	operations := forcedHookVerificationCopyOps(func(path string, flag int, mode os.FileMode) (hookVerificationCopyTarget, error) {
		file, err := os.OpenFile(path, flag, mode)
		if err != nil {
			return nil, err
		}
		return &hookVerificationTestTarget{file: file, close: func() error {
			closeErr := file.Close()
			renameErr := os.Rename(source, source+".original")
			if renameErr == nil {
				renameErr = os.Rename(replacement, source)
			}
			return errors.Join(closeErr, renameErr)
		}}, nil
	})
	if err := linkOrCopyVerificationExecutableWithOps(source, target, operations); err == nil || !strings.Contains(err.Error(), "changed while reading") {
		t.Fatalf("source identity swap error = %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("failed created target survived cleanup: %v", err)
	}
}

func BenchmarkLinkOrCopyVerificationExecutableStreaming(b *testing.B) {
	for _, size := range []int64{1 << 20, 32 << 20} {
		b.Run(fmt.Sprintf("size-%dMiB", size>>20), func(b *testing.B) {
			directory := b.TempDir()
			source := filepath.Join(directory, "source")
			file, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
			if err != nil {
				b.Fatal(err)
			}
			if err := file.Truncate(size); err != nil {
				_ = file.Close()
				b.Fatal(err)
			}
			if err := file.Close(); err != nil {
				b.Fatal(err)
			}
			operations := forcedHookVerificationCopyOps(nil)
			b.ReportAllocs()
			b.ReportMetric(float64(size), "source-bytes/op")
			b.ResetTimer()
			for index := range b.N {
				target := filepath.Join(directory, fmt.Sprintf("target-%d", index))
				if err := linkOrCopyVerificationExecutableWithOps(source, target, operations); err != nil {
					b.Fatal(err)
				}
				if err := os.Remove(target); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

var _ io.Writer = (*hookVerificationTestTarget)(nil)
