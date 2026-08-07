package cli

import (
	"math"
	"strconv"
	"testing"
	"time"
)

// TestHookRuntimeTimingThresholdStaysInRange keeps an operator value from
// inverting the diagnostic. An unclamped conversion wraps negative, and the
// caller's "greater than zero" guard then reports every hook as slow.
func TestHookRuntimeTimingThresholdStaysInRange(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "unset", raw: "", want: 0},
		{name: "zero disables", raw: "0", want: 0},
		{name: "negative disables", raw: "-5", want: 0},
		{name: "not a number disables", raw: "soon", want: 0},
		{name: "plain value", raw: "250", want: 250 * time.Millisecond},
		{name: "above the cap clamps", raw: strconv.Itoa(maxHookTimingThresholdMS + 1), want: maxHookTimingThresholdMS * time.Millisecond},
		{name: "overflowing value clamps", raw: strconv.Itoa(math.MaxInt), want: maxHookTimingThresholdMS * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RECONC_HOOK_TIMING_THRESHOLD_MS", tc.raw)
			got := hookRuntimeTimingThreshold()
			if got != tc.want {
				t.Fatalf("threshold = %s, want %s", got, tc.want)
			}
			if got < 0 {
				t.Fatalf("threshold went negative: %s", got)
			}
		})
	}
}
