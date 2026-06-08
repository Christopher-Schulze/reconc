package main

import "testing"

func TestProjectName(t *testing.T) {
	if got := projectName(); got != "project" {
		t.Fatalf("projectName() = %q, want project", got)
	}
}
