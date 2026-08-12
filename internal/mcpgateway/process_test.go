package mcpgateway

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCleanupProcessErrorIgnoresExpectedTerminationResults(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr bool
	}{
		{name: "nil"},
		{name: "already done", err: os.ErrProcessDone},
		{name: "terminated exit", err: &exec.ExitError{}},
		{name: "unexpected", err: errors.New("cleanup failed"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := cleanupProcessError("cleanup", test.err)
			if (err != nil) != test.wantErr {
				t.Fatalf("cleanupProcessError() = %v, want error %t", err, test.wantErr)
			}
		})
	}
}

func TestBoundedStderrRedactsAndBoundsFlood(t *testing.T) {
	secret := "api_key=Q7m9V2p4R8x6L3n5"
	body := secret + strings.Repeat("x", MaxStderrBytes*2)
	stderr := &boundedStderr{}
	stderr.drain(io.NopCloser(strings.NewReader(body)))
	if stderr.total != uint64(len(body)) || len(stderr.retained) != MaxStderrBytes {
		t.Fatalf("stderr bounds = total %d, retained %d", stderr.total, len(stderr.retained))
	}
	summary := stderr.summary(context.Background())
	if strings.Contains(summary, secret) || !strings.Contains(summary, "classifications=secret") ||
		!strings.Contains(summary, "retained=262144") {
		t.Fatalf("stderr summary = %q", summary)
	}
}

func TestBoundedStderrByteCountSaturates(t *testing.T) {
	stderr := &boundedStderr{total: math.MaxUint64 - 1}
	stderr.drain(io.NopCloser(strings.NewReader("overflow")))
	if stderr.total != math.MaxUint64 {
		t.Fatalf("stderr total = %d, want saturation", stderr.total)
	}
}
