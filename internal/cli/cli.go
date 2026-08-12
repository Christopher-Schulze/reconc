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
	"strings"
	"sync"

	"reconc.dev/reconc/internal/commandmeta"
)

// CLIError carries an exit code alongside an error message so the CLI
// layer can map non-zero outcomes to the correct shell exit.
type CLIError struct {
	ExitCode int
	Message  string
}

var hiddenCompatibilityCommands = map[string]struct{}{
	"compile":         {},
	"grok":            {},
	"post-task-check": {},
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
	strictHelpTarget := false
	if argv[0] == "help" {
		if len(argv) == 1 || len(argv) == 2 && isHelpFlag(argv[1]) {
			printUsage(stdout, version)
			return nil
		}
		strictHelpTarget = true
		target := append([]string(nil), argv[1:]...)
		argv = append(target, "--help")
	} else if isHelpFlag(argv[0]) {
		if len(argv) == 1 {
			printUsage(stdout, version)
			return nil
		}
		strictHelpTarget = true
		target := append([]string(nil), argv[1:]...)
		argv = append(target, "--help")
	}
	if handled, err := printTargetHelp(argv, stdout, strictHelpTarget); handled {
		return err
	}
	switch argv[0] {
	case "--version", "-V", "version":
		return runVersion(argv[1:], version, stdout)
	case "doctor":
		return runDoctor(argv[1:], version, stdout, stderr)
	case "compile":
		return runCompile(argv[1:], version, stdout, stderr)
	case "refresh":
		return runRefresh(argv[1:], version, stdout, stderr)
	case "sources":
		return runSources(argv[1:], stdout)
	case "check":
		return runCheck(argv[1:], version, stdout, stderr)
	case "assert":
		return runAssert(argv[1:], version, stdout, stderr)
	case "init":
		return runInit(argv[1:], version, stdout, stderr)
	case "status":
		return runStatus(argv[1:], stdout, stderr)
	case "ci":
		return runCI(argv[1:], version, stdout, stderr)
	case "impact":
		return runImpact(argv[1:], version, stdout)
	case "exec":
		return runExec(argv[1:], stdout, stderr)
	case "hook":
		return runHook(argv[1:], stdout, stderr)
	case "mcp":
		return runMCP(argv[1:], version, stdout, stderr)
	case "grok":
		return runGrok(argv[1:], stdout, stderr)
	case "preset":
		return runPreset(argv[1:], stdout, stderr)
	case "bootstrap":
		return runBootstrap(argv[1:], version, stdout, stderr)
	case "repo":
		return runRepo(argv[1:], version, stdout)
	case "install-cli":
		return runInstallCLI(argv[1:], version, stdout)
	case "update":
		return runUpdate(argv[1:], version, stdout)
	case "uninstall":
		return runUninstall(argv[1:], version, stdout)
	case "fix":
		return runFix(argv[1:], version, stdout, stderr)
	case "next":
		return runNext(argv[1:], version, stdout, stderr)
	case "explain":
		return runExplain(argv[1:], stdout, stderr)
	case "why":
		return runWhy(argv[1:], stdout, stderr)
	case "can":
		return runCan(argv[1:], version, stdout, stderr)
	case "adopt":
		return runAdopt(argv[1:], stdout, stderr)
	case "agent-intro":
		return runAgentIntro(argv[1:], stdout, stderr)
	case "audit":
		return runAudit(argv[1:], stdout, stderr)
	case "action":
		return runAction(argv[1:], stdout)
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
	case "done":
		return runDone(argv[1:], stdout, stderr)
	case "proof":
		return runProof(argv[1:], version, stdout)
	case "extract":
		return runExtract(argv[1:], stdout, stderr)
	case "diff":
		return runDiff(argv[1:], stdout, stderr)
	case "tui":
		return runTUI(argv[1:], stdout, stderr)
	case "completion":
		return runCompletion(argv[1:], stdout, stderr)
	case "manpage":
		return runManpage(argv[1:], version, stdout)
	default:
		message := fmt.Sprintf("reconc: unknown subcommand %q; run `reconc --help` for the current surface", argv[0])
		if suggestion := commandmeta.Suggest(argv[0]); suggestion != "" {
			message = fmt.Sprintf("reconc: unknown subcommand %q; did you mean %q?", argv[0], suggestion)
		}
		return &CLIError{
			ExitCode: 1,
			Message:  message,
		}
	}
}

func isHelpFlag(value string) bool {
	return value == "-h" || value == "--help"
}

func printTargetHelp(argv []string, stdout io.Writer, strictTarget bool) (bool, error) {
	helpIndex := -1
	for index, arg := range argv {
		if isHelpFlag(arg) {
			helpIndex = index
			break
		}
	}
	if helpIndex < 0 || len(argv) == 0 {
		return false, nil
	}
	command, ok := commandmeta.Lookup(argv[0])
	if !ok {
		return false, nil
	}
	synopsis := command.Synopsis
	summary := command.Summary
	commandFlags := command.Flags
	children := command.Subcommands
	path := []string{command.Name}
	matchedDepth := 0
	for _, token := range argv[1:helpIndex] {
		if strings.HasPrefix(token, "-") {
			continue
		}
		if len(children) == 0 {
			if strictTarget {
				return true, unknownHelpTarget(append(path, token))
			}
			continue
		}
		found := false
		for _, child := range children {
			if child.Name != token {
				continue
			}
			synopsis = child.Synopsis
			summary = child.Summary
			commandFlags = child.Flags
			children = child.Subcommands
			path = append(path, child.Name)
			matchedDepth++
			found = true
			break
		}
		if !found {
			return true, unknownHelpTarget(append(path, token))
		}
	}
	if matchedDepth == 0 {
		return false, nil
	}
	fmt.Fprintln(stdout, "Usage: "+synopsis)
	fmt.Fprintln(stdout, summary)
	if len(commandFlags) > 0 {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Options:")
		for _, flag := range commandFlags {
			option := flag.Name
			if flag.Value != "" {
				option += " " + flag.Value
			}
			fmt.Fprintln(stdout, "  "+option)
		}
	}
	publicChildren := make([]commandmeta.Subcommand, 0, len(children))
	for _, child := range children {
		if child.Stability == commandmeta.StabilityStable {
			publicChildren = append(publicChildren, child)
		}
	}
	if len(publicChildren) > 0 {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Subcommands:")
		for _, child := range publicChildren {
			fmt.Fprintf(stdout, "  %-16s %s\n", child.Name, child.Summary)
		}
	}
	return true, nil
}

func unknownHelpTarget(path []string) error {
	return &CLIError{
		ExitCode: 1,
		Message:  fmt.Sprintf("reconc help: unknown target %q", strings.Join(path, " ")),
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

Help:
  reconc help [command [subcommand...]]
`, version)
	commands := commandmeta.Public()
	for _, category := range commandmeta.Categories() {
		fmt.Fprintf(w, "\n%s:\n", category.Title)
		for _, command := range commands {
			if command.Category == category.ID {
				fmt.Fprintf(w, "  %-16s %s\n", command.Name, command.Summary)
			}
		}
	}
	fmt.Fprintln(w, "\nreconc is the standalone Go implementation in this repository.")
}
