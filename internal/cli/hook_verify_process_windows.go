//go:build windows

package cli

import (
	"fmt"
	"os/exec"
	"time"
)

func configureHookVerificationProcess(command *exec.Cmd) {
	// CommandContext already terminates the direct child on cancellation.
	// The report cannot claim descendant termination on this platform.
	command.WaitDelay = 100 * time.Millisecond
}

func configureLiveHookCaptureProcess(command *exec.Cmd) (int, error) {
	configureHookVerificationProcess(command)
	return 0, nil
}

func cleanupLiveHookHostProcess(command *exec.Cmd) error {
	// Native runners requiring process-group proof reject Windows before launch.
	return fmt.Errorf("native process-group cleanup is unsupported on Windows")
}
