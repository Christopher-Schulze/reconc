package cli

import (
	"errors"
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

func TestFirstClassRouteReadinessInspectsEachPlatformOnce(t *testing.T) {
	readiness := newFirstClassRouteReadiness()
	calls := 0
	wantErr := errors.New("unavailable")
	readiness.inspect = func(root, kind string) (hooks.PlatformStatus, error) {
		calls++
		if root != "/repo" || kind != hooks.KindCursor {
			t.Fatalf("inspect(%q, %q)", root, kind)
		}
		return hooks.PlatformStatus{Kind: kind, State: hooks.StateDegraded}, wantErr
	}
	for range 2 {
		report, err := readiness.platform("/repo", hooks.KindCursor)
		if report.Kind != hooks.KindCursor || !errors.Is(err, wantErr) {
			t.Fatalf("cached readiness = %+v, %v", report, err)
		}
	}
	if calls != 1 {
		t.Fatalf("platform inspections = %d, want 1", calls)
	}
}

func BenchmarkFirstClassRouteReadinessReuse(b *testing.B) {
	inspect := func(_, kind string) (hooks.PlatformStatus, error) {
		return hooks.PlatformStatus{Kind: kind, State: hooks.StateConfigured}, nil
	}
	for b.Loop() {
		readiness := newFirstClassRouteReadiness()
		readiness.inspect = inspect
		_, _ = readiness.platform("/repo", hooks.KindCursor)
		_, _ = readiness.platform("/repo", hooks.KindCursor)
		_, _ = readiness.platform("/repo", hooks.KindDevinCLI)
		_, _ = readiness.platform("/repo", hooks.KindDevinCLI)
	}
}
