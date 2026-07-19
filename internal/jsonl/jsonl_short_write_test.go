package jsonl

import (
	"bytes"
	"testing"
)

type chunkWriter struct {
	buffer bytes.Buffer
	chunk  int
}

func (w *chunkWriter) Write(data []byte) (int, error) {
	if len(data) > w.chunk {
		data = data[:w.chunk]
	}
	return w.buffer.Write(data)
}

func TestWriteFullHandlesShortWrites(t *testing.T) {
	writer := &chunkWriter{chunk: 2}
	if err := writeFull(writer, []byte("complete")); err != nil {
		t.Fatal(err)
	}
	if writer.buffer.String() != "complete" {
		t.Fatalf("written bytes = %q", writer.buffer.String())
	}
}
