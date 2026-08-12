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

func TestUnixProcessBoundarySleeper(t *testing.T) {
	role := os.Getenv(processBoundarySleeperEnvironment)
	if role == "" {
		return
	}
	if role == "parent" {
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
	}
	time.Sleep(time.Hour)
}
