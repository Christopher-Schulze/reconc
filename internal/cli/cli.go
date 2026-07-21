// Package cli implements the argparse-equivalent CLI dispatcher for reconc.
//
// Run dispatches argv to the appropriate subcommand. It returns nil on
// success or a CLIError carrying an exit code for the main binary to
// surface to the shell.
//
// Exit codes:
//
//	0 -- clean run, or a non-blocking decision (pass/warn)
//	1 -- runtime or input error
//	2 -- at least one blocking policy violation (block)
//	3..255 -- a non-zero child exit propagated by reconc exec
package cli

import (
	stderrors "errors"
	"fmt"
	"io"
	"sync"
)

// CLIError carries an exit code alongside an error message so the CLI
// layer can map non-zero outcomes to the correct shell exit.
type CLIError struct {
	ExitCode int
	Message  string
}

func (e *CLIError) Error() string {
	return e.Message
}

// ExitCode extracts a shell exit code from any error returned by Run.
// A nil error means exit 0. A *CLIError carries its own code. Any other
// error maps to exit 1 (runtime error).
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ce *CLIError
	if stderrors.As(err, &ce) {
		return ce.ExitCode
	}
	return 1
}

// Run parses argv and dispatches to the matching subcommand. stdout and
// stderr are explicit so tests can capture output without touching os.Stdout.
func Run(argv []string, version string, stdout, stderr io.Writer) (runErr error) {
	trackedStdout := &trackedOutputWriter{writer: stdout}
	trackedStderr := &trackedOutputWriter{writer: stderr}
	stdout = trackedStdout
	stderr = trackedStderr
	defer func() {
		runErr = stderrors.Join(runErr, trackedStdout.Err(), trackedStderr.Err())
	}()
	if len(argv) == 0 {
		printUsage(stdout, version)
		return nil
	}
	switch argv[0] {
	case "--version", "-V", "version":
		return runVersion(argv[1:], version, stdout)
	case "--help", "-h", "help":
		printUsage(stdout, version)
		return nil
	case "doctor":
		return runDoctor(argv[1:], stdout, stderr)
	case "compile":
		return runCompile(argv[1:], version, stdout, stderr)
	case "refresh":
		return runRefresh(argv[1:], version, stdout, stderr)
	case "check":
		return runCheck(argv[1:], stdout, stderr)
	case "assert":
		return runAssert(argv[1:], stdout, stderr)
	case "init":
		return runInit(argv[1:], stdout, stderr)
	case "status":
		return runStatus(argv[1:], stdout, stderr)
	case "ci":
		return runCI(argv[1:], stdout, stderr)
	case "exec":
		return runExec(argv[1:], stdout, stderr)
	case "hook":
		return runHook(argv[1:], stdout, stderr)
	case "grok":
		return runGrok(argv[1:], stdout, stderr)
	case "preset":
		return runPreset(argv[1:], stdout, stderr)
	case "bootstrap":
		return runBootstrap(argv[1:], version, stdout, stderr)
	case "fix":
		return runFix(argv[1:], stdout, stderr)
	case "next":
		return runNext(argv[1:], stdout, stderr)
	case "explain":
		return runExplain(argv[1:], stdout, stderr)
	case "verify":
		return runVerify(argv[1:], stdout, stderr)
	case "why":
		return runWhy(argv[1:], stdout, stderr)
	case "can":
		return runCan(argv[1:], stdout, stderr)
	case "adopt":
		return runAdopt(argv[1:], stdout, stderr)
	case "changelog":
		return runChangelog(argv[1:], stdout, stderr)
	case "agent-intro":
		return runAgentIntro(argv[1:], stdout, stderr)
	case "audit":
		return runAudit(argv[1:], stdout, stderr)
	case "run":
		return runRunControl(argv[1:], stdout, stderr)
	case "task":
		return runTask(argv[1:], stdout, stderr)
	case "prune":
		return runPrune(argv[1:], stdout)
	case "template":
		return runTemplate(argv[1:], stdout, stderr)
	case "session-briefing":
		return runSessionBriefing(argv[1:], stdout, stderr)
	case "context":
		return runContext(argv[1:], stdout, stderr)
	case "start":
		return runStart(argv[1:], stdout, stderr)
	case "post-task-check":
		return runPostTaskCheck(argv[1:], stdout, stderr)
	case "delta":
		return runDelta(argv[1:], stdout, stderr)
	case "done":
		return runDone(argv[1:], stdout, stderr)
	case "spec":
		return runSpec(argv[1:], stdout, stderr)
	case "coverage":
		return runCoverage(argv[1:], stdout, stderr)
	case "extract":
		return runExtract(argv[1:], stdout, stderr)
	case "diff":
		return runDiff(argv[1:], stdout, stderr)
	case "watch":
		return runWatch(argv[1:], stdout, stderr)
	case "tui":
		return runTUI(argv[1:], stdout, stderr)
	case "completion":
		return runCompletion(argv[1:], stdout, stderr)
	case "manpage":
		return runManpage(argv[1:], version, stdout)
	default:
		return &CLIError{
			ExitCode: 1,
			Message:  fmt.Sprintf("reconc: subcommand %q is not yet implemented in this build; run `reconc --help` for the current surface", argv[0]),
		}
	}
}

