//go:build !windows

package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestPiTrustProbeFollowsSymlinkedConfig proves the bounded probe still reads a
// symlinked user config. Pi's agent directory belongs to the user, and pointing
// trust.json at a dotfile repository is the normal layout; refusing the final
// symlink would report a healthy trust store as unreadable.
func TestPiTrustProbeFollowsSymlinkedConfig(t *testing.T) {
	repo := t.TempDir()
	agentDir := t.TempDir()
	store := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)

	writePiSettings(t, store, "always")
	if err := os.Symlink(filepath.Join(store, "settings.json"), filepath.Join(agentDir, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindPi, repo, false); err != nil {
		t.Fatal(err)
	}
	status := statusForKind(t, repo, KindPi)
	if status.State != StateConfigured || !status.Configured {
		t.Fatalf("symlinked Pi settings must stay readable: %+v", status)
	}
}

// TestPiTrustProbeRejectsFifoWithoutBlocking is the reason the probe is bounded
// at all: a bare os.Open on a FIFO blocks until a writer appears, which would
// hang `reconc hook status` forever.
func TestPiTrustProbeRejectsFifoWithoutBlocking(t *testing.T) {
	agentDir := t.TempDir()
	fifo := filepath.Join(agentDir, "trust.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("FIFO creation unsupported on this host: %v", err)
	}

	type probeResult struct {
		err error
	}
	done := make(chan probeResult, 1)
	go func() {
		_, err := readBoundedPiJSON(fifo)
		done <- probeResult{err: err}
	}()

	select {
	case result := <-done:
		if result.err == nil {
			t.Fatal("a FIFO must not be accepted as a Pi config")
		}
		if !strings.Contains(result.err.Error(), "regular file") {
			t.Fatalf("expected a regular-file rejection, got %v", result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Pi config probe blocked on a FIFO")
	}
}

// TestPiTrustProbeBoundsOversizedConfig keeps the byte bound observable on the
// shipped probe.
func TestPiTrustProbeBoundsOversizedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	if err := os.WriteFile(path, make([]byte, maxPiTrustConfigBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedPiJSON(path); err == nil {
		t.Fatal("an oversized Pi config must be refused")
	}
}

// TestPiAgentDirResolvesEveryConfiguredForm covers the override an operator
// actually types. A relative value is repository-relative and a tilde value is
// home-relative; both resolve to an absolute directory before any probe runs.
func TestPiAgentDirResolvesEveryConfiguredForm(t *testing.T) {
	repo := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory on this host: %v", err)
	}
	cases := []struct {
		name       string
		configured string
		want       string
	}{
		{name: "absolute", configured: filepath.Join(repo, "agent"), want: filepath.Join(repo, "agent")},
		{name: "repository relative", configured: ".pi/agent", want: filepath.Join(repo, ".pi", "agent")},
		{name: "home tilde", configured: "~", want: home},
		{name: "home relative", configured: "~/.pi/agent", want: filepath.Join(home, ".pi", "agent")},
		{name: "trailing separator", configured: filepath.Join(repo, "agent") + "/", want: filepath.Join(repo, "agent")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_CODING_AGENT_DIR", tc.configured)
			got, err := piAgentDir(repo)
			if err != nil {
				t.Fatalf("piAgentDir: %v", err)
			}
			if got != tc.want {
				t.Fatalf("piAgentDir(%q) = %q, want %q", tc.configured, got, tc.want)
			}
		})
	}

	t.Setenv("PI_CODING_AGENT_DIR", "")
	got, err := piAgentDir(repo)
	if err != nil {
		t.Fatalf("piAgentDir default: %v", err)
	}
	if got != filepath.Join(home, ".pi", "agent") {
		t.Fatalf("default agent dir = %q", got)
	}
}
