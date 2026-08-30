//go:build !windows

package mcpgateway

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

const processBoundarySleeperEnvironment = "RECONC_TEST_PROCESS_BOUNDARY_SLEEPER"
const processBoundaryDescendantPIDEnvironment = "RECONC_TEST_PROCESS_BOUNDARY_DESCENDANT_PID"

func TestUnixProcessBoundaryTerminatesAndKillsOwnedGroups(t *testing.T) {
	for _, test := range []struct {
		name   string
		signal func(processBoundary) error
	}{
		{name: "terminate", signal: func(boundary processBoundary) error { return boundary.Terminate() }},
		{name: "kill", signal: func(boundary processBoundary) error { return boundary.Kill() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(executable, "-test.run=^TestUnixProcessBoundarySleeper$")
			command.Env = append(os.Environ(), processBoundarySleeperEnvironment+"=1")
			boundary, err := prepareProcessBoundary(command)
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				if waited {
					return
				}
				_ = boundary.Kill()
				_ = command.Wait()
			}()
			if err := boundary.Attach(command.Process); err != nil {
				t.Fatal(err)
			}
			if err := test.signal(boundary); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case err := <-done:
				waited = true
				if err == nil {
					t.Fatal("signalled child exited successfully instead of by signal")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("signalled child process group did not terminate")
			}
		})
	}
}

func TestUnixProcessBoundaryKillsDescendants(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidPath := t.TempDir() + "/descendant.pid"
	command := exec.Command(executable, "-test.run=^TestUnixProcessBoundarySleeper$")
	command.Env = append(
		os.Environ(),
		processBoundarySleeperEnvironment+"=parent",
		processBoundaryDescendantPIDEnvironment+"="+pidPath,
	)
	boundary, err := prepareProcessBoundary(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = boundary.Kill()
		_ = command.Wait()
	}()
	if err := boundary.Attach(command.Process); err != nil {
		t.Fatal(err)
	}
	waitForRegularFile(t, pidPath)
	body, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(body))
	if err != nil || pid <= 0 {
		t.Fatalf("descendant PID = %q, %v", body, err)
	}
	if err := boundary.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("descendant process %d survived process-group kill: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUnixProcessBoundaryStopsSignalingAfterReap(t *testing.T) {
	signals := []syscall.Signal{}
	boundary := &unixProcessBoundary{
		process: &os.Process{Pid: 12345}, state: unixBoundaryAttached,
		groupExists: func(int) (bool, error) { return false, nil },
		signalGroup: func(_ int, signal syscall.Signal) error {
			signals = append(signals, signal)
			return nil
		},
	}
	if err := boundary.Reaped(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Terminate(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Close(); err != nil {
		t.Fatal(err)
	}
	if len(signals) != 0 {
		t.Fatalf("post-reap signals = %v, want none", signals)
	}
}

func TestUnixProcessBoundaryRejectsInvalidAttachTransitions(t *testing.T) {
	if err := (&unixProcessBoundary{}).Attach(nil); err == nil {
		t.Fatal("nil process attachment was accepted")
	}
	boundary := &unixProcessBoundary{}
	if err := boundary.Attach(&os.Process{Pid: 12345}); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Attach(&os.Process{Pid: 12346}); err == nil {
		t.Fatal("duplicate process attachment was accepted")
	}
	closed := &unixProcessBoundary{}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closed.Attach(&os.Process{Pid: 12345}); err == nil {
		t.Fatal("attachment after close was accepted")
	}
}

func TestUnixProcessBoundaryReapKillsDescendantsExactlyOnce(t *testing.T) {
	signals := []syscall.Signal{}
	boundary := &unixProcessBoundary{
		process: &os.Process{Pid: 12345}, state: unixBoundaryAttached,
		groupExists: func(int) (bool, error) { return true, nil },
		signalGroup: func(_ int, signal syscall.Signal) error {
			signals = append(signals, signal)
			return nil
		},
	}
	if err := boundary.Reaped(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Reaped(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Close(); err != nil {
		t.Fatal(err)
	}
	if len(signals) != 1 || signals[0] != syscall.SIGKILL {
		t.Fatalf("reap signals = %v, want one SIGKILL", signals)
	}
}

func TestUnixProcessBoundarySignalsAreIdempotent(t *testing.T) {
	signals := []syscall.Signal{}
	boundary := &unixProcessBoundary{
		process: &os.Process{Pid: 12345}, state: unixBoundaryAttached,
		groupExists: func(int) (bool, error) { return true, nil },
		signalGroup: func(_ int, signal syscall.Signal) error {
			signals = append(signals, signal)
			return nil
		},
	}
	if err := boundary.Terminate(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Terminate(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Reaped(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Close(); err != nil {
		t.Fatal(err)
	}
	want := []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}
	if len(signals) != len(want) || signals[0] != want[0] || signals[1] != want[1] {
		t.Fatalf("signals = %v, want %v", signals, want)
	}
}

func TestUnixProcessBoundaryReapCleansLeaderlessDescendants(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidPath := t.TempDir() + "/descendant.pid"
	command := exec.Command(executable, "-test.run=^TestUnixProcessBoundarySleeper$")
	command.Env = append(
		os.Environ(),
		processBoundarySleeperEnvironment+"=parent-exits",
		processBoundaryDescendantPIDEnvironment+"="+pidPath,
	)
	boundary, err := prepareProcessBoundary(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Attach(command.Process); err != nil {
		t.Fatal(err)
	}
	defer boundary.Close()
	waitForRegularFile(t, pidPath)
	body, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	descendantPID, err := strconv.Atoi(string(body))
	if err != nil || descendantPID <= 0 {
		t.Fatalf("descendant PID = %q, %v", body, err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("leader exit: %v", err)
	}
	if err := boundary.Reaped(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(descendantPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("leaderless descendant %d survived reap cleanup: %v", descendantPID, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUnixProcessBoundarySleeper(t *testing.T) {
	role := os.Getenv(processBoundarySleeperEnvironment)
	if role == "" {
		return
	}
	if role == "parent" || role == "parent-exits" {
		executable, err := os.Executable()
		if err != nil {
			os.Exit(91)
		}
		child := exec.Command(executable, "-test.run=^TestUnixProcessBoundarySleeper$")
		child.Env = append(os.Environ(), processBoundarySleeperEnvironment+"=child")
		if err := child.Start(); err != nil {
			os.Exit(92)
		}
		pidPath := os.Getenv(processBoundaryDescendantPIDEnvironment)
		candidatePath := pidPath + ".candidate"
		if err := os.WriteFile(
			candidatePath,
			[]byte(strconv.Itoa(child.Process.Pid)),
			0o600,
		); err != nil {
			os.Exit(93)
		}
		if err := os.Rename(candidatePath, pidPath); err != nil {
			os.Exit(94)
		}
		if role == "parent-exits" {
			return
		}
	}
	time.Sleep(time.Hour)
}