type trackedOutputWriter struct {
	mu     sync.Mutex
	writer io.Writer
	err    error
}

func (w *trackedOutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	written, err := w.writer.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return written, err
}

func (w *trackedOutputWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *trackedOutputWriter) Unwrap() io.Writer {
	return w.writer
}

func printUsage(w io.Writer, version string) {
	fmt.Fprintf(w, `reconc %s -- Repository Control Compiler

Usage:
  reconc [flags] <subcommand> [args...]

Flags:
  --version, -V    Print version and exit
  --help, -h       Print this help and exit

Daily:
  status           one-line policy health summary
  check            evaluate runtime evidence against compiled policy
  next             show the next remediation
  done             evidence-complete task-finish gate: prints done or blocked

Bootstrap & inspection:
  bootstrap        inspect / profiles / plan / apply / verify / remove onboarding
  init             Scaffold .reconc.yml (and stub AGENTS.md) for a fresh repo
  adopt            Scan repo for tooling and suggest matching rules
  extract          Heuristic scan of AGENTS.md/CLAUDE.md prose for rule hints
  doctor           Inspect discovery and validation state
  verify           End-to-end installation health check ($RECONC_HOME, repo, lockfile, hook)

Compile & evaluate:
  compile          Compile policy sources into .reconc/policy.lock.json
  refresh          Explicitly refresh .reconc/policy.lock.json
  ci               Derive write_paths from git diff and run check
  exec             Execute a command and bind success to the staged Git state
  assert           Evaluate one rule by id with --var key=value substitution
  can              Ultra-terse yes/no for an action (e.g. 'reconc can write src/app.go')
  diff             Compare two compiled lockfiles (added / removed / changed rules)
  watch            Recompile on source-file changes (exits on Ctrl-C)

Explain & remediate:
  explain          Render a check report in text or markdown
  why              Print the full details of one compiled rule

Packs & wiring:
  preset           list / show bundled and user presets
  template         list / show bundled and user rule templates (W18)
  hook             generate / install / uninstall / status / sync-scaffold / claim hooks
  grok             strict Grok ACP runner with Reconc continuation enforcement

Workflow maintenance:
  changelog        rotate docs/changelog.md / list-archives
  agent-intro      print the embedded reconc agent integration guide
  audit            tail / stats / export the enforcement decision log
  run              AI-operated on / off / status / log repository run control
  task             typed TASK status / validation / atomic lifecycle mutations
  prune            bound runtime state, logs, generated binaries, and owned temp residue
  session-briefing versioned session/reentry delta (TASK + policy + run)
  context          canonical entrypoint + active TASK token budget
  start            render / write a canonical start.md onboarding doc
  post-task-check  evidence-complete pre-done gate
  delta            show audit + policy changes since a point in time
  spec             check docs/spec.md presence + freshness
  coverage         check a coverage percentage against a minimum
  tui              terminal dashboard for policy / rules / audit / session

Meta:
  version          print the build version
  completion       emit shell completion script (bash / zsh / fish)
  manpage          emit a groff man(1) page for reconc(1)

reconc is the standalone Go implementation in this repository.
`, version)
}
