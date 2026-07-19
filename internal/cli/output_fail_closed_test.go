package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type failingOutputWriter struct{}

func (failingOutputWriter) Write([]byte) (int, error) {
	return 0, errors.New("output unavailable")
}

func TestTextCommandSurfacesOutputFailure(t *testing.T) {
	var stderr bytes.Buffer
	err := Run([]string{"preset", "list"}, "test", failingOutputWriter{}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "output unavailable") {
		t.Fatalf("expected output failure, got %v", err)
	}
}
