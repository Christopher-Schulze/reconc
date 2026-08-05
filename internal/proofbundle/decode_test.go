package proofbundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestDecodeAcceptsCanonicalAndReorderedObjects(t *testing.T) {
	body, err := MarshalJSON(validProofBundle())
	if err != nil {
		t.Fatal(err)
	}
	crossPlatform := validProofBundle()
	crossPlatform.Build.GOOS, crossPlatform.Build.GOARCH = "windows", "amd64"
	crossPlatform.Digest = digest(crossPlatform)
	crossPlatformBody, err := MarshalJSON(crossPlatform)
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string][]byte{
		"canonical":    body,
		"reordered":    reorderRootObject(t, body),
		"windows CRLF": bytes.ReplaceAll(crossPlatformBody, []byte("\n"), []byte("\r\n")),
	} {
		t.Run(name, func(t *testing.T) {
			bundle, err := Decode(input)
			if err != nil || bundle.Digest == "" {
				t.Fatalf("Decode() = %+v, %v", bundle, err)
			}
		})
	}
}

func TestDecodeRejectsMalformedAndStructurallyInvalidJSON(t *testing.T) {
	body, err := MarshalJSON(validProofBundle())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{name: "truncated", input: body[:len(body)-3], want: "EOF"},
		{name: "invalid UTF-8", input: append([]byte(`{"`), 0xff), want: "UTF-8"},
		{name: "trailing value", input: append(append([]byte{}, body...), []byte(` {}`)...), want: "multiple JSON values"},
		{name: "duplicate nested key", input: bytes.Replace(body, []byte(`"version": "test"`), []byte(`"version": "test", "version": "other"`), 1), want: "duplicate object key"},
		{name: "unknown field", input: bytes.Replace(body, []byte(`"format_version": "1",`), []byte(`"format_version": "1", "unknown": true,`), 1), want: "unknown field"},
		{name: "missing required field", input: bytes.Replace(body, []byte(`"provenance_format": "",`), nil, 1), want: "required field"},
		{name: "null collection", input: bytes.Replace(body, []byte(`"dirty_paths": []`), []byte(`"dirty_paths": null`), 1), want: "null collection"},
		{name: "non-object", input: []byte(`[]`), want: "root value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode(test.input)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode() error = %v, want %q", err, test.want)
			}
			if test.name == "null collection" && !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("null collection classification = %v", err)
			}
			if test.name != "null collection" && !errors.Is(err, ErrMalformedInput) {
				t.Fatalf("malformed classification = %v", err)
			}
		})
	}
}

func TestDecodeFileRejectsOversizedSymlinkAndNonRegularInputs(t *testing.T) {
	root := t.TempDir()
	oversized := filepath.Join(root, "oversized.json")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeFile(oversized); err == nil || !errors.Is(err, ErrMalformedInput) {
		t.Fatalf("oversized error = %v", err)
	}
	if _, err := Decode(bytes.Repeat([]byte("x"), MaxBytes+1)); err == nil || !errors.Is(err, ErrMalformedInput) {
		t.Fatalf("oversized byte input error = %v", err)
	}
	valid := filepath.Join(root, "valid.json")
	body, err := MarshalJSON(validProofBundle())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(valid, body, 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "link.json")
	if err := os.Symlink(valid, symlink); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{symlink, root} {
		if _, err := DecodeFile(path); err == nil || !errors.Is(err, ErrUnsafeInput) {
			t.Fatalf("unsafe input %s error = %v", path, err)
		}
	}
}

func TestVerifyRejectsRecomputedNonCanonicalAndOverBudgetContracts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Bundle)
	}{
		{name: "reordered paths", mutate: func(bundle *Bundle) { bundle.Candidate.DirtyPaths = []string{"z.go", "a.go"} }},
		{name: "non-portable path", mutate: func(bundle *Bundle) { bundle.Candidate.DirtyPaths = []string{`docs\windows.md`} }},
		{name: "too many checks", mutate: func(bundle *Bundle) {
			bundle.Checks = make([]Check, maxItems+1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := validProofBundle()
			test.mutate(bundle)
			bundle.Digest = digest(bundle)
			if err := Verify(bundle); err == nil || !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func reorderRootObject(t *testing.T, body []byte) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	var output bytes.Buffer
	output.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			output.WriteByte(',')
		}
		fmt.Fprintf(&output, "%q:%s", key, fields[key])
	}
	output.WriteByte('}')
	return output.Bytes()
}
