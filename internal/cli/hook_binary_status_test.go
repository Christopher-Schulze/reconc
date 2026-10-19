package cli

import (
	"bytes"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

func TestHookBinaryStatusPropagatesOutputFailure(t *testing.T) {
	for _, binary := range []hooks.WrapperBinaryStatus{{Path: "/selected/reconc"}, {Error: "unavailable"}} {
		if err := writeHookBinaryStatus(failingWriter{}, &binary); err == nil {
			t.Fatal("status concealed a broken output writer")
		}
		var output bytes.Buffer
		if err := writeHookBinaryStatus(&output, &binary); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), binary.Path+binary.Error) {
			t.Fatalf("status lost the selected path or error: %q", output.String())
		}
	}
}
