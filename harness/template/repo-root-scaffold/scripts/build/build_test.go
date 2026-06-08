package main

import "testing"

func TestRunRejectsUnknownAction(t *testing.T) {
	if err := run("nope"); err == nil {
		t.Fatal("expected unknown action to fail")
	}
}
