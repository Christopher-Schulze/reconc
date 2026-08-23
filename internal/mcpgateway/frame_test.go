package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestProtocolObserverBindsConcurrentRequestsToExactParameters(t *testing.T) {
	tests := []struct {
		name      string
		firstMeta string
	}{
		{name: "legacy", firstMeta: ""},
		{name: "current protocol metadata", firstMeta: `,"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer := newProtocolObserver()
			first, err := observer.begin("tools/call", map[string]any{
				"name": "echo", "arguments": map[string]any{"value": "first"},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.cancel(first)

			type beginResult struct {
				call *pendingProtocolCall
				err  error
			}
			secondReady := make(chan beginResult, 1)
			go func() {
				call, beginErr := observer.begin("tools/call", map[string]any{
					"name": "echo", "arguments": map[string]any{"value": "second"},
				}, nil)
				secondReady <- beginResult{call: call, err: beginErr}
			}()
			select {
			case result := <-secondReady:
				if result.call != nil {
					observer.cancel(result.call)
				}
				t.Fatal("second observed send was not serialized until the first request ID was bound")
			case <-time.After(20 * time.Millisecond):
			}

			firstFrame := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"value":"first"}` + test.firstMeta + `}}`
			if !json.Valid([]byte(firstFrame)) {
				t.Fatalf("invalid first test frame: %s", firstFrame)
			}
			if err := observer.outbound([]byte(firstFrame)); err != nil {
				t.Fatal(err)
			}
			observer.mu.Lock()
			bound := len(observer.byID)
			observer.mu.Unlock()
			if bound != 1 {
				t.Fatalf("bound request IDs = %d, want 1", bound)
			}
			var second *pendingProtocolCall
			select {
			case result := <-secondReady:
				if result.err != nil {
					t.Fatal(result.err)
				}
				second = result.call
			case <-time.After(time.Second):
				t.Fatal("second observed send did not resume after the first request ID was bound")
			}
			defer observer.cancel(second)
			if err := observer.outbound([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"value":"second"}}}`)); err != nil {
				t.Fatal(err)
			}
			if err := observer.inbound([]byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"second"}]}}`)); err != nil {
				t.Fatal(err)
			}
			if err := observer.inbound([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"first"}]}}`)); err != nil {
				t.Fatal(err)
			}
			assertObservedText(t, first, "first")
			assertObservedText(t, second, "second")
		})
	}
}

func TestProtocolObserverTimeoutConsumesPendingCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		observer := newProtocolObserver()
		call, err := observer.begin("tools/list", map[string]any{"cursor": ""}, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		result := make(chan error, 1)
		go func() {
			_, waitErr := observer.wait(ctx, call)
			result <- waitErr
		}()
		synctest.Sleep(time.Second)
		if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("observer timeout = %v", err)
		}
		assertProtocolObserverIdle(t, observer)
		assertProtocolObserverReusable(t, observer)
	})
}

func TestProtocolObserverCancellationConsumesBoundCall(t *testing.T) {
	observer := newProtocolObserver()
	call, err := observer.begin("tools/list", map[string]any{"cursor": "next"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := observer.outbound([]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{"cursor":"next"}}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := observer.wait(ctx, call); !errors.Is(err, context.Canceled) {
		t.Fatalf("observer cancellation = %v", err)
	}
	assertProtocolObserverIdle(t, observer)
	assertProtocolObserverReusable(t, observer)
}

func TestProtocolObserverMalformedResponseCompletesExactlyOnce(t *testing.T) {
	observer := newProtocolObserver()
	completed := 0
	call, err := observer.begin("tools/call", map[string]any{
		"name": "echo", "arguments": map[string]any{},
	}, func() { completed++ })
	if err != nil {
		t.Fatal(err)
	}
	if err := observer.outbound([]byte(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"echo","arguments":{}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := observer.inbound([]byte(`{"jsonrpc":"2.0","id":8}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.wait(t.Context(), call); err == nil || !strings.Contains(err.Error(), "omitted result") {
		t.Fatalf("malformed observed response = %v", err)
	}
	observer.cancel(call)
	if completed != 1 {
		t.Fatalf("completion count = %d, want 1", completed)
	}
	assertProtocolObserverIdle(t, observer)
	assertProtocolObserverReusable(t, observer)
}

