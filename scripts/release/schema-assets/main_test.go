package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/schema"
)

func TestRunListsEveryReleaseSchema(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"list-release"}, &output); err != nil {
		t.Fatalf("run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != len(schema.Contracts()) {
		t.Fatalf("release schema rows = %d, want %d", len(lines), len(schema.Contracts()))
	}
	if !sort.StringsAreSorted(lines) {
		t.Fatal("release schema rows are not sorted")
	}
	for _, line := range lines {
		if fields := strings.Split(line, "\t"); len(fields) != 2 || fields[0] == "" || fields[1] == "" {
			t.Errorf("malformed release schema row %q", line)
		}
	}
}

func TestRunWritesMachineReadableRegistry(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"json"}, &output); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got []schema.Contract
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode registry: %v", err)
	}
	want := schema.Contracts()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registry JSON differs from typed registry")
	}
}

func TestRunRejectsInvalidCommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing"},
		{name: "extra", args: []string{"json", "extra"}},
		{name: "unknown", args: []string{"unknown"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := run(test.args, &bytes.Buffer{}); err == nil {
				t.Fatal("run accepted invalid command")
			}
		})
	}
}

func TestReadRegisteredSchemaRejectsLocalContractDrift(t *testing.T) {
	const canonicalURL = "https://example.test/schema.json"
	tests := []struct {
		name       string
		body       []byte
		defaultURL string
		badDigest  bool
		want       string
	}{
		{
			name:       "digest",
			body:       []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://example.test/schema.json","type":"object"}`),
			defaultURL: canonicalURL, badDigest: true, want: "digest",
		},
		{
			name:       "identity",
			body:       []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://example.test/schema.json","type":"object"}`),
			defaultURL: "https://example.test/other.json", want: "$id",
		},
		{
			name:       "meta schema",
			body:       []byte(`{"$schema":"http://json-schema.org/draft-07/schema#","$id":"https://example.test/schema.json","type":"object"}`),
			defaultURL: canonicalURL, want: "meta-schema",
		},
		{
			name: "malformed JSON", body: []byte(`{"$schema":`),
			defaultURL: canonicalURL, want: "decode",
		},
		{
			name:       "duplicate object name",
			body:       []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://example.test/schema.json","type":"object","type":"array"}`),
			defaultURL: canonicalURL, want: "duplicate object name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			localPath := filepath.Join("schemas", "v1", "test.schema.json")
			path := filepath.Join(root, localPath)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, test.body, 0o644); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(test.body)
			registeredDigest := fmt.Sprintf("%x", digest)
			if test.badDigest {
				registeredDigest = strings.Repeat("0", 64)
			}
			contract := schema.Contract{
				LocalPath: filepath.ToSlash(localPath), DefaultURL: test.defaultURL, SHA256: registeredDigest,
			}
			if _, err := readRegisteredSchema(root, contract); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readRegisteredSchema() = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyPublishedRequiresExactBoundedBytes(t *testing.T) {
	root := t.TempDir()
	var published []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/schema.json" {
			http.NotFound(response, request)
			return
		}
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(published)
	}))
	t.Cleanup(server.Close)
	local := []byte(fmt.Sprintf("{\"$schema\":\"https://json-schema.org/draft/2020-12/schema\",\"$id\":%q}\n", server.URL+"/schema.json"))
	published = append([]byte(nil), local...)
	localPath := filepath.Join("schemas", "v1", "test.schema.json")
	if err := os.MkdirAll(filepath.Join(root, "schemas", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, localPath), local, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(local)
	contract := schema.Contract{
		LocalPath: localPath, DefaultURL: server.URL + "/schema.json",
		SHA256: fmt.Sprintf("%x", digest),
	}
	client := server.Client()
	client.CheckRedirect = publicationClient().CheckRedirect
	if err := verifyPublished(context.Background(), client, root, []schema.Contract{contract}); err != nil {
		t.Fatalf("verify exact published bytes: %v", err)
	}

	published = []byte("different")
	if err := verifyPublished(context.Background(), client, root, []schema.Contract{contract}); err == nil || !strings.Contains(err.Error(), "bytes differ") {
		t.Fatalf("mismatched remote bytes error = %v", err)
	}
	contract.SHA256 = strings.Repeat("0", 64)
	if err := verifyPublished(context.Background(), client, root, []schema.Contract{contract}); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("mismatched local digest error = %v", err)
	}
}

func TestVerifyPublishedRejectsHTTPFailureRedirectAndOversize(t *testing.T) {
	tests := []struct {
		name   string
		serve  func(http.ResponseWriter, *http.Request)
		needle string
	}{
		{name: "status", serve: func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusNotFound)
		}, needle: "HTTP 404"},
		{name: "redirect", serve: func(response http.ResponseWriter, request *http.Request) {
			http.Redirect(response, request, "/mutable", http.StatusFound)
		}, needle: "redirects are forbidden"},
		{name: "oversize", serve: func(response http.ResponseWriter, _ *http.Request) {
			_, _ = response.Write(bytes.Repeat([]byte("x"), maxPublishedSchemaBytes+1))
		}, needle: "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(test.serve))
			t.Cleanup(server.Close)
			root := t.TempDir()
			localPath := "schema.json"
			local := []byte(fmt.Sprintf("{\"$schema\":\"https://json-schema.org/draft/2020-12/schema\",\"$id\":%q}\n", server.URL))
			if err := os.WriteFile(filepath.Join(root, localPath), local, 0o644); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(local)
			contract := schema.Contract{LocalPath: localPath, DefaultURL: server.URL, SHA256: fmt.Sprintf("%x", digest)}
			client := server.Client()
			client.CheckRedirect = publicationClient().CheckRedirect
			err := verifyPublished(context.Background(), client, root, []schema.Contract{contract})
			if err == nil || !strings.Contains(err.Error(), test.needle) {
				t.Fatalf("verifyPublished() = %v, want %q", err, test.needle)
			}
		})
	}
	if err := verifyPublished(context.Background(), nil, t.TempDir(), nil); err == nil {
		t.Fatal("nil publication client was accepted")
	}
}
