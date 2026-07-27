package grokacp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type grokWriteCloser struct {
	io.Writer
}

func (grokWriteCloser) Close() error { return nil }

func TestACPClientServerMethodErrorAndTerminalState(t *testing.T) {
	var output bytes.Buffer
	client := &acpClient{
		writer:  grokWriteCloser{Writer: &output},
		pending: map[string]chan rpcOutcome{"1": make(chan rpcOutcome, 1)},
		done:    make(chan struct{}),
	}
	if err := client.writeServerMethodError(json.RawMessage("7"), "workspace/read"); err != nil {
		t.Fatalf("writeServerMethodError: %v", err)
	}
	var message rpcMessage
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &message); err != nil {
		t.Fatalf("decode server-method response: %v", err)
	}
	if string(message.ID) != "7" || message.Error == nil || message.Error.Code != -32601 ||
		!strings.Contains(message.Error.Message, "workspace/read") {
		t.Fatalf("unexpected server-method response: %+v", message)
	}

	client.removePending("1")
	if len(client.pending) != 0 {
		t.Fatalf("pending request was not removed: %v", client.pending)
	}
	if err := client.terminalError(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty terminal error = %v", err)
	}
	client.finalErr = errors.New("stream failed")
	if err := client.terminalError(); err == nil || err.Error() != "stream failed" {
		t.Fatalf("stored terminal error = %v", err)
	}
}

func TestInspectJSONRunsBoundedRealProcess(t *testing.T) {
	root := t.TempDir()
	body, err := InspectJSON(context.Background(), root, "/bin/echo")
	if err != nil {
		t.Fatalf("InspectJSON(echo): %v", err)
	}
	for _, expected := range []string{"--cwd", root, "inspect", "--json"} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("inspect output omitted %q: %q", expected, body)
		}
	}

	if _, err := InspectJSON(context.Background(), root, filepath.Join(root, "missing-grok")); err == nil {
		t.Fatal("missing Grok binary was accepted")
	}
}

func TestInspectJSONPreservesStdoutAndStderrFailure(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "grok-fixture")
	body := []byte("#!/bin/sh\nprintf 'partial-output'\nprintf 'diagnostic' >&2\nexit 7\n")
	if err := os.WriteFile(script, body, 0o700); err != nil {
		t.Fatalf("write process fixture: %v", err)
	}
	stdout, err := InspectJSON(context.Background(), root, script)
	if err == nil || !strings.Contains(err.Error(), "diagnostic") {
		t.Fatalf("expected stderr-enriched process failure, got %v", err)
	}
	if string(stdout) != "partial-output" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestRunRejectsInvalidOptionsBeforeSpawningAgent(t *testing.T) {
	for _, test := range []struct {
		name    string
		options Options
		err     string
	}{
		{name: "negative continuation budget", options: Options{MaxContinuations: -1, Prompt: "work"}, err: "at least 1"},
		{name: "empty prompt", options: Options{Prompt: " \n "}, err: "non-empty"},
		{name: "oversized prompt", options: Options{Prompt: strings.Repeat("x", maxPromptBytes+1)}, err: "prompt exceeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Run(context.Background(), test.options)
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("expected %q error, got %v", test.err, err)
			}
		})
	}
}
