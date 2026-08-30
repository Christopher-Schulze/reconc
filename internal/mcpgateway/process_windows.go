//go:build windows

package mcpgateway

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessBoundary struct {
	job      windows.Handle
	process  windows.Handle
	assigned bool
}

func prepareProcessBoundary(command *exec.Cmd) (processBoundary, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create downstream job object: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		return nil, errors.Join(
			fmt.Errorf("configure downstream job object: %w", err),
			windows.CloseHandle(job),
		)
	}
	command.SysProcAttr = &windows.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED,
	}
	return &windowsProcessBoundary{job: job}, nil
}

func (b *windowsProcessBoundary) Attach(process *os.Process) error {
	if process == nil {
		return fmt.Errorf("downstream process is unavailable")
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(process.Pid),
	)
	if err != nil {
		return err
	}
	b.process = handle
	if err := windows.AssignProcessToJobObject(b.job, handle); err != nil {
		return err
	}
	b.assigned = true
	if err := resumeSuspendedProcess(uint32(process.Pid)); err != nil {
		return fmt.Errorf("resume job-owned downstream process: %w", err)
	}
	return nil
}

func resumeSuspendedProcess(processID uint32) error {
	deadline := time.Now().Add(time.Second)
	for {
		resumed, err := resumeOwnedThread(processID)
		if err != nil {
			return err
		}
		if resumed {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("suspended downstream primary thread was not found")
		}
		time.Sleep(time.Millisecond)
	}
}

func resumeOwnedThread(processID uint32) (resumed bool, resultErr error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, windows.CloseHandle(snapshot)) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return false, nil
		}
		return false, err
	}
	for {
		if entry.OwnerProcessID == processID {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return false, openErr
			}
			previous, resumeErr := windows.ResumeThread(thread)
			closeErr := windows.CloseHandle(thread)
			if resumeErr != nil || closeErr != nil {
				return false, errors.Join(resumeErr, closeErr)
			}
			if previous != 1 {
				return false, fmt.Errorf("downstream primary thread suspend count was %d", previous)
			}
			return true, nil
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return false, nil
			}
			return false, err
		}
	}
}

func (b *windowsProcessBoundary) Terminate() error {
	if b == nil {
		return nil
	}
	if b.assigned && b.job != 0 {
		if err := windows.TerminateJobObject(b.job, 1); err == nil {
			return nil
		} else if b.process != 0 {
			return errors.Join(err, windows.TerminateProcess(b.process, 1))
		} else {
			return err
		}
	}
	if b.process != 0 {
		return windows.TerminateProcess(b.process, 1)
	}
	return nil
}

func (b *windowsProcessBoundary) Kill() error { return b.Terminate() }

func (*windowsProcessBoundary) Reaped() error { return nil }

func (b *windowsProcessBoundary) Close() error {
	if b == nil {
		return nil
	}
	var result error
	if b.process != 0 {
		result = errors.Join(result, windows.CloseHandle(b.process))
		b.process = 0
	}
	b.assigned = false
	if b.job != 0 {
		result = errors.Join(result, windows.CloseHandle(b.job))
		b.job = 0
	}
	return result
}
