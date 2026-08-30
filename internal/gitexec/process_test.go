package gitexec

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	gitExecHelperMode  = "RECONC_GITEXEC_HELPER"
	gitExecHelperReady = "RECONC_GITEXEC_READY"
)

func TestConfiguredGitCommandBoundsDescendantPipeCleanup(t *testing.T) {
	for iteration := 0; iteration < 2; iteration++ {
		ready := filepath.Join(t.TempDir(), "ready")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitCommandCancellationHelper$")
		command.Env = append(os.Environ(), gitExecHelperMode+"=parent", gitExecHelperReady+"="+ready)
		var output bytes.Buffer
		command.Stdout, command.Stderr = &output, &output
		configureGitCommand(command)
		result := make(chan error, 1)
		go func() { result <- command.Run() }()
		descendantPID := waitForGitExecHelper(t, ready)
		cancelStarted := time.Now()
		cancel()
		select {
		case err := <-result:
			if err == nil {
				t.Fatal("canceled Git helper exited successfully")
			}
		case <-time.After(gitCancellationWait + time.Second):
			killGitExecHelper(descendantPID)
			t.Fatal("descendant-held pipes outlived the Git cancellation bound")
		}
		if elapsed := time.Since(cancelStarted); elapsed > gitCancellationWait+time.Second {
			killGitExecHelper(descendantPID)
			t.Fatalf("Git cancellation took %s", elapsed)
		}
		killGitExecHelper(descendantPID)
	}
}

func TestConfiguredGitCommandPreservesExitStatusAndCancellationRaces(t *testing.T) {
	command := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestGitCommandCancellationHelper$")
	command.Env = append(os.Environ(), gitExecHelperMode+"=exit")
	configureGitCommand(command)
	_, err := command.Output()
	var exitErr *exec.ExitError
	matchedExit := errors.As(err, &exitErr)
	stderr := []byte(nil)
	if matchedExit {
		stderr = exitErr.Stderr
	}
	if !matchedExit || exitErr.ExitCode() != 7 || string(exitErr.Stderr) != "git-helper-stderr" {
		t.Fatalf("Git exit status = %v, stderr %q", err, stderr)
	}

	for range 4 {
		ctx, cancel := context.WithCancel(context.Background())
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitCommandCancellationHelper$")
		command.Env = append(os.Environ(), gitExecHelperMode+"=success")
		configureGitCommand(command)
		result := make(chan error, 1)
		go func() { result <- command.Run() }()
		cancel()
		select {
		case <-result:
		case <-time.After(gitCancellationWait + time.Second):
			t.Fatal("Git process-exit race exceeded the cancellation bound")
		}
	}
}

func TestGitCommandCancellationHelper(t *testing.T) {
	switch os.Getenv(gitExecHelperMode) {
	case "parent":
		descendant := exec.Command(os.Args[0], "-test.run=^TestGitCommandCancellationHelper$")
		descendant.Env = append(os.Environ(), gitExecHelperMode+"=descendant")
		descendant.Stdout, descendant.Stderr = os.Stdout, os.Stderr
		configureEscapedGitExecDescendant(descendant)
		if err := descendant.Start(); err != nil {
			t.Fatal(err)
		}
		ready := os.Getenv(gitExecHelperReady)
		if ready == "" {
			t.Fatal("Git helper readiness path is unavailable")
		}
		if err := os.WriteFile(ready, []byte(strconv.Itoa(descendant.Process.Pid)), 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Second)
	case "descendant":
		time.Sleep(30 * time.Second)
	case "exit":
		_, _ = os.Stderr.WriteString("git-helper-stderr")
		os.Exit(7)
	case "success":
		return
	}
}

func waitForGitExecHelper(t testing.TB, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(body)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Git cancellation helper did not publish its descendant")
	return 0
}

func killGitExecHelper(pid int) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = process.Kill()
	_ = process.Release()
}
