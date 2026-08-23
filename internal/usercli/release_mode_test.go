package usercli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseCandidateModeMatchesPlatformContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), executableName())
	if err := os.WriteFile(path, []byte("candidate"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o700, 0o400} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		matched, err := releaseCandidateModeMatches(path, info, mode)
		if err != nil || !matched {
			t.Fatalf("mode %04o match = %t, err=%v", mode, matched, err)
		}
		opposite := os.FileMode(0o700)
		if mode == 0o700 {
			opposite = 0o400
		}
		matched, err = releaseCandidateModeMatches(path, info, opposite)
		if err != nil || matched {
			t.Fatalf("mode %04o opposite %04o match = %t, err=%v", mode, opposite, matched, err)
		}
	}
}
