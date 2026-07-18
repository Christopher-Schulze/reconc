//go:build (darwin || linux) && (amd64 || arm64)

package hooks

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGrokWrapperFailsClosedAndPassesOnlyExactDecisionJSON(t *testing.T) {
	const fallback = `{"decision":"deny","reason":"Reconc could not evaluate this Grok tool call. Repair the Reconc binary or hook installation before retrying."}` + "\n"
	tests := []struct {
		name       string
		binaryBody string
		want       string
	}{
		{name: "missing binary", want: fallback},
		{name: "broken binary", binaryBody: "#!/bin/sh\nexit 2\n", want: fallback},
		{name: "empty output", binaryBody: "#!/bin/sh\nexit 0\n", want: fallback},
		{name: "invalid output", binaryBody: "#!/bin/sh\nprintf 'not-json\\n'\n", want: fallback},
		{name: "unquoted reason", binaryBody: "#!/bin/sh\nprintf '%s\\n' '{\"decision\":\"deny\",\"reason\":blocked}'\n", want: fallback},
		{name: "extra field", binaryBody: "#!/bin/sh\nprintf '%s\\n' '{\"decision\":\"deny\",\"reason\":\"blocked\",\"extra\":\"x\"}'\n", want: fallback},
		{name: "multiline output", binaryBody: "#!/bin/sh\nprintf '%s\\n%s\\n' '{\"decision\":\"allow\"}' 'junk'\n", want: fallback},
		{name: "raw tab", binaryBody: "#!/bin/sh\nprintf '{\"decision\":\"deny\",\"reason\":\"bad\\tcontrol\"}\\n'\n", want: fallback},
		{name: "raw carriage return", binaryBody: "#!/bin/sh\nprintf '{\"decision\":\"deny\",\"reason\":\"bad\\rcontrol\"}\\n'\n", want: fallback},
		{name: "invalid escape", binaryBody: "#!/bin/sh\nprintf '%s\\n' '{\"decision\":\"deny\",\"reason\":\"bad\\q\"}'\n", want: fallback},
		{name: "exact allow", binaryBody: "#!/bin/sh\nprintf '{\"decision\":\"allow\"}\\n'\n", want: `{"decision":"allow"}` + "\n"},
		{name: "exact deny", binaryBody: "#!/bin/sh\nprintf '{\"decision\":\"deny\",\"reason\":\"blocked\"}\\n'\n", want: `{"decision":"deny","reason":"blocked"}` + "\n"},
		{name: "escaped deny", binaryBody: "#!/bin/sh\nprintf '%s\\n' '{\"decision\":\"deny\",\"reason\":\"blocked \\\"quoted\\\"\"}'\n", want: `{"decision":"deny","reason":"blocked \"quoted\""}` + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			if test.binaryBody != "" {
				writeExecutableTestFile(t, filepath.Join(repo, ".build/bin/reconc"), test.binaryBody)
			}
			stdout, _, err := runGeneratedWrapper(t, repo, "grok-pre-tool-use")
			if err != nil {
				t.Fatalf("wrapper error = %v, stdout=%q", err, stdout)
			}
			if stdout != test.want {
				t.Fatalf("wrapper stdout = %q, want exact %q", stdout, test.want)
			}
		})
	}
}

func TestGrokWrapperFailsClosedOnAmbiguousHostArtifacts(t *testing.T) {
	repo := t.TempDir()
	for _, version := range []string{"0.5.0", "0.8.4"} {
		name := "reconc-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH
		writeExecutableTestFile(t, filepath.Join(repo, "tools/reconc/dist", name), "#!/bin/sh\nprintf '{\"decision\":\"allow\"}\\n'\n")
	}
	stdout, stderr, err := runGeneratedWrapper(t, repo, "grok-pre-tool-use")
	if err != nil {
		t.Fatalf("ambiguous Grok wrapper must use explicit deny with exit zero: %v", err)
	}
	if !strings.Contains(stdout, `"decision":"deny"`) || !strings.Contains(stderr, "ambiguous") {
		t.Fatalf("ambiguous wrapper stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestNonGrokWrapperStillFailsHardWithoutBinary(t *testing.T) {
	stdout, stderr, err := runGeneratedWrapper(t, t.TempDir(), "claude-stop")
	if err == nil || stdout != "" || !strings.Contains(stderr, "no executable Reconc binary") {
		t.Fatalf("non-Grok missing binary stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
}

func runGeneratedWrapper(t *testing.T, repo, event string) (string, string, error) {
	t.Helper()
	wrapper := filepath.Join(repo, filepath.FromSlash(WrapperPath))
	writeExecutableTestFile(t, wrapper, GenerateWrapper().Content)
	cmd := exec.Command(wrapper, event, repo)
	cmd.Dir = repo
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PATH=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PATH=/usr/bin:/bin")
	cmd.Stdin = strings.NewReader(`{}`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func writeExecutableTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
