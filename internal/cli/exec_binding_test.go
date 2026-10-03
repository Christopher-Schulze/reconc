package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

type execBoundaryWriter struct {
	buffer bytes.Buffer
	change func() error
	seen   bool
}

func (w *execBoundaryWriter) Write(body []byte) (int, error) {
	if !w.seen {
		w.seen = true
		if err := w.change(); err != nil {
			return 0, err
		}
	}
	return w.buffer.Write(body)
}

func TestExecKeepsItsStartSessionAndWriteEpoch(t *testing.T) {
	tests := []struct {
		name      string
		noSession bool
		wantExit  int
	}{
		{name: "write after start"},
		{name: "session switched after start"},
		{name: "session created after start", noSession: true},
		{name: "bound session ended before record", wantExit: 1},
		{name: "same session ID restarted before record", wantExit: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(agentsession.StateRootEnv, filepath.Join(t.TempDir(), "state"))
			repo := makeCheckRepo(t, "rules:\n  - id: fresh-command\n    kind: require_command_success\n    when_paths: ['src/**']\n    commands: ['go version']\n    mode: block\n    message: command must follow the write\n")
			if !test.noSession {
				if _, err := agentsession.InitializeSessionState(repo, "original"); err != nil {
					t.Fatal(err)
				}
				if _, err := agentsession.MutateSessionState(repo, "original", func(state agentsession.SessionState) agentsession.SessionState {
					return agentsession.RecordWriteEvent(state, []string{"src/main.go"})
				}); err != nil {
					t.Fatal(err)
				}
			}
			stdout := &execBoundaryWriter{change: func() error {
				switch test.name {
				case "write after start":
					if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(repo, "src/main.go"), []byte("package main\n"), 0o644); err != nil {
						return err
					}
					_, err := agentsession.MutateSessionState(repo, "original", func(state agentsession.SessionState) agentsession.SessionState {
						return agentsession.RecordWriteEvent(state, []string{"src/main.go"})
					})
					return err
				case "bound session ended before record":
					return agentsession.CleanupSessionState(repo, "original")
				case "same session ID restarted before record":
					if _, err := agentsession.InitializeSessionState(repo, "original"); err != nil {
						return err
					}
					_, err := agentsession.MutateSessionState(repo, "original", func(state agentsession.SessionState) agentsession.SessionState {
						return agentsession.RecordWriteEvent(state, []string{"src/main.go"})
					})
					return err
				default:
					_, err := agentsession.InitializeSessionState(repo, "replacement")
					return err
				}
			}}
			var stderr bytes.Buffer
			err := Run([]string{"exec", repo, "--", "go", "version"}, "test", stdout, &stderr)
			if !stdout.seen || ExitCode(err) != test.wantExit {
				t.Fatalf("observed=%t exit=%d want=%d; err=%v stderr=%s", stdout.seen, ExitCode(err), test.wantExit, err, stderr.String())
			}
			if test.wantExit != 0 {
				return
			}
			if test.name != "write after start" {
				active, err := agentsession.ResolveActiveSessionID(repo)
				if err != nil || active != "replacement" {
					t.Fatalf("active session = %q, %v", active, err)
				}
				replacement, err := agentsession.LoadSessionState(repo, "replacement")
				if err != nil || len(replacement.CommandResults) != 0 {
					t.Fatalf("replacement inherited command results: %+v, %v", replacement.CommandResults, err)
				}
			}
			if test.noSession {
				return
			}
			state, err := agentsession.LoadSessionState(repo, "original")
			if err != nil || len(state.CommandResults) != 1 || state.CommandResults[0].EvidenceEpoch != 1 {
				t.Fatalf("start-bound results = %+v, %v", state.CommandResults, err)
			}
			if test.name == "write after start" {
				inputs := runtime.Empty()
				inputs.WritePaths, inputs.WriteEpochs = state.WritePaths, state.WriteEpochs
				inputs.CommandResults = []runtime.CommandResult{{Command: state.CommandResults[0].Command, Outcome: state.CommandResults[0].Outcome, EvidenceEpoch: state.CommandResults[0].EvidenceEpoch}}
				report, err := runtime.CheckRepoPolicy(repo, inputs)
				if err != nil || report.Decision != runtime.DecisionBlock {
					t.Fatalf("older run qualified for a newer write: report=%+v err=%v", report, err)
				}
			}
		})
	}
}
