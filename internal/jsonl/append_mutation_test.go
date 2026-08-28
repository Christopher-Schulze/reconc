package jsonl

import (
	"bytes"
	"os"
	"testing"
)

func TestAppendDoesNotMutateCallerSlice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		record string
		want   string
	}{
		{name: "plain", record: "hello", want: "hello\n"},
		{name: "trailing newline", record: "hello\n", want: "hello\n"},
		{name: "trailing CRLF", record: "hello\r\n", want: "hello\n"},
		{name: "empty", record: "", want: "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backing := append([]byte(test.record), bytes.Repeat([]byte{0xa5}, 64)...)
			record := backing[:len(test.record)]
			before := bytes.Clone(backing)
			path := t.TempDir() + "/events.jsonl"

			if err := Append(path, record, Policy{MaxBytes: 2048, MaxArchives: 1}); err != nil {
				t.Fatalf("append: %v", err)
			}
			if !bytes.Equal(backing, before) {
				t.Fatal("Append mutated the caller-owned record backing array")
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if !bytes.Equal(body, []byte(test.want)) {
				t.Fatalf("file body = %q, want %q", body, test.want)
			}
		})
	}
}
