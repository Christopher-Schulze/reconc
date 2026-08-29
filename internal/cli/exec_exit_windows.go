//go:build windows

package cli

import "os"

func signaledCommandExitCode(state *os.ProcessState) (int, bool) {
	return 0, false
}
