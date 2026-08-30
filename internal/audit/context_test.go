package audit

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/jsonl"
)

func TestAuditContextEntryPointsRejectCanceledLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := t.TempDir()
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "retention-enforce", run: func() error { _, err := EnforceRetentionContext(ctx, repo); return err }},
		{name: "retention-inspect", run: func() error { _, err := InspectRetentionContext(ctx, repo); return err }},
		{name: "tail", run: func() error { _, err := TailContext(ctx, repo, TailOptions{}); return err }},
		{name: "stats", run: func() error { _, err := StatsContext(ctx, repo); return err }},
		{name: "export", run: func() error { return ExportJSONLContext(ctx, repo, &bytes.Buffer{}) }},
		{name: "verify", run: func() error { _, err := VerifyContext(ctx, repo); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("context entry point error = %v, want %v", err, context.Canceled)
			}
		})
	}
}

func TestAppendContextCancelsContendedProcessGate(t *testing.T) {
	repo := filepath.Join("synthetic", t.Name())
	release, err := acquireAuditAppendGate(context.Background(), repo, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := AppendContext(ctx, repo, Entry{Event: "check"}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("AppendContext() error = %v, want %v", err, context.Canceled)
	}
}

func TestVerifyContextCancelsContendedJSONLLock(t *testing.T) {
	repo := t.TempDir()
	if err := Append(repo, Entry{Event: "check", Decision: "pass"}, 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, AuditFileRelative)
	layout := auditLayout(path)
	acquired := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var releaseOnce sync.Once
	waited := false
	go func() {
		done <- jsonl.ReadSnapshotContextWithLayout(context.Background(), path, layout, nil, func() error {
			close(acquired)
			<-release
			return nil
		})
	}()
	<-acquired
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		if waited {
			return
		}
		if err := <-done; err != nil {
			t.Errorf("release held audit lock: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := VerifyContext(ctx, repo); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("VerifyContext() error = %v, want %v", err, context.DeadlineExceeded)
	}

	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waited = true
}
