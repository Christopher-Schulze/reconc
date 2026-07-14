package main

import (
	"os"
	"testing"
	"time"
)

func TestCommandWithTimeoutCancelsProcess(t *testing.T) {
	command, cancel := commandWithTimeout(25*time.Millisecond, os.Args[0], "-test.run=^TestCommandWithTimeoutHelper$")
	if command.WaitDelay != auditProcessWaitDelay {
		t.Fatalf("WaitDelay = %s, want %s", command.WaitDelay, auditProcessWaitDelay)
	}
	command.Env = append(os.Environ(), "RECONC_COMMAND_TIMEOUT_HELPER=1")
	started := time.Now()
	err := command.Run()
	cancel()
	if err == nil {
		t.Fatal("timed command unexpectedly completed")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timed command took %s, want at most 2s", elapsed)
	}
}

func TestCommandWithTimeoutHelper(t *testing.T) {
	if os.Getenv("RECONC_COMMAND_TIMEOUT_HELPER") != "1" {
		return
	}
	time.Sleep(10 * time.Second)
}
