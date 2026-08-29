//go:build !(aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || windows)

package cli

import "os"

func signaledCommandExitCode(state *os.ProcessState) (int, bool) {
	return 0, false
}
