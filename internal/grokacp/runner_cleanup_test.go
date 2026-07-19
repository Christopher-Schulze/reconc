//go:build !windows

package grokacp

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

type cleanupCloser struct {
	err error
}

func (c cleanupCloser) Close() error {
	return c.err
}

func TestRunSurfacesReconcSessionCleanupFailure(t *testing.T) {
	repo := configuredGrokRepo(t)
	dependencies := defaultDependencies
	dependencies.preflight = func(context.Context, string, string, commandRunner) error { return nil }
	dependencies.stop = func(string, []byte) agentsession.Result { return agentsession.Result{} }
	dependencies.sessionStart = func(string, []byte) agentsession.Result { return agentsession.Result{} }
	dependencies.sessionEnd = func(string, []byte) agentsession.Result {
		return agentsession.Result{ExitCode: 1, Stderr: "cleanup failed"}
	}

	err := run(context.Background(), Options{
		RepoRoot:   repo,
		GrokBinary: fakeGrokBinary(t),
		Prompt:     "do the work",
	}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "finalize Reconc Grok session: cleanup failed") {
		t.Fatalf("session cleanup failure was hidden: %v", err)
	}
}

func TestStopAgentProcessSurfacesCleanupFailures(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		closeError error
		want       string
	}{
		{name: "close", command: "exit 0", closeError: errors.New("close failed"), want: "close failed"},
		{name: "wait", command: "exit 7", want: "exit status 7"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("sh", "-c", test.command)
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			wait := make(chan error, 1)
			go func() { wait <- command.Wait() }()
			err := stopAgentProcess(cleanupCloser{err: test.closeError}, command, wait)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("cleanup error = %v, want %q", err, test.want)
			}
		})
	}
}
