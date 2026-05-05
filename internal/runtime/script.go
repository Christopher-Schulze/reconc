package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Script execution constants.
//
// MaxScriptTimeoutSec hard-caps how long a require_script may declare,
// preventing a misconfigured rule from blocking the hook pipeline
// indefinitely. The default of 300s (5 min) is generous enough for any
// reasonable check; CI-grade harnesses can override globally if needed.
const (
	DefaultScriptTimeoutSec     = 60
	DefaultScriptKillTimeoutSec = 5
	MaxScriptTimeoutSec         = 300

	MaxScriptOutputBytes = 64 * 1024 // captured stdout/stderr cap per stream
)

// ScriptOutcome is the structured result of one require_script run.
//
// Status:
//   - "pass":    exit 0
//   - "block":   exit 2 (or any non-zero non-1 if AllowAnyNonZero is set;
//     today only exit 2 = block per the contract)
//   - "error":   any other condition - script crash, IO failure,
//     timeout, malformed config; treated as a HARD error (exit 1)
//     because we do not know the answer
//
// Stdout / Stderr are size-capped to MaxScriptOutputBytes per stream
// to keep audit logs and violation reports bounded.
type ScriptOutcome struct {
	Status   string
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
	TimedOut bool
}

// ScriptInput is the JSON payload reconc writes to the script's stdin.
// Scripts may parse it (or ignore it) to make context-aware decisions.
type ScriptInput struct {
	RuleID         string            `json:"rule_id"`
	RepoRoot       string            `json:"repo_root"`
	Captures       map[string]string `json:"captures"`
	WritePaths     []string          `json:"write_paths"`
	ReadPaths      []string          `json:"read_paths"`
	Commands       []string          `json:"commands"`
	Claims         []string          `json:"claims"`
	CommandResults []CommandResult   `json:"command_results"`
}

// RunScript executes the given script under timeout enforcement and
// returns a structured ScriptOutcome.
//
// Behavior:
//   - cwd      = repoRoot (script always sees the repo from its root)
//   - stdin    = JSON-encoded ScriptInput
//   - stdout   = captured up to MaxScriptOutputBytes
//   - stderr   = captured up to MaxScriptOutputBytes
//   - timeout  = timeoutSec (or DefaultScriptTimeoutSec when 0),
//     hard-capped by MaxScriptTimeoutSec
//   - SIGTERM is sent on timeout, then SIGKILL after killTimeoutSec
//     (or DefaultScriptKillTimeoutSec when 0)
//
// Errors:
//   - script not found or not executable -> ("error", non-nil error)
//   - subprocess crashed (signal etc.) -> ("error", non-nil error)
//   - timeout -> ("error", nil error, TimedOut=true)
//   - exit 0 -> ("pass", nil error)
//   - exit 2 -> ("block", nil error)
//   - any other exit -> ("error", non-nil error)
func RunScript(repoRoot, scriptPath string, args []string, input ScriptInput, timeoutSec, killTimeoutSec int) (ScriptOutcome, error) {
	if timeoutSec == 0 {
		timeoutSec = DefaultScriptTimeoutSec
	}
	if timeoutSec > MaxScriptTimeoutSec {
		timeoutSec = MaxScriptTimeoutSec
	}
	if killTimeoutSec == 0 {
		killTimeoutSec = DefaultScriptKillTimeoutSec
	}

	full := filepath.Join(repoRoot, scriptPath)
	info, err := os.Stat(full)
	if err != nil {
		return ScriptOutcome{Status: "error"}, fmt.Errorf("script not found: %s: %w", scriptPath, err)
	}
	if info.IsDir() {
		return ScriptOutcome{Status: "error"}, fmt.Errorf("script path is a directory: %s", scriptPath)
	}
	if info.Mode()&0o111 == 0 {
		return ScriptOutcome{Status: "error"}, fmt.Errorf("script is not executable (no +x bit): %s", scriptPath)
	}

	// Build the JSON stdin payload.
	stdinJSON, err := json.Marshal(input)
	if err != nil {
		return ScriptOutcome{Status: "error"}, fmt.Errorf("encode script input: %w", err)
	}

	timeout := time.Duration(timeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, full, args...)
	cmd.Dir = repoRoot
	cmd.Env = sanitizedEnv()
	cmd.Stdin = strings.NewReader(string(stdinJSON))

	// Cancel sends SIGTERM first; WaitDelay then escalates to SIGKILL.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = time.Duration(killTimeoutSec) * time.Second

	stdoutBuf := newCappedWriter(MaxScriptOutputBytes)
	stderrBuf := newCappedWriter(MaxScriptOutputBytes)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	start := time.Now()
	err = cmd.Run()
	duration := time.Since(start)

	outcome := ScriptOutcome{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Duration: duration,
	}

	if ctx.Err() == context.DeadlineExceeded {
		outcome.Status = "error"
		outcome.TimedOut = true
		outcome.ExitCode = -1
		return outcome, nil
	}

	if err != nil {
		var exitErr *exec.ExitError
		if asExitErr(err, &exitErr) {
			outcome.ExitCode = exitErr.ExitCode()
			switch outcome.ExitCode {
			case 2:
				outcome.Status = "block"
				return outcome, nil
			default:
				outcome.Status = "error"
				return outcome, fmt.Errorf("script exited %d", outcome.ExitCode)
			}
		}
		outcome.Status = "error"
		outcome.ExitCode = -1
		return outcome, err
	}

	outcome.ExitCode = 0
	outcome.Status = "pass"
	return outcome, nil
}

// sanitizedEnv returns a minimal env for script execution. We strip
// most env vars to avoid leaking agent secrets / API keys into
// arbitrary scripts; the script gets PATH (so it can find common
// tools), HOME, and the marker RECONC_SCRIPT=1 so scripts can detect
// they're running under reconc.
func sanitizedEnv() []string {
	keep := []string{"PATH", "HOME", "LANG", "LC_ALL", "TMPDIR"}
	out := []string{"RECONC_SCRIPT=1"}
	for _, name := range keep {
		if v := os.Getenv(name); v != "" {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// asExitErr is a small wrapper around errors.As so the call site reads
// cleanly without an extra import in the file.
func asExitErr(err error, target **exec.ExitError) bool {
	for e := err; e != nil; {
		if x, ok := e.(*exec.ExitError); ok {
			*target = x
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

// cappedWriter implements io.Writer with a hard byte cap. Writes
// beyond the cap are silently discarded so command output cannot OOM
// the harness.
type cappedWriter struct {
	cap int
	buf []byte
}

func newCappedWriter(cap int) *cappedWriter {
	return &cappedWriter{cap: cap, buf: make([]byte, 0, 1024)}
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := w.cap - len(w.buf)
	if remaining <= 0 {
		return len(p), nil // pretend the write happened
	}
	if len(p) > remaining {
		w.buf = append(w.buf, p[:remaining]...)
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *cappedWriter) String() string { return string(w.buf) }

// Static interface assertion (compile-time check that cappedWriter
// implements io.Writer without manually constructing one).
var _ io.Writer = (*cappedWriter)(nil)
