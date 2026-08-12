package mcpgateway

import (
	"bytes"
	"context"
	"testing"
)

func FuzzValidateToolContract(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"name":"echo","description":"Echo a value.","inputSchema":{"type":"object"}}`),
		[]byte(`{"name":"echo","annotations":{"readOnlyHint":true},"inputSchema":{"type":"object"},"outputSchema":{"type":"object"}}`),
		[]byte(`{"name":"echo","inputSchema":{"type":"array"}}`),
		[]byte(`{"name":"echo","name":"other","inputSchema":{"type":"object"}}`),
		{0xff},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxToolMetadataBytes {
			t.Skip()
		}
		first, err := validateToolContract(context.Background(), input)
		if err != nil {
			return
		}
		second, err := validateToolContract(context.Background(), first.Canonical)
		if err != nil || first.Name != second.Name || first.ContractDigest != second.ContractDigest ||
			!bytes.Equal(first.Canonical, second.Canonical) {
			t.Fatalf("accepted tool contract did not canonicalize deterministically: %v", err)
		}
	})
}

func FuzzDecodeToolPage(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"tools":[]}`),
		[]byte(`{"tools":[{"name":"echo","inputSchema":{"type":"object"}}],"nextCursor":"next"}`),
		[]byte(`{"tools":[null]}`),
		[]byte(`{"tools":[],"nextCursor":""}`),
		[]byte(`[]`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxProtocolFrameBytes {
			t.Skip()
		}
		first, err := decodeToolPage(input)
		if err != nil {
			return
		}
		second, err := decodeToolPage(input)
		if err != nil || first.NextCursor != second.NextCursor || len(first.Tools) != len(second.Tools) {
			t.Fatalf("accepted tool page decoded nondeterministically: %v", err)
		}
		for index := range first.Tools {
			if !bytes.Equal(first.Tools[index], second.Tools[index]) {
				t.Fatalf("accepted tool page changed tool %d", index)
			}
		}
	})
}
