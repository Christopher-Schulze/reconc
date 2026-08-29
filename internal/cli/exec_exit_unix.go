//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package cli

import (
	"os"
	"syscall"
)

func signaledCommandExitCode(state *os.ProcessState) (int, bool) {
	if state == nil {
		return 0, false
	}
	waitStatus, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !waitStatus.Signaled() {
		return 0, false
	}
	signal := int(waitStatus.Signal())
	if signal <= 0 {
		return 0, false
	}
	return 128 + signal, true
}
