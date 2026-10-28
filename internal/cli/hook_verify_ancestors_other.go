//go:build !darwin && !linux

package cli

import "fmt"

// Native runners that prove capture ownership run only on macOS and Linux.
func liveHookParentProcessID(int) (int, error) {
	return 0, fmt.Errorf("process ancestry is unsupported on this platform")
}
