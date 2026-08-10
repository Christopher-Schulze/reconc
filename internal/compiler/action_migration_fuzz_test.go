package compiler

import (
	"bytes"
	"encoding/json"
	"testing"
)

func FuzzMigrateLockfileBytes(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"format_version":"5"}`),
		[]byte(`{"format_version":"4","$schema":"invalid"}`),
		[]byte(`{"format_version":"99"}`),
		[]byte(`null`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		var payload map[string]interface{}
		if err := decoder.Decode(&payload); err != nil || payload == nil {
			return
		}
		_, _, _ = MigrateLockfile(payload)
	})
}
