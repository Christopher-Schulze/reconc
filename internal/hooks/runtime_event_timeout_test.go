package hooks

import (
	"testing"
	"time"
)

func TestRuntimeEventTimeoutFollowsTheRouteBudget(t *testing.T) {
	for event, want := range map[string]time.Duration{
		"claude-pre-tool-use":  10 * time.Second,
		"claude-post-tool-use": 5 * time.Second,
		"claude-stop":          30 * time.Second,
	} {
		if got, ok := RuntimeEventTimeout(event); !ok || got != want {
			t.Errorf("RuntimeEventTimeout(%q) = %s, %t; want %s", event, got, ok, want)
		}
	}
	if got, ok := RuntimeEventTimeout("no-such-runtime-event"); ok {
		t.Fatalf("unknown runtime event has a budget: %s", got)
	}
}
