package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

func TestHookWorkerProcessesOrderedRequestsAndShutdown(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	frames := []hookWorkerRequest{
		{FormatVersion: 1, Type: "ping", ID: "ping-1"},
		{
			FormatVersion: 1,
			Type:          "request",
			ID:            "request-1",
			Event:         "claude-session-start",
			Repo:          repo,
			Payload:       json.RawMessage(`{"session_id":"worker-session"}`),
		},
		{
			FormatVersion: 1,
			Type:          "request",
			ID:            "request-2",
			Event:         "claude-session-end",
			Repo:          repo,
			Payload:       json.RawMessage(`{"session_id":"worker-session"}`),
		},
		{FormatVersion: 1, Type: "shutdown", ID: "shutdown-1"},
	}
	var input bytes.Buffer
	encoder := json.NewEncoder(&input)
	for _, frame := range frames {
		if err := encoder.Encode(frame); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := runHookWorker(nil, &input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for index, wantID := range []string{"ping-1", "request-1", "request-2", "shutdown-1"} {
		var response hookWorkerResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("decode response %d: %v", index, err)
		}
		if response.ID != wantID || response.Code != 0 || response.Error != "" {
			t.Fatalf("response %d = %+v, want id=%q code=0", index, response, wantID)
		}
		if index == len(frames)-1 && response.Type != "shutdown" {
			t.Fatalf("shutdown response type=%q", response.Type)
		}
	}
}

func TestHookWorkerProtocolErrorIsFramedAndWorkerContinues(t *testing.T) {
	input := strings.NewReader(
		`{"format_version":2,"type":"ping","id":"old"}` + "\n" +
			`{"format_version":1,"type":"ping","id":"current"}` + "\n",
	)
	var output bytes.Buffer
	if err := runHookWorker(nil, input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var first, second hookWorkerResponse
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if first.Type != "error" || first.ID != "old" || !strings.Contains(first.Error, "format_version") {
		t.Fatalf("protocol error response=%+v", first)
	}
	if second.Type != "response" || second.ID != "current" || second.Code != 0 {
		t.Fatalf("worker did not continue after request error: %+v", second)
	}
}

func TestReadHookWorkerFrameRejectsBoundAndEncodingFailures(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		limit int
		want  string
	}{
		{name: "oversized", input: []byte("12345\n"), limit: 4, want: "bounded protocol limit"},
		{name: "invalid UTF-8", input: []byte{0xff, '\n'}, limit: 4, want: "valid UTF-8"},
		{name: "truncated", input: []byte("{}"), limit: 4, want: "truncated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := readHookWorkerFrameLimit(bufio.NewReader(bytes.NewReader(test.input)), test.limit)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
	_, err := readHookWorkerFrameLimit(bufio.NewReader(strings.NewReader("")), 4)
	if err != io.EOF {
		t.Fatalf("empty stream error=%v, want EOF", err)
	}
}

func TestDecodeHookWorkerRequestRejectsUnknownFieldsAndInvalidTokens(t *testing.T) {
	for _, frame := range []string{
		`{"format_version":1,"type":"ping","id":"ok","extra":true}`,
		`{"format_version":1,"type":"ping","id":"bad id"}`,
		`{"format_version":1,"type":"request","id":"ok","event":"x","repo":".","payload":null}`,
	} {
		if _, err := decodeHookWorkerRequest([]byte(frame)); err == nil {
			t.Fatalf("invalid frame accepted: %s", frame)
		}
	}
}

func FuzzDecodeHookWorkerRequest(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"format_version":1,"type":"ping","id":"seed"}`),
		[]byte(`{"format_version":2,"type":"request","id":"old","event":"x","repo":".","payload":{}}`),
		{0xff, 0x00, '{', '}'},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, frame []byte) {
		_, _ = decodeHookWorkerRequest(frame)
	})
}

func TestHookWorkerRootCacheRefreshesReplacedRepositoryIdentity(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := hookWorkerRootCache{roots: make(map[string]agentsession.ResolvedRepoRoot)}
	first, err := cache.resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	oldRepo := filepath.Join(parent, "replaced-repo")
	if err := os.Rename(repo, oldRepo); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := first.Revalidate(); err == nil {
		t.Fatal("replaced repository retained the old resolved identity")
	}
	second, err := cache.resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	if second.Path() != first.Path() {
		t.Fatalf("canonical path drifted across same-path replacement: first=%q second=%q", first.Path(), second.Path())
	}
	if err := second.Revalidate(); err != nil {
		t.Fatalf("refreshed identity did not revalidate: %v", err)
	}
}
