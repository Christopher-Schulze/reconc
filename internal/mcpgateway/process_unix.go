//go:build !windows

package mcpgateway

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type unixProcessBoundaryState uint8

const (
	unixBoundaryUnattached unixProcessBoundaryState = iota
	unixBoundaryAttached
	unixBoundaryReaped
	unixBoundaryClosed
)

type unixProcessBoundary struct {
	mu          sync.Mutex
	process     *os.Process
	state       unixProcessBoundaryState
	terminated  bool
	killed      bool
	signalGroup func(int, syscall.Signal) error
	groupExists func(int) (bool, error)
}

func prepareProcessBoundary(command *exec.Cmd) (processBoundary, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &unixProcessBoundary{
		signalGroup: signalUnixProcessGroup,
		groupExists: unixProcessGroupExists,
	}, nil
}

func (b *unixProcessBoundary) Attach(process *os.Process) error {
	if process == nil {
		return errors.New("downstream process is unavailable")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != unixBoundaryUnattached || b.process != nil {
		return errors.New("downstream process boundary is already attached or closed")
	}
	b.process = process
	b.state = unixBoundaryAttached
	return nil
}

func (b *unixProcessBoundary) Terminate() error {
	return b.signalOnce(syscall.SIGTERM)
}

func (b *unixProcessBoundary) Kill() error {
	return b.signalOnce(syscall.SIGKILL)
}

func (b *unixProcessBoundary) signalOnce(signal syscall.Signal) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != unixBoundaryAttached || b.process == nil {
		return nil
	}
	if (signal == syscall.SIGTERM && (b.terminated || b.killed)) ||
		(signal == syscall.SIGKILL && b.killed) {
		return nil
	}
	if err := b.signalLocked(b.process.Pid, signal); err != nil {
		return err
	}
	if signal == syscall.SIGTERM {
		b.terminated = true
	}
	if signal == syscall.SIGKILL {
		b.killed = true
	}
	return nil
}

func (b *unixProcessBoundary) signalLocked(pid int, signal syscall.Signal) error {
	signalGroup := b.signalGroup
	if signalGroup == nil {
		signalGroup = signalUnixProcessGroup
	}
	err := signalGroup(pid, signal)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

func (b *unixProcessBoundary) Reaped() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != unixBoundaryAttached || b.process == nil {
		return nil
	}
	pid := b.process.Pid
	b.process = nil
	b.state = unixBoundaryReaped
	if b.killed {
		return nil
	}
	groupExists := b.groupExists
	if groupExists == nil {
		groupExists = unixProcessGroupExists
	}
	exists, err := groupExists(pid)
	if err != nil || !exists {
		return err
	}
	return b.signalLocked(pid, syscall.SIGKILL)
}

func (b *unixProcessBoundary) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == unixBoundaryClosed {
		return nil
	}
	var err error
	if b.state == unixBoundaryAttached && b.process != nil {
		if !b.killed {
			err = b.signalLocked(b.process.Pid, syscall.SIGKILL)
		}
	}
	b.process = nil
	b.state = unixBoundaryClosed
	return err
}

func signalUnixProcessGroup(pid int, signal syscall.Signal) error {
	return syscall.Kill(-pid, signal)
}

func unixProcessGroupExists(pid int) (bool, error) {
	err := syscall.Kill(-pid, 0)
	if err == nil || err == syscall.EPERM {
		return true, nil
	}
	if err == syscall.ESRCH {
		return false, nil
	}
	return false, err
}
