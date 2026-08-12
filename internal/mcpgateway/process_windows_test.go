//go:build windows

package mcpgateway

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const windowsBoundaryRoleEnvironment = "RECONC_TEST_WINDOWS_BOUNDARY_ROLE"
const windowsBoundaryPIDEnvironment = "RECONC_TEST_WINDOWS_BOUNDARY_PID"

func TestWindowsProcessBoundaryTerminatesOwnedDescendants(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant.pid")
	command := exec.Command(os.Args[0], "-test.run=^TestWindowsProcessBoundaryHelper$")
	command.Env = append(
		os.Environ(),
		windowsBoundaryRoleEnvironment+"=parent",
		windowsBoundaryPIDEnvironment+"="+marker,
	)
	boundary, err := prepareProcessBoundary(command)
	if err != nil {
		t.Fatal(err)
	}
	defer boundary.Close()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	if err := boundary.Attach(command.Process); err != nil {
		t.Fatal(err)
	}
	waitForRegularFile(t, marker)
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	descendantPID, err := strconv.ParseUint(strings.TrimSpace(string(body)), 10, 32)
	if err != nil || descendantPID == 0 {
		t.Fatalf("descendant PID = %q, %v", body, err)
	}
	descendant, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(descendantPID))
	if err != nil {
		t.Fatalf("open descendant process %d: %v", descendantPID, err)
	}
	defer windows.CloseHandle(descendant)
	if err := boundary.Terminate(); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(ChildKillGrace):
		t.Fatal("owned Windows parent did not terminate")
	}
	event, err := windows.WaitForSingleObject(descendant, uint32(ChildKillGrace/time.Millisecond))
	if err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatalf("owned Windows descendant wait = %d, %v", event, err)
	}
}

func TestWindowsProcessBoundaryHelper(t *testing.T) {
	switch os.Getenv(windowsBoundaryRoleEnvironment) {
	case "":
		return
	case "descendant":
		for {
			time.Sleep(time.Hour)
		}
	case "parent":
		command := exec.Command(os.Args[0], "-test.run=^TestWindowsProcessBoundaryHelper$")
		command.Env = append(os.Environ(), windowsBoundaryRoleEnvironment+"=descendant")
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		marker := os.Getenv(windowsBoundaryPIDEnvironment)
		if marker == "" {
			t.Fatal("Windows descendant PID marker is unavailable")
		}
		if err := os.WriteFile(marker, []byte(fmt.Sprintf("%d\n", command.Process.Pid)), 0o600); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Hour)
		}
	default:
		t.Fatalf("unknown Windows process-boundary role %q", os.Getenv(windowsBoundaryRoleEnvironment))
	}
}