func TestProtocolObserverCompletionConsumesStateBeforeConsumerErrors(t *testing.T) {
	tests := []struct {
		name    string
		result  string
		consume func(json.RawMessage) error
	}{
		{
			name:   "decode failure",
			result: `{"tools":"invalid"}`,
			consume: func(raw json.RawMessage) error {
				_, err := decodeToolPage(raw)
				return err
			},
		},
		{
			name:   "SDK mismatch",
			result: `{"tools":[],"nextCursor":"wire"}`,
			consume: func(raw json.RawMessage) error {
				page, err := decodeToolPage(raw)
				if err != nil {
					return err
				}
				if page.NextCursor != "sdk" {
					return errors.New("SDK tool page differs from the strict wire observation")
				}
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer := newProtocolObserver()
			call, err := observer.begin("tools/list", map[string]any{"cursor": test.name}, nil)
			if err != nil {
				t.Fatal(err)
			}
			request := fmt.Sprintf(
				`{"jsonrpc":"2.0","id":9,"method":"tools/list","params":{"cursor":%q}}`,
				test.name,
			)
			if err := observer.outbound([]byte(request)); err != nil {
				t.Fatal(err)
			}
			response := `{"jsonrpc":"2.0","id":9,"result":` + test.result + `}`
			if err := observer.inbound([]byte(response)); err != nil {
				t.Fatal(err)
			}
			raw, err := observer.wait(t.Context(), call)
			if err != nil {
				t.Fatal(err)
			}
			assertProtocolObserverIdle(t, observer)
			if err := test.consume(raw); err == nil {
				t.Fatal("post-observation consumer unexpectedly succeeded")
			}
			assertProtocolObserverReusable(t, observer)
		})
	}
}

func TestStrictFrameWriterRejectsShortWritesWithExactCount(t *testing.T) {
	first := []byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n")
	second := []byte(`{"jsonrpc":"2.0","id":2,"result":{}}` + "\n")
	tests := []struct {
		name       string
		input      []byte
		writeSizes []int
		want       int
	}{
		{name: "first frame", input: first, writeSizes: []int{len(first) / 2}, want: len(first) / 2},
		{
			name: "second frame", input: append(append([]byte(nil), first...), second...),
			writeSizes: []int{-1, len(second) / 2}, want: len(first) + len(second)/2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := &scriptedWriteCloser{writeSizes: test.writeSizes}
			writer := newStrictFrameWriter(output, nil)
			got, err := writer.Write(test.input)
			if got != test.want || !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("Write() = (%d, %v), want (%d, %v)", got, err, test.want, io.ErrShortWrite)
			}
			if next, nextErr := writer.Write(first); next != 0 || !errors.Is(nextErr, io.ErrShortWrite) {
				t.Fatalf("Write() after failure = (%d, %v)", next, nextErr)
			}
		})
	}
}

func TestStrictFrameWriterDoesNotRecountBufferedBytesOnShortWrite(t *testing.T) {
	output := &scriptedWriteCloser{writeSizes: []int{2}}
	writer := newStrictFrameWriter(output, nil)
	partial := []byte(`{"jsonrpc":"2.0","id":1`)
	if got, err := writer.Write(partial); got != len(partial) || err != nil {
		t.Fatalf("buffer partial frame = (%d, %v)", got, err)
	}
	remainder := []byte(`,"result":{}}` + "\n")
	got, err := writer.Write(remainder)
	if got != 0 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("complete buffered frame = (%d, %v), want (0, %v)", got, err, io.ErrShortWrite)
	}
}

