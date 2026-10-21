package runtime

import (
	"os"
	"testing"

	"reconc.dev/reconc/internal/testtemp"
)

// TestMain keeps every temporary test path short; see package testtemp.
func TestMain(testingMain *testing.M) {
	os.Exit(testtemp.Main(testingMain))
}
