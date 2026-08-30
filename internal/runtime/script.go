package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/pathidentity"
)

// Script execution constants.
//
// MaxScriptTimeoutSec hard-caps how long a require_script may declare,
// preventing a misconfigured rule from blocking the hook pipeline
// indefinitely. The default of 300s (5 min) is generous enough for any
// reasonable check; CI-grade harnesses can override globally if needed.
//
// MaxScriptKillTimeoutSec hard-caps the SIGTERM-to-SIGKILL grace the same way.
// Without a cap, a lockfile value near math.MaxInt64 overflows the
// time.Duration conversion and either wraps into a nonsense delay or pins a
// script that ignores SIGTERM long past MaxScriptTimeoutSec.
const (
	DefaultScriptTimeoutSec     = 60
	DefaultScriptKillTimeoutSec = 5
	MaxScriptTimeoutSec         = 300
	MaxScriptKillTimeoutSec     = 60

	MaxScriptOutputBytes = 64 * 1024 // captured stdout/stderr cap per stream
)

// ScriptOutcome is the structured result of one require_script run.
//
// Status:
//   - "pass":    exit 0
//   - "block":   exit 2
//   - "error":   any other condition - script crash, IO failure,
//     timeout, malformed config; callers fail closed because the script
//     did not produce a trustworthy policy answer
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
	Canceled bool
}

type scriptOutcomeDisposition uint8

const (
	scriptOutcomePass scriptOutcomeDisposition = iota
	scriptOutcomeBlock
	scriptOutcomeError
)

