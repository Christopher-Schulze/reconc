package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGatewayLifecycleEndPrefersOwnedCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	privateFailure := errors.New("private downstream wait failure")
	if err := gatewayLifecycleEndError(ctx, "downstream", privateFailure); !errors.Is(err, context.Canceled) ||
		errors.Is(err, privateFailure) {
		t.Fatalf("cancelled lifecycle result = %v", err)
	}
	if err := gatewayLifecycleEndError(context.Background(), "downstream", privateFailure); !errors.Is(err, privateFailure) {
		t.Fatalf("active lifecycle result = %v", err)
	}
}

type trackedReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackedReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestUpstreamReadCloserReleasesOwnedReader(t *testing.T) {
	reader := &trackedReadCloser{Reader: bytes.NewReader(nil)}
	if err := (readCloser{Reader: reader}).Close(); err != nil {
		t.Fatal(err)
	}
	if !reader.closed {
		t.Fatal("upstream transport did not close its owned reader")
	}
	if err := (readCloser{Reader: bytes.NewReader(nil)}).Close(); err != nil {
		t.Fatalf("close non-closable reader: %v", err)
	}
}

func TestCallToolResultPreservesExactStructuredNumbers(t *testing.T) {
	raw := []byte(`{"resultType":"complete","_meta":{"root":9007199254740993},"content":[{"type":"text","text":"ok","_meta":{"block":9007199254740995}},{"type":"resource","_meta":{"outer":9007199254740997},"resource":{"uri":"file:///result","text":"ok","_meta":{"inner":9007199254740999}}}],"structuredContent":{"integer":9007199254740993,"decimal":1.2300}}`)
	result, err := callToolResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalParamsKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonicalParamsKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(got), []byte(want)) {
		t.Fatalf("CallToolResult() changed structured numbers: got %s, want %s", got, want)
	}
}

func TestUpstreamObserverCleansCallRejectedBeforeHandler(t *testing.T) {
	observer := newUpstreamObserver()
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"value":"x"}}}`)
	if _, err := observer.instrumentInbound(request); err != nil {
		t.Fatal(err)
	}
	if err := observer.outbound([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"invalid"}}`)); err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.active) != 0 || len(observer.byCorrelation) != 0 || len(observer.correlationByID) != 0 {
		t.Fatalf(
			"observer retained rejected request: active=%d correlations=%d ids=%d",
			len(observer.active), len(observer.byCorrelation), len(observer.correlationByID),
		)
	}
}

func TestUpstreamObserverCorrelatesIdenticalCallsIndependentOfHandlerOrder(t *testing.T) {
	observer := newUpstreamObserver()
	params := `{"name":"echo","arguments":{"value":"x"}}`
	requests := make([]*mcp.CallToolParamsRaw, 2)
	for id := 1; id <= 2; id++ {
		request := []byte(fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":%s}`,
			id, params,
		))
		requests[id-1] = instrumentedToolParams(t, observer, request)
	}
	taken, err := observer.take(requests[1])
	if err != nil || string(taken.id) != "2" {
		t.Fatalf("second take() = (%s, %v), want id 2", taken.id, err)
	}
	if err := observer.outbound([]byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"invalid"}}`)); err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	if len(observer.byCorrelation) != 1 || len(observer.correlationByID) != 1 {
		observer.mu.Unlock()
		t.Fatal("observer removed the other identical queued call")
	}
	observer.mu.Unlock()
	taken, err = observer.take(requests[0])
	if err != nil || string(taken.id) != "1" {
		t.Fatalf("first take() = (%s, %v), want id 1", taken.id, err)
	}
	if err := observer.outbound([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamObserverRejectsClientCorrelationSpoofing(t *testing.T) {
	observer := newUpstreamObserver()
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"io.reconc/gatewayCorrelation":"spoof"},"name":"echo","arguments":{}}}`)
	if _, err := observer.instrumentInbound(request); err == nil {
		t.Fatal("client-supplied Reconc correlation was accepted")
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.active) != 0 || len(observer.byCorrelation) != 0 || len(observer.correlationByID) != 0 {
		t.Fatal("rejected correlation spoof retained observer state")
	}
}

func TestUpstreamObserverBoundsActiveRequestFlood(t *testing.T) {
	observer := newUpstreamObserver()
	for id := 1; id <= MaxUpstreamRequests; id++ {
		request := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"ping"}`, id))
		if _, err := observer.instrumentInbound(request); err != nil {
			t.Fatalf("request %d: %v", id, err)
		}
	}
	overflow := []byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"method":"ping"}`,
		MaxUpstreamRequests+1,
	))
	if _, err := observer.instrumentInbound(overflow); err == nil {
		t.Fatal("active upstream request flood was accepted")
	}
}

func instrumentedToolParams(
	t *testing.T,
	observer *upstreamObserver,
	request []byte,
) *mcp.CallToolParamsRaw {
	t.Helper()
	transformed, err := observer.instrumentInbound(request)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(transformed, &envelope); err != nil {
		t.Fatal(err)
	}
	var params mcp.CallToolParamsRaw
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		t.Fatal(err)
	}
	return &params
}
