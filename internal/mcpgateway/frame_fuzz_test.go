package mcpgateway

import (
	"bytes"
	"io"
	"testing"
)

func FuzzValidateFrameJSON(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`),
		[]byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`),
		[]byte(`{"id":1,"id":2}`),
		[]byte(`[]`),
		{0xff},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, frame []byte) {
		if len(frame) > 1<<20 {
			t.Skip()
		}
		if err := validateFrameJSON(frame); err != nil {
			return
		}
		input := append(append([]byte(nil), frame...), '\n')
		reader := newStrictFrameReader(io.NopCloser(bytes.NewReader(input)), nil)
		read, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("validated frame failed strict reader: %v", err)
		}
		if !bytes.Equal(read, input) {
			t.Fatalf("strict reader changed validated frame")
		}
	})
}

func FuzzProtocolCorrelationCanonicalization(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"name":"echo","arguments":{}}`),
		[]byte(`{"arguments":{"value":1},"name":"echo","_meta":{"progressToken":1}}`),
		[]byte(`{"name":"echo","name":"other"}`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<20 {
			t.Skip()
		}
		first, err := correlationParamsKey(raw)
		if err != nil {
			return
		}
		second, err := correlationParamsKey(raw)
		if err != nil || first != second {
			t.Fatalf("protocol correlation is nondeterministic: %q %q %v", first, second, err)
		}
	})
}
