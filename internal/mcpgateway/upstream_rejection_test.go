package mcpgateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestUpstreamRequestFailuresReturnErrorsAndKeepReading(t *testing.T) {
	observer := newUpstreamObserver()
	if _, err := observer.instrumentInbound([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"_meta":{"io.reconc/gatewayCorrelation":"spoof"},"name":"echo","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":[]}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"value":"valid"}}}`,
	}, "\n") + "\n"
	output := &scriptedWriteCloser{}
	writer := newStrictFrameWriter(output, observer.outboundFrame)
	reader := newStrictTransformingFrameReader(
		io.NopCloser(strings.NewReader(input)), observer.instrumentInboundFrame,
		func(frame validatedFrame, transformErr error) (bool, error) {
			return writeUpstreamRequestError(writer, frame, transformErr)
		},
	)
	forwarded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	frames := bytes.Split(bytes.TrimSpace(forwarded), []byte{'\n'})
	if len(frames) != 1 {
		t.Fatalf("forwarded frames = %d, want only the later valid request", len(frames))
	}
	valid, err := parseFrameJSON(frames[0])
	if err != nil || string(valid.id) != "4" || !bytes.Contains(valid.params, []byte(upstreamCorrelationMetaKey)) {
		t.Fatalf("forwarded valid request = %s, %v", frames[0], err)
	}
	assertUpstreamRequestErrors(t, output.Bytes(), map[string]int64{
		"1": jsonrpc.CodeInvalidRequest,
		"2": jsonrpc.CodeInvalidParams,
		"3": jsonrpc.CodeInvalidParams,
	})

	observer.mu.Lock()
	active := len(observer.active)
	correlations := len(observer.byCorrelation)
	ids := len(observer.correlationByID)
	observer.mu.Unlock()
	if active != 2 || correlations != 1 || ids != 1 {
		t.Fatalf("observer state after rejections = active %d correlations %d ids %d", active, correlations, ids)
	}
	for _, id := range []string{"4", "1"} {
		response := []byte(`{"jsonrpc":"2.0","id":` + id + `,"result":{}}`)
		if err := observer.outbound(response); err != nil {
			t.Fatal(err)
		}
	}
	assertUpstreamObserverIdle(t, observer)
}

func TestUpstreamOversizeTransformIsLocalAndStreamCorruptionIsFatal(t *testing.T) {
	prefix := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"echo","arguments":{"a":"`
	middle := `","b":"`
	tail := `","c":"`
	suffix := `"}}}`
	payloadBytes := MaxProtocolFrameBytes - 1 - len(prefix) - len(middle) - len(tail) - len(suffix)
	firstBytes := payloadBytes / 3
	secondBytes := payloadBytes / 3
	thirdBytes := payloadBytes - firstBytes - secondBytes
	oversizedTransform := prefix + strings.Repeat("x", firstBytes) + middle +
		strings.Repeat("y", secondBytes) + tail + strings.Repeat("z", thirdBytes) + suffix
	valid := `{"jsonrpc":"2.0","id":6,"method":"ping","params":{}}`
	observer := newUpstreamObserver()
	output := &scriptedWriteCloser{}
	writer := newStrictFrameWriter(output, observer.outboundFrame)
	reader := newStrictTransformingFrameReader(
		io.NopCloser(strings.NewReader(oversizedTransform+"\n"+valid+"\n")),
		observer.instrumentInboundFrame,
		func(frame validatedFrame, transformErr error) (bool, error) {
			return writeUpstreamRequestError(writer, frame, transformErr)
		},
	)
	forwarded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(forwarded) != valid+"\n" {
		t.Fatalf("later valid frame was not isolated from oversize rejection: %d bytes", len(forwarded))
	}
	assertUpstreamRequestErrors(t, output.Bytes(), map[string]int64{"5": jsonrpc.CodeInvalidParams})
	if err := observer.outbound([]byte(`{"jsonrpc":"2.0","id":6,"result":{}}`)); err != nil {
		t.Fatal(err)
	}
	assertUpstreamObserverIdle(t, observer)

	corrupt := newStrictTransformingFrameReader(
		io.NopCloser(strings.NewReader("{invalid}\n"+valid+"\n")),
		observer.instrumentInboundFrame,
		func(frame validatedFrame, transformErr error) (bool, error) {
			return writeUpstreamRequestError(writer, frame, transformErr)
		},
	)
	buffer := make([]byte, 128)
	if _, err := corrupt.Read(buffer); err == nil {
		t.Fatal("corrupt upstream stream was accepted")
	} else if _, repeated := corrupt.Read(buffer); repeated == nil ||
		!errors.Is(repeated, err) && repeated.Error() != err.Error() {
		t.Fatalf("fatal stream error was not latched: first=%v repeated=%v", err, repeated)
	}

	fatalObserver := newUpstreamObserver()
	fatalObserver.next = ^uint64(0)
	fatalOutput := &scriptedWriteCloser{}
	fatalWriter := newStrictFrameWriter(fatalOutput, fatalObserver.outboundFrame)
	fatalReader := newStrictTransformingFrameReader(
		io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"echo","arguments":{}}}`+"\n"+valid+"\n")),
		fatalObserver.instrumentInboundFrame,
		func(frame validatedFrame, transformErr error) (bool, error) {
			return writeUpstreamRequestError(fatalWriter, frame, transformErr)
		},
	)
	if forwarded, err := io.ReadAll(fatalReader); err == nil || len(forwarded) != 0 || fatalOutput.Len() != 0 {
		t.Fatalf("fatal correlation failure = forwarded %d, output %d, error %v", len(forwarded), fatalOutput.Len(), err)
	}
	assertUpstreamObserverIdle(t, fatalObserver)
}

func TestStrictFrameWriterSerializesLocalErrorWithoutCorrelationCleanup(t *testing.T) {
	output := &scriptedWriteCloser{}
	observed := 0
	writer := newStrictFrameWriter(output, func(validatedFrame) error {
		observed++
		return nil
	})
	partial := []byte(`{"jsonrpc":"2.0","id":9`)
	if count, err := writer.Write(partial); err != nil || count != len(partial) {
		t.Fatalf("buffer SDK response = %d, %v", count, err)
	}
	local := []byte(`{"jsonrpc":"2.0","id":8,"error":{"code":-32602,"message":"invalid request"}}`)
	if err := writer.writeUnobservedFrame(local); err != nil {
		t.Fatal(err)
	}
	remainder := []byte(`,"result":{}}` + "\n")
	if count, err := writer.Write(remainder); err != nil || count != len(remainder) {
		t.Fatalf("complete SDK response = %d, %v", count, err)
	}
	want := append(append(append([]byte(nil), local...), '\n'), partial...)
	want = append(want, remainder...)
	if !bytes.Equal(output.Bytes(), want) || observed != 1 {
		t.Fatalf("serialized output = %q, observed=%d", output.Bytes(), observed)
	}
}

func assertUpstreamRequestErrors(t testing.TB, body []byte, want map[string]int64) {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(body), []byte{'\n'})
	if len(lines) != len(want) {
		t.Fatalf("request error frames = %d, want %d: %s", len(lines), len(want), body)
	}
	for _, line := range lines {
		var response struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Error   struct {
				Code    int64  `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatal(err)
		}
		id := string(response.ID)
		if response.JSONRPC != "2.0" || response.Error.Code != want[id] || response.Error.Message == "" || len(line)+1 > MaxProtocolFrameBytes {
			t.Fatalf("request error = %s", line)
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("missing request errors: %v", want)
	}
}

func assertUpstreamObserverIdle(t testing.TB, observer *upstreamObserver) {
	t.Helper()
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.active) != 0 || len(observer.byCorrelation) != 0 || len(observer.correlationByID) != 0 {
		t.Fatalf("observer retained state: active=%d correlations=%d ids=%d", len(observer.active), len(observer.byCorrelation), len(observer.correlationByID))
	}
}
