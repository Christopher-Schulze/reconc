package grokacp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
)

func TestACPClientKeepsTerminalErrorForFutureRequests(t *testing.T) {
	writer := &bufferWriteCloser{}
	client := newACPClient(strings.NewReader(""), writer, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := client.request(ctx, "initialize", map[string]interface{}{}, nil); !errors.Is(err, io.EOF) {
		t.Fatalf("first request error = %v, want EOF", err)
	}
	start := time.Now()
	if err := client.request(ctx, "session/new", map[string]interface{}{}, nil); !errors.Is(err, io.EOF) {
		t.Fatalf("future request error = %v, want EOF", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("future request waited after terminal EOF: %s", elapsed)
	}
}

func TestSharedCappedOutputRetainsPrefixAndFlagsOverflow(t *testing.T) {
	output, err := boundedexec.NewBuffer(4)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := output.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("write = %d, %v", n, err)
	}
	if got := string(output.Bytes()); got != "abcd" {
		t.Fatalf("capped bytes = %q", got)
	}
	if !output.Truncated() {
		t.Fatal("overflow was not recorded")
	}
}

func TestACPClientReturnsFinalResponseBeforeEOF(t *testing.T) {
	reader, source := io.Pipe()
	writer := &signalingWriteCloser{wrote: make(chan struct{})}
	client := newACPClient(reader, writer, nil)
	go func() {
		<-writer.wrote
		_, _ = io.WriteString(source, `{"jsonrpc":"2.0","id":1,"result":{"sessionId":"final"}}`+"\n")
		_ = source.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := client.request(ctx, "session/new", map[string]interface{}{}, &result); err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "final" {
		t.Fatalf("final response = %+v", result)
	}
}

func TestACPClientCompletesShortWrites(t *testing.T) {
	writer := &shortWriteCloser{maxWrite: 3}
	client := &acpClient{writer: writer}
	if err := client.writeJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	}); err != nil {
		t.Fatal(err)
	}
	if got := writer.String(); !strings.HasSuffix(got, "\n") ||
		!strings.Contains(got, `"method":"initialize"`) {
		t.Fatalf("incomplete ACP request: %q", got)
	}
}

type bufferWriteCloser struct {
	bytes.Buffer
}

func (*bufferWriteCloser) Close() error {
	return nil
}

type signalingWriteCloser struct {
	mu    sync.Mutex
	wrote chan struct{}
	once  sync.Once
}

func (w *signalingWriteCloser) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.once.Do(func() { close(w.wrote) })
	return len(data), nil
}

func (*signalingWriteCloser) Close() error {
	return nil
}

type shortWriteCloser struct {
	bytes.Buffer
	maxWrite int
}

func (w *shortWriteCloser) Write(data []byte) (int, error) {
	if len(data) > w.maxWrite {
		data = data[:w.maxWrite]
	}
	return w.Buffer.Write(data)
}

func (*shortWriteCloser) Close() error {
	return nil
}
