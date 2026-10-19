package usercli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain gives every test in this package a private home directory. The
// default portable skill location lives under the user's home, so without this
// a developer machine with an installed skill makes lifecycle tests observe
// that unrelated real installation. Tests that need a specific home still set
// HOME themselves. Go's own home-derived locations stay pinned to the real
// user so child go commands keep the existing module cache, build cache and
// environment file instead of populating the temporary home.
func TestMain(m *testing.M) {
	// Helper child processes re-run TestMain and may exit without returning;
	// they inherit the parent's private home instead of creating their own.
	if os.Getenv(hermeticHomeEnv) != "" {
		os.Exit(m.Run())
	}
	if err := pinGoLocations(); err != nil {
		fmt.Fprintln(os.Stderr, "pin go locations:", err)
		os.Exit(1)
	}
	home, err := os.MkdirTemp("", "reconc-usercli-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create hermetic test home:", err)
		os.Exit(1)
	}
	for _, name := range []string{"HOME", "USERPROFILE", hermeticHomeEnv} {
		if err := os.Setenv(name, home); err != nil {
			fmt.Fprintln(os.Stderr, "set hermetic test home:", err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintln(os.Stderr, "remove hermetic test home:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

const hermeticHomeEnv = "RECONC_USERCLI_TEST_HOME"

func pinGoLocations() error {
	realHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	defaults := map[string]string{
		"GOPATH":  filepath.Join(realHome, "go"),
		"GOCACHE": filepath.Join(cacheDir, "go-build"),
		"GOENV":   filepath.Join(configDir, "go", "env"),
	}
	for name, value := range defaults {
		if os.Getenv(name) != "" {
			continue
		}
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}
