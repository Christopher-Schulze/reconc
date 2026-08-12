package mcpgateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reconc.dev/reconc/internal/actionstate"
	"reconc.dev/reconc/internal/pathidentity"
)

func newPrivateGatewayHome(t testing.TB) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "reconc-home")
	if _, err := actionstate.CreateIdentityKey(home, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	resolved, err := pathidentity.ResolveExisting(home)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func writePrivateGatewayFixture(t testing.TB, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := securePrivateGatewayFixture(path); err != nil {
		t.Fatal(err)
	}
}
