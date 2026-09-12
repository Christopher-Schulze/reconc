package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/hooks"
)

func writeLiveHookHostFixture(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("executable metadata fixtures use native POSIX scripts")
	}
	path := filepath.Join(t.TempDir(), "host")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLiveHookHostIdentityUsesVersionAndCursorBrand(t *testing.T) {
	tests := []struct {
		name, kind, version, help string
		valid                     bool
	}{
		{"codex", hooks.KindCodex, "codex-cli 0.154.0", "", true},
		{"devin", hooks.KindDevinCLI, "devin 3000.10.21 (611c1cba)", "", true},
		{"omp", hooks.KindOMP, "omp/18.1.18", "", true},
		{"dsh", "dsh", "0.1.5-rc.2", "dsh: boot a DeepSeek Harness profile", true},
		{"dsh-wrong-brand", "dsh", "0.1.5-rc.2", "Distributed shell", false},
		{"dsh-wrong-version", "dsh", "another-tool 0.1.5", "dsh: boot a DeepSeek Harness profile", false},
		{"cursor", hooks.KindCursor, "2026.09.10-fd3934a", "Start the Cursor Agent", true},
		{"wrong-brand", hooks.KindCursor, "2026.09.10-fd3934a", "Start the Grok Agent", false},
		{"wrong-version", hooks.KindCodex, "devin 3000.10.21", "", false},
		{"raw-diagnostic", hooks.KindCodex, "secret\nvalue", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeLiveHookHostFixture(t, "case \"$1\" in --version) printf '%s\\n' '"+test.version+"';; --help) printf '%s\\n' '"+test.help+"';; *) exit 9;; esac\n")
			surface := "cli"
			if test.kind == hooks.KindCursor {
				surface = string(hooks.HostSurfaceCursorCLIPrint)
			}
			identity, err := inspectLiveHookHost(context.Background(), test.kind, surface, path)
			if (err == nil) != test.valid {
				t.Fatalf("identity=%+v err=%v", identity, err)
			}
			if !test.valid {
				return
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				t.Fatal(err)
			}
			if identity.Executable != resolved || identity.Version != test.version || !identity.IdentityVerified || identity.EntrypointSHA256 != fmt.Sprintf("%x", sha256.Sum256(body)) {
				t.Fatalf("identity=%+v", identity)
			}
		})
	}
}

func TestLiveHookHostDiscoveryPrefersCursorAndRejectsWrongAlias(t *testing.T) {
	correct := writeLiveHookHostFixture(t, "case \"$1\" in --version) echo 2026.09.10-fd3934a;; --help) echo 'Start the Cursor Agent';; esac\n")
	wrong := writeLiveHookHostFixture(t, "echo 'grok 1.0.0'\n")
	original := hookVerifyLookPath
	t.Cleanup(func() { hookVerifyLookPath = original })
	for _, available := range []bool{true, false} {
		var lookedUp []string
		hookVerifyLookPath = func(name string) (string, error) {
			lookedUp = append(lookedUp, name)
			if name == "cursor-agent" && available {
				return correct, nil
			}
			if name == "agent" {
				return wrong, nil
			}
			return "", exec.ErrNotFound
		}
		identity, err := discoverLiveHookHost(context.Background(), hooks.KindCursor, string(hooks.HostSurfaceCursorCLIPrint))
		if (err == nil) != available || lookedUp[0] != "cursor-agent" {
			t.Fatalf("available=%t identity=%+v err=%v lookups=%v", available, identity, err, lookedUp)
		}
		if available && len(lookedUp) != 1 {
			t.Fatal("discovery ran unrelated alias despite verified Cursor entrypoint")
		}
	}
}

func TestLiveHookHostMetadataIsBoundedAndRejectsMutation(t *testing.T) {
	tests := []struct {
		name, body string
		timeout    time.Duration
	}{
		{"canceled", "exec sleep 5\n", 20 * time.Millisecond},
		{"failed", "echo secret-token >&2; exit 1\n", time.Second},
		{"oversized", "head -c 40000 /dev/zero\n", time.Second},
		{"mutated", "echo '# mutation' >> \"$0\"; echo 'codex-cli 0.154.0'\n", time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeLiveHookHostFixture(t, test.body)
			ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
			defer cancel()
			start := time.Now()
			identity, err := inspectLiveHookHost(ctx, hooks.KindCodex, "cli", path)
			if err == nil || identity != nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("identity=%+v err=%v", identity, err)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("metadata probe exceeded cancellation budget")
			}
		})
	}
}
