package main

import (
	"strings"
	"testing"
)

func TestRunCommandExecutesRealToolAndSurfacesLaunchFailure(t *testing.T) {
	if err := runCommand("go", "version"); err != nil {
		t.Fatalf("runCommand(go version): %v", err)
	}
	err := runCommand("reconc-build-command-that-does-not-exist")
	if err == nil || !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("missing command error = %v", err)
	}
}