func TestFrameAndProtocolIDRejectUnsafeShapes(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "array frame", run: func() error { return validateFrameJSON([]byte(`[]`)) }},
		{name: "scalar frame", run: func() error { return validateFrameJSON([]byte(`1`)) }},
		{name: "embedded newline", run: func() error { return validateFrameJSON([]byte("{}\n")) }},
		{name: "embedded carriage return", run: func() error { return validateFrameJSON([]byte("{}\r")) }},
		{name: "fractional ID", run: func() error { _, err := canonicalProtocolID([]byte(`1.5`)); return err }},
		{
			name: "oversized ID",
			run: func() error {
				_, err := canonicalProtocolID([]byte(`"` + strings.Repeat("x", MaxProtocolIDBytes) + `"`))
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err == nil {
				t.Fatal("unsafe protocol shape was accepted")
			}
		})
	}
	if id, err := canonicalProtocolID([]byte(`1.0`)); err != nil || id != "1" {
		t.Fatalf("integral numeric ID = %q, %v", id, err)
	}
}

func TestParseFrameJSONExtractsEnvelopeWithoutCopying(t *testing.T) {
	body := []byte(` { "params" : {"progressToken":"token","progress":1}, "method":"notifications/progress", "id":7, "result":{"ok":true} } `)
	frame, err := parseFrameJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if frame.method != "notifications/progress" || string(frame.id) != "7" ||
		string(frame.params) != `{"progressToken":"token","progress":1}` ||
		string(frame.result) != `{"ok":true}` {
		t.Fatalf("parsed frame = %#v", frame)
	}
	paramsStart := bytes.Index(body, frame.params)
	if len(frame.params) == 0 || paramsStart < 0 || &frame.params[0] != &body[paramsStart] {
		t.Fatal("parsed params do not alias the validated frame")
	}
}

func TestStrictFrameWriterClearsRetainedBuffer(t *testing.T) {
	output := &scriptedWriteCloser{}
	writer := newStrictFrameWriter(output, nil)
	frame := []byte(`{"jsonrpc":"2.0","id":1,"result":{"secret":"sensitive"}}` + "\n")
	if _, err := writer.Write(frame); err != nil {
		t.Fatal(err)
	}
	retained := writer.pending[:cap(writer.pending)]
	for index, value := range retained {
		if value != 0 {
			t.Fatalf("retained writer byte %d was not cleared", index)
		}
	}
}

type scriptedWriteCloser struct {
	bytes.Buffer
	writeSizes []int
	calls      int
}

func (w *scriptedWriteCloser) Write(input []byte) (int, error) {
	size := len(input)
	if w.calls < len(w.writeSizes) && w.writeSizes[w.calls] >= 0 {
		size = min(size, w.writeSizes[w.calls])
	}
	w.calls++
	return w.Buffer.Write(input[:size])
}

func (*scriptedWriteCloser) Close() error { return nil }

func assertObservedText(t *testing.T, call *pendingProtocolCall, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	raw, err := waitObserved(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != want {
		t.Fatalf("observed response = %s, want text %q", strings.TrimSpace(string(raw)), want)
	}
}

func assertProtocolObserverIdle(t *testing.T, observer *protocolObserver) {
	t.Helper()
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.pending) != 0 || len(observer.pendingMethods) != 0 || len(observer.byID) != 0 {
		t.Fatalf(
			"observer retained state: pending=%d methods=%d ids=%d",
			len(observer.pending), len(observer.pendingMethods), len(observer.byID),
		)
	}
}

func assertProtocolObserverReusable(t *testing.T, observer *protocolObserver) {
	t.Helper()
	call, err := observer.begin("tools/list", map[string]any{"cursor": "reused"}, nil)
	if err != nil {
		t.Fatalf("reuse observer: %v", err)
	}
	observer.cancel(call)
}
