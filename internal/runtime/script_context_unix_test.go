//go:build !windows

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunScriptContextCancellationKillsProcessGroup(t *testing.T) {
	repo := t.TempDir()
	marker := filepath.Join(repo, "child-survived")
	writeContextScript(t, repo, "scripts/process-tree.sh", "#!/bin/sh\n(sleep 1; echo survived > child-survived) &\nwait\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	outcome, err := RunScriptContext(ctx, repo, "scripts/process-tree.sh", nil, ScriptInput{}, 5, 1)
	if !errors.Is(err, context.DeadlineExceeded) || !outcome.Canceled || outcome.TimedOut {
		t.Fatalf("process-tree cancellation = (%#v, %v)", outcome, err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("caller cancellation left a child process alive: %v", err)
	}
}

func TestRunScriptContextCancellationHonorsKillGrace(t *testing.T) {
	repo := t.TempDir()
	marker := filepath.Join(repo, "child-survived-grace")
	ready := filepath.Join(repo, "ignore-term-ready")
	writeContextScript(t, repo, "scripts/ignore-term.sh", "#!/bin/sh\ntrap '' TERM\nprintf ready > ignore-term-ready\n(trap '' TERM; sleep 3; echo survived > child-survived-grace) &\nwait\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceledAt := make(chan time.Time, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(ready); err == nil {
				now := time.Now()
				cancel()
				canceledAt <- now
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
		canceledAt <- time.Time{}
	}()
	outcome, err := RunScriptContext(ctx, repo, "scripts/ignore-term.sh", nil, ScriptInput{}, 5, 1)
	cancellation := <-canceledAt
	if cancellation.IsZero() {
		t.Fatal("signal-ignoring script did not reach its ready state")
	}
	elapsed := time.Since(cancellation)
	if !errors.Is(err, context.Canceled) || !outcome.Canceled || outcome.TimedOut {
		t.Fatalf("kill-grace cancellation = (%#v, %v)", outcome, err)
	}
	if elapsed < 850*time.Millisecond || elapsed >= 2500*time.Millisecond {
		t.Fatalf("kill grace duration = %s, want cancellation plus one-second grace", elapsed)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("kill-grace escalation left a child process alive: %v", err)
	}
}
