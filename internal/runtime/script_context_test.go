package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunScriptContextDistinguishesCallerCancellationAndDeadline(t *testing.T) {
	tests := []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		wantErr error
	}{
		{name: "cancel", context: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(100*time.Millisecond, cancel)
			return ctx, cancel
		}, wantErr: context.Canceled},
		{name: "deadline", context: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 100*time.Millisecond)
		}, wantErr: context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			writeContextScript(t, repo, "scripts/wait.sh", "#!/bin/sh\nsleep 30\n")
			ctx, cancel := test.context()
			defer cancel()
			started := time.Now()
			outcome, err := RunScriptContext(ctx, repo, "scripts/wait.sh", nil, ScriptInput{}, 5, 1)
			if !errors.Is(err, test.wantErr) || !outcome.Canceled || outcome.TimedOut ||
				outcome.Status != "error" || outcome.ExitCode != -1 {
				t.Fatalf("caller termination = (%#v, %v), want canceled %v", outcome, err, test.wantErr)
			}
			if time.Since(started) >= 3*time.Second {
				t.Fatalf("caller termination ignored until script timeout: %s", time.Since(started))
			}
		})
	}
}

func TestRunScriptContextPreservesConfiguredTimeoutDisposition(t *testing.T) {
	repo := t.TempDir()
	writeContextScript(t, repo, "scripts/wait.sh", "#!/bin/sh\nsleep 30\n")
	outcome, err := RunScriptContext(context.Background(), repo, "scripts/wait.sh", nil, ScriptInput{}, 1, 1)
	if err != nil || !outcome.TimedOut || outcome.Canceled {
		t.Fatalf("configured timeout = (%#v, %v)", outcome, err)
	}
}

func TestPolicyEvaluationContextCancelsEveryScriptPath(t *testing.T) {
	tests := []struct {
		name   string
		policy string
	}{
		{name: "top-level", policy: `rules:
  - id: gate
    kind: require_script
    when_paths: ['src/**']
    script: scripts/wait.sh
    timeout_sec: 5
    mode: block
    message: gate
`},
		{name: "composite", policy: `rules:
  - id: gate
    kind: all_of
    when_paths: ['src/**']
    checks:
      - kind: require_script
        script: scripts/wait.sh
        timeout_sec: 5
    mode: block
    message: gate
`},
		{name: "batch", policy: `rules:
  - id: gate-a
    kind: require_script
    when_paths: ['src/**']
    script: audits/run-workflow-audit
    args: ['a']
    timeout_sec: 5
    mode: block
    message: gate
  - id: gate-b
    kind: require_script
    when_paths: ['src/**']
    script: audits/run-workflow-audit
    args: ['b']
    timeout_sec: 5
    mode: block
    message: gate
`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("RECONC_HOME", t.TempDir())
			repo := t.TempDir()
			writeFile(t, repo, "AGENTS.md", "# test\n")
			writeFile(t, repo, "policies/rules.yml", test.policy)
			writeContextScript(t, repo, "scripts/wait.sh", "#!/bin/sh\nsleep 30\n")
			writeContextScript(t, repo, "audits/run-workflow-audit", "#!/bin/sh\nsleep 30\n")
			if _, err := compileTestHelper(repo); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, err := CheckRepoPolicyContext(ctx, repo, ExecutionInputs{WritePaths: []string{"src/main.go"}})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s script cancellation = %v", test.name, err)
			}
		})
	}
}

func TestRunScriptContextRejectsNilContext(t *testing.T) {
	var missingContext context.Context
	outcome, err := RunScriptContext(missingContext, t.TempDir(), "script.sh", nil, ScriptInput{}, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "context is required") || outcome.Status != "error" {
		t.Fatalf("nil script context = (%#v, %v)", outcome, err)
	}
}

func writeContextScript(t *testing.T, repo, relative, body string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
