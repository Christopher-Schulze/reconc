package grokacp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeNativeStopGate(t *testing.T) {
	tests := []struct {
		name       string
		guide      string
		want       bool
		wantDetail string
	}{
		{
			name:       "blocking contract",
			guide:      "## Hook Events\n| Event | Blocking? |\n| `Stop` | Yes - can block the stop |\n### Stop Decision Control\n",
			want:       true,
			wantDetail: "advertises blocking Stop",
		},
		{
			name:       "passive stable contract",
			guide:      "## Hook Events\n| Event | Blocking? |\n| `Stop` | No |\nOnly `PreToolUse` can block.\n",
			wantDetail: "does not advertise",
		},
		{
			name:       "section alone is insufficient",
			guide:      "### Stop Decision Control\n",
			wantDetail: "does not advertise",
		},
		{
			name:       "table row alone is insufficient",
			guide:      "| `Stop` | An agent turn ends. | Yes - can block the stop |\n",
			wantDetail: "does not advertise",
		},
		{
			name:       "malformed oversized line fails closed",
			guide:      "| `Stop` | Yes - can block the stop |\n### Stop Decision Control\n" + strings.Repeat("x", 128<<10),
			wantDetail: "does not advertise",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "10-hooks.md")
			if err := os.WriteFile(path, []byte(test.guide), 0o600); err != nil {
				t.Fatal(err)
			}
			probe := probeNativeStopGateAt(path)
			if probe.Supported != test.want || !strings.Contains(probe.Detail, test.wantDetail) {
				t.Fatalf("probe = %+v, want supported=%t detail containing %q", probe, test.want, test.wantDetail)
			}
			if probe.DocumentationPath != path {
				t.Fatalf("documentation path = %q, want %q", probe.DocumentationPath, path)
			}
		})
	}
}

func TestProbeNativeStopGateUsesGrokHomeAndFailsClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv(grokHomeEnv, home)

	probe := ProbeNativeStopGate()
	if probe.Supported || !strings.Contains(probe.Detail, "inspect installed Grok hook guide") {
		t.Fatalf("missing guide probe = %+v", probe)
	}
	wantPath := filepath.Join(home, filepath.FromSlash(nativeStopGateGuidePath))
	if probe.DocumentationPath != wantPath {
		t.Fatalf("documentation path = %q, want %q", probe.DocumentationPath, wantPath)
	}

	if err := os.MkdirAll(filepath.Dir(wantPath), 0o700); err != nil {
		t.Fatal(err)
	}
	oversized := strings.Repeat("x", nativeStopGateGuideMax+1)
	if err := os.WriteFile(wantPath, []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	probe = ProbeNativeStopGate()
	if probe.Supported || !strings.Contains(probe.Detail, "exceeds") {
		t.Fatalf("oversized guide probe = %+v", probe)
	}

	if err := os.Remove(wantPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(wantPath, 0o700); err != nil {
		t.Fatal(err)
	}
	probe = ProbeNativeStopGate()
	if probe.Supported || !strings.Contains(probe.Detail, "not a regular file") {
		t.Fatalf("non-regular guide probe = %+v", probe)
	}
}
