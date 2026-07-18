package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

var (
	grokPreToolGuardRuntime = runHookRuntime
	grokPreToolGuardTimeout = 5 * time.Second
)

func runGrokPreToolGuard(args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return &CLIError{ExitCode: 1, Message: "reconc hook grok-pre-tool-guard: expected <repo>"}
	}
	var runtimeStdout, runtimeStderr bytes.Buffer
	done := make(chan error, 1)
	runtimeFn := grokPreToolGuardRuntime
	go func() {
		done <- runtimeFn(
			[]string{"grok-pre-tool-use", args[0]},
			&runtimeStdout,
			&runtimeStderr,
		)
	}()
	timer := time.NewTimer(grokPreToolGuardTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		_, _ = stdout.Write(runtimeStdout.Bytes())
		_, _ = stderr.Write(runtimeStderr.Bytes())
		return err
	case <-timer.C:
		body, _ := json.Marshal(map[string]string{
			"decision": "deny",
			"reason":   "Reconc timed out while evaluating this Grok tool call; execution was denied.",
		})
		fmt.Fprintln(stdout, string(body))
		fmt.Fprintf(stderr, "reconc hook grok-pre-tool-guard: runtime exceeded %s; denied before Grok's host timeout\n", grokPreToolGuardTimeout)
		return nil
	}
}
