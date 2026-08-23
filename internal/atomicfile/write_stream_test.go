package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteStreamPublishesBoundedBytesAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteStream(path, strings.NewReader("new payload"), 11, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "new payload" {
		t.Fatalf("published body = %q, %v", body, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("published mode = %v, %v", info, err)
	}
}

func TestWriteStreamLeavesCurrentFileOnReadFailureOrOverflow(t *testing.T) {
	for _, test := range []struct {
		name   string
		source io.Reader
		limit  int64
	}{
		{name: "read failure", source: io.MultiReader(strings.NewReader("partial"), errorReader{}), limit: 32},
		{name: "overflow", source: strings.NewReader("too-long"), limit: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "binary")
			if err := os.WriteFile(path, []byte("old"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := WriteStream(path, test.source, test.limit, 0o755); err == nil {
				t.Fatal("stream failure unexpectedly succeeded")
			}
			body, err := os.ReadFile(path)
			if err != nil || string(body) != "old" {
				t.Fatalf("failed stream changed current body = %q, %v", body, err)
			}
		})
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("injected read failure")
}