type scriptOutcomeEvaluation struct {
	disposition scriptOutcomeDisposition
	detail      string
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
//   - Unix sends SIGTERM to the process group, then SIGKILL after
//     killTimeoutSec (or DefaultScriptKillTimeoutSec when 0), hard-capped by
//     MaxScriptKillTimeoutSec; Windows uses its native immediate process kill
//
// Errors:
//   - script escapes the repository -> ("error", non-nil error)
//   - script not found or not executable -> ("error", non-nil error)
//   - subprocess crashed (signal etc.) -> ("error", non-nil error)
//   - caller cancellation -> ("error", context error, Canceled=true)
//   - timeout -> ("error", nil error, TimedOut=true)
//   - exit 0 -> ("pass", nil error)
//   - exit 2 -> ("block", nil error)
//   - any other exit -> ("error", non-nil error)
func RunScript(repoRoot, scriptPath string, args []string, input ScriptInput, timeoutSec, killTimeoutSec int) (ScriptOutcome, error) {
	return RunScriptContext(context.Background(), repoRoot, scriptPath, args, input, timeoutSec, killTimeoutSec)
}

// RunScriptContext executes a policy script under the caller lifecycle and a
// configured child timeout. The legacy RunScript entry point uses a bounded
// background lifecycle for callers that do not own cancellation.
func RunScriptContext(caller context.Context, repoRoot, scriptPath string, args []string, input ScriptInput, timeoutSec, killTimeoutSec int) (ScriptOutcome, error) {
	return runScriptContext(caller, repoRoot, scriptPath, args, input, timeoutSec, killTimeoutSec, nil)
}

func runScriptContext(caller context.Context, repoRoot, scriptPath string, args []string, input ScriptInput, timeoutSec, killTimeoutSec int, hook scriptExecutionHook) (outcome ScriptOutcome, resultErr error) {
	if caller == nil {
		return ScriptOutcome{Status: "error"}, errors.New("script caller context is required")
	}
	if err := caller.Err(); err != nil {
		return canceledScriptOutcome(err)
	}
	full, err := resolveRepoScriptPath(repoRoot, scriptPath)
	if err != nil {
		return ScriptOutcome{Status: "error"}, err
	}
	bound, err := prepareBoundScript(repoRoot, scriptPath, full, hook)
	if err != nil {
		return ScriptOutcome{Status: "error"}, err
	}
	defer func() {
		if cleanupErr := bound.cleanup(); cleanupErr != nil {
			outcome.Status = "error"
			outcome.ExitCode = -1
			resultErr = errors.Join(resultErr, fmt.Errorf("remove private script snapshot: %w", cleanupErr))
		}
	}()
	if err := caller.Err(); err != nil {
		return canceledScriptOutcome(err)
	}

	// Build the JSON stdin payload.
	stdinJSON, err := json.Marshal(input)
	if err != nil {
		return ScriptOutcome{Status: "error"}, fmt.Errorf("encode script input: %w", err)
	}

	timeout := normalizedScriptTimeout(timeoutSec)
	ctx, cancel := context.WithTimeout(caller, timeout)
	defer cancel()

	cmd, err := scriptCommand(ctx, bound.executionPath, args)
	if err != nil {
		return ScriptOutcome{Status: "error"}, err
	}
	cmd.Dir = repoRoot
	cmd.Env = sanitizedEnv()
	cmd.Stdin = bytes.NewReader(stdinJSON)

	done := make(chan struct{})
	killGrace := normalizedScriptKillTimeout(killTimeoutSec)
	configureScriptProcess(cmd, killGrace)

	stdoutBuf, err := boundedexec.NewBuffer(MaxScriptOutputBytes)
	if err != nil {
		return ScriptOutcome{Status: "error"}, fmt.Errorf("initialize script stdout capture: %w", err)
	}
	stderrBuf, err := boundedexec.NewBuffer(MaxScriptOutputBytes)
	if err != nil {
		return ScriptOutcome{Status: "error"}, fmt.Errorf("initialize script stderr capture: %w", err)
	}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf
	if err := runScriptExecutionHook(hook, scriptStageCommandPrepared, full); err != nil {
		return ScriptOutcome{Status: "error"}, err
	}
	if err := bound.revalidate(); err != nil {
		return ScriptOutcome{Status: "error"}, err
	}

	start := time.Now()
	err = cmd.Start()
	if err == nil {
		monitorScriptProcess(ctx, cmd.Process.Pid, done, killGrace)
		err = cmd.Wait()
	}
	close(done)
	duration := time.Since(start)

	outcome = ScriptOutcome{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Duration: duration,
	}

	if callerErr := caller.Err(); callerErr != nil {
		outcome.Canceled = true
		outcome.Status = "error"
		outcome.ExitCode = -1
		return outcome, fmt.Errorf("script canceled by caller: %w", callerErr)
	}
	if ctx.Err() == context.DeadlineExceeded {
		outcome.Status = "error"
		outcome.TimedOut = true
		outcome.ExitCode = -1
		return outcome, nil
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
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

func canceledScriptOutcome(err error) (ScriptOutcome, error) {
	return ScriptOutcome{Status: "error", ExitCode: -1, Canceled: true}, fmt.Errorf("script canceled by caller: %w", err)
}

func normalizedScriptTimeout(timeoutSec int) time.Duration {
	if timeoutSec == 0 {
		timeoutSec = DefaultScriptTimeoutSec
	}
	if timeoutSec > MaxScriptTimeoutSec {
		timeoutSec = MaxScriptTimeoutSec
	}
	return time.Duration(timeoutSec) * time.Second
}

// normalizedScriptKillTimeout is the single conversion point from a declared
// kill_timeout_sec to the SIGTERM-to-SIGKILL grace. Every process backend takes
// the normalized duration, so an out-of-range lockfile value can neither
// overflow time.Duration nor outlive the script timeout cap.
func normalizedScriptKillTimeout(killTimeoutSec int) time.Duration {
	if killTimeoutSec <= 0 {
		killTimeoutSec = DefaultScriptKillTimeoutSec
	}
	if killTimeoutSec > MaxScriptKillTimeoutSec {
		killTimeoutSec = MaxScriptKillTimeoutSec
	}
	return time.Duration(killTimeoutSec) * time.Second
}

// resolveRepoScriptPath validates that scriptPath names a script inside the
// repository and returns the path to execute.
//
// Containment is enforced on the resolved parent directory, which is what the
// lexical parser cannot see: rejecting `..` segments does not stop an
// intermediate directory symlink from moving the execution target outside the
// repository. The returned leaf stays lexical so the source identity check
// keeps rejecting a symlinked script file itself.
func resolveRepoScriptPath(repoRoot, scriptPath string) (string, error) {
	configured := filepath.FromSlash(scriptPath)
	cleaned := filepath.Clean(configured)
	if configured == "" || pathidentity.Rooted(scriptPath) || pathidentity.EscapesLexically(scriptPath) ||
		cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", &rerrors.RepoBoundaryError{Path: scriptPath, RepoRoot: repoRoot}
	}
	resolvedRoot, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root for script %q: %w", scriptPath, err)
	}
	lexical := filepath.Join(resolvedRoot, cleaned)
	resolvedParent, err := pathidentity.ResolveProspective(filepath.Dir(lexical))
	if err != nil {
		return "", fmt.Errorf("resolve script directory for %q: %w", scriptPath, err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedParent)
	if err != nil {
		return "", fmt.Errorf("validate script %q containment: %w", scriptPath, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &rerrors.RepoBoundaryError{Path: scriptPath, RepoRoot: resolvedRoot}
	}
	return lexical, nil
}

// classifyScriptOutcome is the single fail-closed contract shared by every
// require_script caller. Only a consistent pass outcome admits the write; a
// declared block stays a policy failure, while timeouts, process failures, and
// contradictory or unknown outcomes are operational failures.
func classifyScriptOutcome(outcome ScriptOutcome, runErr error, timeoutSec int) scriptOutcomeEvaluation {
	if outcome.Canceled {
		detail := "canceled by caller"
		if runErr != nil {
			detail += ": " + runErr.Error()
		}
		return scriptOutcomeEvaluation{disposition: scriptOutcomeError, detail: detail}
	}
	if outcome.TimedOut {
		return scriptOutcomeEvaluation{
			disposition: scriptOutcomeError,
			detail:      "timed out after " + normalizedScriptTimeout(timeoutSec).String(),
		}
	}
	if runErr != nil {
		return scriptOutcomeEvaluation{disposition: scriptOutcomeError, detail: runErr.Error()}
	}

	switch outcome.Status {
	case "pass":
		if outcome.ExitCode != 0 {
			return scriptOutcomeEvaluation{
				disposition: scriptOutcomeError,
				detail:      fmt.Sprintf("returned pass status with exit code %d", outcome.ExitCode),
			}
		}
		return scriptOutcomeEvaluation{disposition: scriptOutcomePass}
	case "block":
		if outcome.ExitCode != 2 {
			return scriptOutcomeEvaluation{
				disposition: scriptOutcomeError,
				detail:      fmt.Sprintf("returned block status with exit code %d", outcome.ExitCode),
			}
		}
		return scriptOutcomeEvaluation{disposition: scriptOutcomeBlock, detail: scriptBlockDetail(outcome)}
	case "error":
		return scriptOutcomeEvaluation{
			disposition: scriptOutcomeError,
			detail:      fmt.Sprintf("returned error status with exit code %d", outcome.ExitCode),
		}
	default:
		return scriptOutcomeEvaluation{
			disposition: scriptOutcomeError,
			detail:      fmt.Sprintf("returned invalid status %q with exit code %d", outcome.Status, outcome.ExitCode),
		}
	}
}

func scriptBlockDetail(outcome ScriptOutcome) string {
	detail := strings.TrimSpace(outcome.Stdout)
	if detail == "" {
		detail = strings.TrimSpace(outcome.Stderr)
	}
	if detail == "" {
		return "no output"
	}
	return detail
}

// sanitizedEnv minimizes incidental secret exposure to trusted,
// policy-authored repository code; it is not a process sandbox. HOME stays
// available because common toolchains need user-level caches and configuration.
// Scripts also receive PATH, locale/temp settings, and RECONC_SCRIPT=1.
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
