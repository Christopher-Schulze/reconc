//go:build darwin || linux

package cli

import "syscall"

func lowerObservationHookPriorityBestEffort() {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)
}
