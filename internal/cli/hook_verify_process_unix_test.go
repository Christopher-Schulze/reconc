//go:build !windows

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLiveHookOwnedProcessGroupHelper(t *testing.T) {
	path := os.Getenv("RECONC_HOOK_VERIFY_GROUP_TEST_FILE")
	if path == "" {
		return
	}
	if err := syscall.Setpgid(0, 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", "-c", `printf '%s\n' "$$" > "$1"; exec sleep 30`, "capture-child", path)
	if _, err := configureLiveHookCaptureProcess(command); err != nil {
		t.Fatal(err)
	}
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveHookCancellationOwnsCaptureDescendants(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "capture-pid")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", "-c", `export RECONC_HOOK_VERIFY_HOST_GROUP=$$; "$@"; code=$?; exit "$code"`, "native-test", executable, "-test.run=^TestLiveHookOwnedProcessGroupHelper$")
	command.Env = append(os.Environ(), "RECONC_HOOK_VERIFY_GROUP_TEST_FILE="+path)
	configureHookVerificationProcess(command)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var child int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body, readErr := os.ReadFile(path)
		if readErr == nil {
			child, err = strconv.Atoi(strings.TrimSpace(string(body)))
			if err == nil && child > 1 {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	group, groupErr := syscall.Getpgid(child)
	cancel()
	waitErr := command.Wait()
	if child <= 1 || groupErr != nil || group != command.Process.Pid || waitErr == nil {
		t.Fatalf("capture pid=%d group=%d group-error=%v wait-error=%v", child, group, groupErr, waitErr)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-group, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("owned native/capture group survived cancellation")
}

func TestLiveHookCleanupStopsDescendantsAfterNativeExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "orphan-pid")
	command := exec.CommandContext(ctx, "/bin/sh", "-c", `sleep 30 </dev/null >/dev/null 2>&1 & printf '%s\n' "$!" > "$1"`, "native-exit", path)
	configureHookVerificationProcess(command)
	t.Cleanup(func() {
		if err := cleanupLiveHookHostProcess(command); err != nil {
			t.Error(err)
		}
	})
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil || child <= 1 {
		t.Fatal("missing actual descendant pid")
	}
	group, err := syscall.Getpgid(child)
	if err != nil || group != command.Process.Pid {
		t.Fatalf("descendant group=%d error=%v", group, err)
	}
	if err := cleanupLiveHookHostProcess(command); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-group, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("native descendant group survived cleanup")
	}
}
