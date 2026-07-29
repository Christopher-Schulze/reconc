package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

var (
	grokPreToolGuardRuntime = runGrokPreToolGuardRuntime
	grokPreToolGuardTimeout = 5 * time.Second
)

func runGrokPreToolGuardRuntime(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current Reconc executable: %w", err)
	}
	commandArgs := append([]string{"hook", "runtime"}, args...)
	command := exec.CommandContext(ctx, executable, commandArgs...)
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

func runGrokPreToolGuard(args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return &CLIError{ExitCode: 1, Message: "reconc hook grok-pre-tool-guard: expected <repo>"}
	}
	var runtimeStdout, runtimeStderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), grokPreToolGuardTimeout)
	defer cancel()
	err := grokPreToolGuardRuntime(
		ctx,
		[]string{"grok-pre-tool-use", args[0]},
		&runtimeStdout,
		&runtimeStderr,
	)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		body, _ := json.Marshal(map[string]string{
			"decision": "deny",
			"reason":   "Reconc timed out while evaluating this Grok tool call; execution was denied.",
		})
		fmt.Fprintln(stdout, string(body))
		fmt.Fprintf(stderr, "reconc hook grok-pre-tool-guard: runtime exceeded %s; denied before Grok's host timeout\n", grokPreToolGuardTimeout)
		return nil
	}
	_, _ = stdout.Write(runtimeStdout.Bytes())
	_, _ = stderr.Write(runtimeStderr.Bytes())
	return err
}
