//go:build !windows

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	command := exec.CommandContext(ctx, "/bin/sh", "-c", `export RECONC_HOOK_VERIFY_HOST_GROUP=$$; "$@"; code=$?; exit "$code"`, "native-test", executable, "-test.run=^TestLiveHookOwnedProcessGroupHelper$", cliTestChildFlag())
	// The child is killed, so it gets a private TMPDIR that this test removes.
	command.Env = append(os.Environ(), "RECONC_HOOK_VERIFY_GROUP_TEST_FILE="+path, "TMPDIR="+t.TempDir())
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

func TestLiveHookAncestorChainHelper(t *testing.T) {
	path := os.Getenv("RECONC_HOOK_VERIFY_ANCESTOR_TEST_FILE")
	if path == "" {
		return
	}
	encoded, err := json.Marshal(liveHookAncestorProcessIDs())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLiveHookAncestorsReachHostAcrossNewSession proves that a capture started
// through an intermediate shell in a new session, without the host group
// variable, still lists the starting host process among its ancestors, as a
// Codex hook does since codex-cli 0.164.0.
func TestLiveHookAncestorsReachHostAcrossNewSession(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ancestors.json")
	command := exec.CommandContext(t.Context(), "/bin/sh", "-c", `"$@"; exit $?`, "hook-shell", executable, "-test.run=^TestLiveHookAncestorChainHelper$", cliTestChildFlag())
	environment := []string{"RECONC_HOOK_VERIFY_ANCESTOR_TEST_FILE=" + path, "TMPDIR=" + t.TempDir()}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "RECONC_HOOK_VERIFY_HOST_GROUP=") && !strings.HasPrefix(entry, "TMPDIR=") {
			environment = append(environment, entry)
		}
	}
	command.Env = environment
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v\n%s", err, output)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ancestors []int
	if err := json.Unmarshal(body, &ancestors); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ancestors, command.Process.Pid) || !slices.Contains(ancestors, os.Getpid()) {
		t.Fatalf("ancestors %v lack the hook shell %d or the host %d", ancestors, command.Process.Pid, os.Getpid())
	}
}

func TestLiveHookForeignSessionGroupHelper(t *testing.T) {
	path := os.Getenv("RECONC_HOOK_VERIFY_FOREIGN_SESSION_FILE")
	if path == "" {
		return
	}
	command := exec.CommandContext(t.Context(), "/bin/true")
	group, err := configureLiveHookCaptureProcess(command)
	result := map[string]any{"group": group, "error": ""}
	if err != nil {
		result["error"] = err.Error()
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLiveHookCaptureInForeignSessionUsesAncestry proves that a capture whose
// inherited host group lives in another session, as when codex-cli copies the
// probe's group variable into the shell snapshot of a hook started in a new
// session, does not fail on the impossible group join and leaves ownership to
// the recorded ancestry.
func TestLiveHookCaptureInForeignSessionUsesAncestry(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	group, err := syscall.Getpgid(0)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "result.json")
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestLiveHookForeignSessionGroupHelper$", cliTestChildFlag())
	command.Env = append(os.Environ(), "RECONC_HOOK_VERIFY_FOREIGN_SESSION_FILE="+path, "RECONC_HOOK_VERIFY_HOST_GROUP="+strconv.Itoa(group), "TMPDIR="+t.TempDir())
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v\n%s", err, output)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Group int    `json:"group"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Group != 0 {
		t.Fatalf("capture in a foreign session = group %d, error %q; want no join and no error", result.Group, result.Error)
	}
}
