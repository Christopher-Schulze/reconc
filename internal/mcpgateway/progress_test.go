package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
)

func TestUpstreamProgressTokenStrictlyValidatesMetadata(t *testing.T) {
	tests := []struct {
		name        string
		params      string
		want        any
		wantPresent bool
		wantError   bool
	}{
		{name: "absent", params: `{"name":"echo"}`},
		{name: "string", params: `{"_meta":{"progressToken":"token"}}`, want: "token", wantPresent: true},
		{name: "integer", params: `{"_meta":{"progressToken":42}}`, want: int64(42), wantPresent: true},
		{name: "fraction", params: `{"_meta":{"progressToken":1.5}}`, wantError: true},
		{name: "exponent", params: `{"_meta":{"progressToken":1e2}}`, wantError: true},
		{name: "boolean", params: `{"_meta":{"progressToken":true}}`, wantError: true},
		{name: "invalid metadata", params: `{"_meta":[]}`, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, present, err := upstreamProgressToken(json.RawMessage(test.params))
			if (err != nil) != test.wantError {
				t.Fatalf("upstreamProgressToken() error = %v, wantError %t", err, test.wantError)
			}
			if present != test.wantPresent || got != test.want {
				t.Fatalf("upstreamProgressToken() = (%#v, %t), want (%#v, %t)", got, present, test.want, test.wantPresent)
			}
		})
	}
	oversized, err := json.Marshal(map[string]any{
		"_meta": map[string]any{"progressToken": strings.Repeat("x", MaxProgressTokenBytes+1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := upstreamProgressToken(oversized); err == nil {
		t.Fatal("oversized upstream progress token was accepted")
	}
}

func TestNormalizeProgressRejectsUnsupportedOrInvalidEvents(t *testing.T) {
	tests := []struct {
		name      string
		params    string
		want      string
		wantError bool
	}{
		{
			name: "valid", params: `{"progressToken":"internal","progress":1.5,"total":2,"message":"ok"}`,
			want: `{"message":"ok","progress":15e-1,"total":2}`,
		},
		{name: "no token", params: `{"progress":1}`, wantError: true},
		{name: "no progress", params: `{"progressToken":"internal"}`, wantError: true},
		{name: "numeric token", params: `{"progressToken":1,"progress":1}`, wantError: true},
		{name: "negative progress", params: `{"progressToken":"internal","progress":-1}`, wantError: true},
		{name: "negative total", params: `{"progressToken":"internal","progress":1,"total":-1}`, wantError: true},
		{name: "float overflow", params: `{"progressToken":"internal","progress":1e1000}`, wantError: true},
		{name: "unsupported field", params: `{"progressToken":"internal","progress":1,"data":"raw"}`, wantError: true},
		{name: "invalid metadata", params: `{"progressToken":"internal","progress":1,"_meta":[]}`, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeProgress(json.RawMessage(test.params))
			if (err != nil) != test.wantError {
				t.Fatalf("normalizeProgress() error = %v, wantError %t", err, test.wantError)
			}
			if err == nil && string(got.payload) != test.want {
				t.Fatalf("normalizeProgress() payload = %s, want %s", got.payload, test.want)
			}
		})
	}
}

func TestCallProgressEnforcesSequenceAndAggregateBounds(t *testing.T) {
	t.Run("strict sequence", func(t *testing.T) {
		tracker := &callProgress{}
		one, _ := action.ParseDecimal("1")
		two, _ := action.ParseDecimal("2")
		if !tracker.advance(one) || !tracker.advance(two) || tracker.advance(two) {
			t.Fatal("progress sequence did not require a strict increase")
		}
	})

	t.Run("event count", func(t *testing.T) {
		tracker := &callProgress{}
		for index := 0; index < MaxProgressEvents; index++ {
			if reason := tracker.admit(context.Background(), ProgressEvent{FrameBytes: 1}); reason != "" {
				t.Fatalf("event %d rejected with %s", index+1, reason)
			}
		}
		if reason := tracker.admit(context.Background(), ProgressEvent{FrameBytes: 1}); reason != action.ReasonLimitExceeded {
			t.Fatalf("event count overflow reason = %s", reason)
		}
	})

	t.Run("aggregate bytes", func(t *testing.T) {
		tracker := &callProgress{}
		for total := 0; total < MaxProgressBytes; total += MaxProgressEventBytes {
			if reason := tracker.admit(
				context.Background(), ProgressEvent{FrameBytes: MaxProgressEventBytes},
			); reason != "" {
				t.Fatalf("aggregate byte %d rejected with %s", total+MaxProgressEventBytes, reason)
			}
		}
		if reason := tracker.admit(context.Background(), ProgressEvent{FrameBytes: 1}); reason != action.ReasonLimitExceeded {
			t.Fatalf("aggregate overflow reason = %s", reason)
		}
	})

	t.Run("inspection time", func(t *testing.T) {
		tracker := &callProgress{}
		if reason := tracker.charge(ProgressEventTimeout + time.Nanosecond); reason != action.ReasonDeadlineExceeded {
			t.Fatalf("inspection timeout reason = %s", reason)
		}
	})
}

func TestSDKDownstreamRoutesOnlyExactActiveProgressToken(t *testing.T) {
	tests := []struct {
		name      string
		frame     string
		wantEvent bool
	}{
		{
			name:      "matching notification",
			frame:     `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"internal","progress":1}}`,
			wantEvent: true,
		},
		{
			name:  "unknown token",
			frame: `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"other","progress":1}}`,
		},
		{
			name:  "request instead of notification",
			frame: `{"jsonrpc":"2.0","id":1,"method":"notifications/progress","params":{"progressToken":"internal","progress":1}}`,
		},
		{
			name:  "numeric attacker token",
			frame: `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":1,"progress":1}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := make(chan ProgressEvent, 1)
			downstream := &sdkDownstream{progress: map[string]ProgressSink{
				"internal": func(_ context.Context, event ProgressEvent) error {
					events <- event
					return nil
				},
			}}
			downstream.routeProgress([]byte(test.frame))
			select {
			case event := <-events:
				if !test.wantEvent {
					t.Fatalf("unexpected routed event: %s", event.Params)
				}
				if event.FrameBytes != uint64(len(test.frame)) {
					t.Fatalf("frame bytes = %d, want %d", event.FrameBytes, len(test.frame))
				}
			default:
				if test.wantEvent {
					t.Fatal("matching progress notification was not routed")
				}
			}
		})
	}
}

func TestSDKDownstreamStopsRouteAfterSinkFailure(t *testing.T) {
	downstream := &sdkDownstream{progress: map[string]ProgressSink{
		"internal": func(context.Context, ProgressEvent) error { return errors.New("stop") },
	}}
	frame := []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"internal","progress":1}}`)
	downstream.routeProgress(frame)
	downstream.progressMu.Lock()
	_, active := downstream.progress["internal"]
	downstream.progressMu.Unlock()
	if active {
		t.Fatal("failed progress sink remained active")
	}
}
