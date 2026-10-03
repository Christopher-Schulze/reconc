//go:build !windows

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"reconc.dev/reconc/internal/processgroup"
)

func TestRunScriptCleansLeaderlessResistantDescendant(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "caller-cancel"
		if timeout {
			name = "policy-timeout"
		}
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			writeContextScript(t, repo, "scripts/leader.sh", `#!/bin/sh
printf '%s' "$$" > parent.pid
sh -c 'trap "" TERM; printf ready > child-ready; sleep 3; printf survived > child-survived' </dev/null >/dev/null 2>&1 &
printf '%s' "$!" > child.pid
while [ ! -f child-ready ]; do sleep 0.01; done
printf ready > parent-ready
wait
`)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			t.Cleanup(func() {
				body, err := os.ReadFile(filepath.Join(repo, "parent.pid"))
				if err != nil {
					return
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
				if err == nil && pid > 0 {
					if err := processgroup.Signal(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
						t.Errorf("cleanup fixture group: %v", err)
					}
				}
			})
			type runResult struct {
				outcome ScriptOutcome
				err     error
			}
			finished := make(chan runResult, 1)
			timeoutSeconds := 5
			if timeout {
				timeoutSeconds = 1
			}
			go func() {
				outcome, err := RunScriptContext(ctx, repo, "scripts/leader.sh", nil, ScriptInput{}, timeoutSeconds, 1)
				finished <- runResult{outcome, err}
			}()
			joined := false
			t.Cleanup(func() {
				cancel()
				if !joined {
					select {
					case <-finished:
					case <-time.After(3 * time.Second):
						t.Error("fixture script lifecycle did not close")
					}
				}
			})
			deadline := time.Now().Add(2 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(repo, "parent-ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("script did not reach descendant readiness")
				}
				time.Sleep(5 * time.Millisecond)
			}
			body, err := os.ReadFile(filepath.Join(repo, "child.pid"))
			if err != nil {
				t.Fatal(err)
			}
			child, err := strconv.Atoi(strings.TrimSpace(string(body)))
			if err != nil || child <= 0 {
				t.Fatalf("invalid child PID: %q, %v", body, err)
			}
			if !timeout {
				cancel()
			}
			select {
			case result := <-finished:
				joined = true
				if timeout {
					if result.err != nil || !result.outcome.TimedOut || result.outcome.Canceled {
						t.Fatalf("timeout outcome: %+v, %v", result.outcome, result.err)
					}
				} else if !errors.Is(result.err, context.Canceled) || !result.outcome.Canceled || result.outcome.TimedOut {
					t.Fatalf("caller outcome: %+v, %v", result.outcome, result.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("script lifecycle did not return")
			}
			deadline = time.Now().Add(2 * time.Second)
			for {
				err = syscall.Kill(child, 0)
				if err == syscall.ESRCH {
					break
				}
				if err != nil || time.Now().After(deadline) {
					t.Fatalf("resistant descendant still alive after lifecycle return: pid=%d err=%v", child, err)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if _, err := os.Stat(filepath.Join(repo, "child-survived")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("descendant mutated repository after cancellation: %v", err)
			}
		})
	}
}
