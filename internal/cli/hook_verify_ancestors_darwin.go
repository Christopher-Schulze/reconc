//go:build darwin

package cli

import "golang.org/x/sys/unix"

func liveHookParentProcessID(pid int) (int, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	return int(info.Eproc.Ppid), nil
}
