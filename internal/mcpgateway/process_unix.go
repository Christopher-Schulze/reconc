//go:build !windows

package mcpgateway

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type unixProcessBoundary struct {
	process *os.Process
}

func prepareProcessBoundary(command *exec.Cmd) (processBoundary, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &unixProcessBoundary{}, nil
}

func (b *unixProcessBoundary) Attach(process *os.Process) error {
	if process == nil {
		return errors.New("downstream process is unavailable")
	}
	b.process = process
	return nil
}

func (b *unixProcessBoundary) Terminate() error {
	return b.signal(syscall.SIGTERM)
}

func (b *unixProcessBoundary) Kill() error {
	return b.signal(syscall.SIGKILL)
}

func (b *unixProcessBoundary) signal(signal syscall.Signal) error {
	if b == nil || b.process == nil {
		return nil
	}
	err := syscall.Kill(-b.process.Pid, signal)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

func (b *unixProcessBoundary) Close() error { return b.signal(syscall.SIGKILL) }
