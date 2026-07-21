package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"reconc.dev/reconc/internal/demo"
)

func runDemo(args []string, version string, stdout io.Writer) error {
	keep := false
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printDemoHelp(stdout)
			return nil
		case "--keep":
			keep = true
		case "--json":
			jsonOut = true
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc demo: unknown argument %q", arg)}
		}
	}

	executable, err := os.Executable()
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc demo: locate current executable: " + err.Error()}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, runErr := demo.Run(ctx, demo.Options{
		Executable: executable,
		Version:    version,
		Keep:       keep,
	})
	if verifyErr := demo.VerifyResult(result); verifyErr != nil {
		return &CLIError{ExitCode: 1, Message: "reconc demo: verify result: " + verifyErr.Error()}
	}
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc demo: encode result: " + err.Error()}
		}
	} else if err := demo.RenderText(stdout, result); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc demo: render result: " + err.Error()}
	}
	if runErr != nil {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func printDemoHelp(stdout io.Writer) {
	fmt.Fprintln(stdout, "Usage: reconc demo [--keep] [--json]")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Run a real block-to-remediation-to-proof journey in an isolated Git repository.")
	fmt.Fprintln(stdout, "The demo is offline, cleans its workspace by default, and uses the current Reconc binary.")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "  --keep  preserve the disposable workspace and print its path")
	fmt.Fprintln(stdout, "  --json  emit the versioned result, steps, decisions, durations, and artifacts")
}
