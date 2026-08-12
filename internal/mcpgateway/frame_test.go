package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
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
