package mcpgateway

import (
	"bytes"
	"context"
	"fmt"
	"runtime/pprof"
	"strings"
	"testing"
)

func TestGatewayRefreshWorkerShutdownHasNoRuntimeLeak(t *testing.T) {
	if err := runRefreshWorkerLifecycle(); err != nil {
		t.Fatalf("close gateway refresh worker: %v", err)
	}
	assertNoGoroutineLeakStack(t, "reconc.dev/reconc/internal/mcpgateway.(*Gateway).runToolRefreshes")
}

func runRefreshWorkerLifecycle() error {
	ctx, cancel := context.WithCancel(context.Background())
	gateway := &Gateway{
		ctx: ctx, cancel: cancel,
		pending:           make(map[string]pendingApproval),
		semaphore:         make(chan struct{}, MaxConcurrentCalls),
		refreshRequests:   make(chan struct{}, 1),
		refreshWorkerDone: make(chan struct{}),
		fatalErrors:       make(chan error, 1),
	}
	go gateway.runToolRefreshes()
	return gateway.Close()
}

func assertNoGoroutineLeakStack(t *testing.T, stackMarker string) {
	t.Helper()
	profile := pprof.Lookup("goroutineleak")
	if profile == nil {
		t.Fatal("Go runtime omitted the goroutineleak profile")
	}
	var report bytes.Buffer
	if err := profile.WriteTo(&report, 2); err != nil {
		t.Fatalf("write goroutineleak profile: %v", err)
	}
	if strings.Contains(report.String(), stackMarker) {
		t.Fatalf("runtime reported a leaked gateway worker matching %q:\n%s", stackMarker, boundedLeakReport(report.String()))
	}
}

func boundedLeakReport(report string) string {
	const maximum = 16 << 10
	if len(report) <= maximum {
		return report
	}
	return fmt.Sprintf("%s\n[goroutineleak profile truncated from %d bytes]", report[:maximum], len(report))
}
