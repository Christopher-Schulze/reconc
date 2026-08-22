package grokacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

const (
	defaultMaxContinuations = 32
	maxPromptBytes          = 1 << 20
)

// Options configures one strict Grok ACP session.
type Options struct {
	RepoRoot         string
	GrokBinary       string
	Model            string
	Prompt           string
	MaxContinuations int
	Stdout           io.Writer
	Stderr           io.Writer
}

type runnerDependencies struct {
	command      commandRunner
	stop         func(string, []byte) agentsession.Result
	sessionStart func(string, []byte) agentsession.Result
	sessionEnd   func(string, []byte) agentsession.Result
	preflight    func(context.Context, string, string, commandRunner) error
}

var defaultDependencies = runnerDependencies{
	command:      exec.CommandContext,
	stop:         agentsession.RunStop,
	sessionStart: agentsession.RunSessionStart,
	sessionEnd:   agentsession.RunSessionEnd,
	preflight:    preflight,
}

// PolicyBlockedError means Grok exhausted the bounded continuation budget while
// Reconc still required more work.
type PolicyBlockedError struct {
	Reason string
}

func (e *PolicyBlockedError) Error() string {
	return e.Reason
}

// Run starts the unmodified official Grok ACP agent and keeps prompting the
// same session until Reconc's strict Stop evaluation is clean.
func Run(ctx context.Context, options Options) error {
	return run(ctx, options, defaultDependencies)
}

func run(ctx context.Context, options Options, dependencies runnerDependencies) (runErr error) {
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	if options.GrokBinary == "" {
		options.GrokBinary = "grok"
	}
	if options.MaxContinuations == 0 {
		options.MaxContinuations = defaultMaxContinuations
	}
	if options.MaxContinuations < 1 {
		return fmt.Errorf("max continuations must be at least 1")
	}
	if len(options.Prompt) > maxPromptBytes {
		return fmt.Errorf("prompt exceeds %d bytes", maxPromptBytes)
	}
	if strings.TrimSpace(options.Prompt) == "" {
		return fmt.Errorf("prompt must be non-empty")
	}
	root, err := agentsession.ResolveRepoRoot(options.RepoRoot)
	if err != nil {
		return err
	}
	if err := dependencies.preflight(ctx, root, options.GrokBinary, dependencies.command); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	args := []string{"agent", "--always-approve", "--no-leader"}
	if strings.TrimSpace(options.Model) != "" {
		args = append(args, "--model", strings.TrimSpace(options.Model))
	}
	args = append(args, "stdio")
	cmd := dependencies.command(ctx, options.GrokBinary, args...)
	cmd.Dir = root
	// The runner's prompt loop is the only continuation driver for this
	// session; hooks fired by the spawned agent must never leader-steer.
	cmd.Env = replaceEnvironmentValue(os.Environ(), SteerEnv, "0", runtime.GOOS == "windows")
	serializedStderr := &lockedWriter{writer: options.Stderr}
	cmd.Stderr = serializedStderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open Grok ACP stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open Grok ACP stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Grok ACP agent: %w", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	defer func() {
		cleanupErr := stopAgentProcess(stdin, cmd, wait)
		if ctx.Err() != nil && runErr == nil {
			return
		}
		runErr = errors.Join(runErr, cleanupErr)
	}()

	renderer := &streamRenderer{writer: options.Stdout}
	client := newACPClient(stdout, stdin, renderer.handle)

	initCtx, cancelInit := context.WithTimeout(ctx, 30*time.Second)
	defer cancelInit()
	var initialized struct {
		AuthMethods []struct {
			ID string `json:"id"`
		} `json:"authMethods"`
	}
	if err := client.request(initCtx, "initialize", map[string]interface{}{
		"protocolVersion":    1,
		"clientCapabilities": grokClientCapabilities(),
	}, &initialized); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("initialize Grok ACP: %w", err)
	}
	if method := selectAuthMethod(initialized.AuthMethods); method != "" {
		if err := client.request(initCtx, "authenticate", map[string]interface{}{
			"methodId": method,
			"_meta":    map[string]bool{"headless": true},
		}, nil); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("authenticate Grok ACP: %w", err)
		}
	} else if len(initialized.AuthMethods) > 0 {
		return fmt.Errorf("grok ACP offers no usable auth method; run `grok login` or set XAI_API_KEY")
	}

	var session struct {
		SessionID string `json:"sessionId"`
	}
	if err := client.request(initCtx, "session/new", map[string]interface{}{
		"cwd":        root,
		"mcpServers": []interface{}{},
	}, &session); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("create Grok ACP session: %w", err)
	}
	if strings.TrimSpace(session.SessionID) == "" {
		return fmt.Errorf("grok ACP returned an empty sessionId")
	}
	startPayload, err := json.Marshal(map[string]interface{}{
		"session_id":     session.SessionID,
		"reconc_runtime": "grok-acp",
	})
	if err != nil {
		return fmt.Errorf("encode Reconc Grok session start: %w", err)
	}
	if result := dependencies.sessionStart(root, startPayload); result.ExitCode != 0 {
		return fmt.Errorf("initialize Reconc Grok session: %s", strings.TrimSpace(result.Stderr))
	}
	defer func() {
		endPayload, err := json.Marshal(map[string]interface{}{
			"session_id":     session.SessionID,
			"reconc_runtime": "grok-acp",
		})
		if err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("encode Reconc Grok session end: %w", err))
			return
		}
		if result := dependencies.sessionEnd(root, endPayload); result.ExitCode != 0 {
			runErr = errors.Join(runErr, fmt.Errorf("finalize Reconc Grok session: %s", strings.TrimSpace(result.Stderr)))
		}
	}()

	prompt := options.Prompt
	for continuation := 0; ; continuation++ {
		var promptResult struct {
			StopReason string `json:"stopReason"`
		}
		if err := client.request(ctx, "session/prompt", map[string]interface{}{
			"sessionId": session.SessionID,
			"prompt": []map[string]string{
				{"type": "text", "text": prompt},
			},
		}, &promptResult); err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("run Grok ACP prompt: %w", err)
		}
		if err := renderer.endTurn(); err != nil {
			return fmt.Errorf("render Grok ACP output: %w", err)
		}

		stopPayload, err := json.Marshal(map[string]interface{}{
			"session_id":          session.SessionID,
			"reconc_runtime":      "grok-acp",
			"strict_continuation": true,
			"reason":              promptResult.StopReason,
		})
		if err != nil {
			return fmt.Errorf("encode strict Grok Stop payload: %w", err)
		}
		stopResult := dependencies.stop(root, stopPayload)
		if stopResult.ExitCode != 0 {
			return fmt.Errorf("evaluate strict Grok Stop gate: %s", strings.TrimSpace(stopResult.Stderr))
		}
		reason := continuationReason(stopResult.Stdout)
		if reason == "" {
			return nil
		}
		if continuation >= options.MaxContinuations {
			return &PolicyBlockedError{Reason: fmt.Sprintf("Reconc still blocks Grok after %d continuation prompts: %s", options.MaxContinuations, reason)}
		}
		if _, err := fmt.Fprintf(serializedStderr, "reconc grok: continuation %d/%d\n", continuation+1, options.MaxContinuations); err != nil {
			return fmt.Errorf("render Grok continuation status: %w", err)
		}
		prompt = reason
	}
}

func replaceEnvironmentValue(environment []string, name, value string, caseInsensitive bool) []string {
	replaced := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		entryName, _, found := strings.Cut(entry, "=")
		if found && (entryName == name || caseInsensitive && strings.EqualFold(entryName, name)) {
			continue
		}
		replaced = append(replaced, entry)
	}
	return append(replaced, name+"="+value)
}

func grokClientCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
		"terminal": false,
	}
}

func selectAuthMethod(methods []struct {
	ID string `json:"id"`
}) string {
	has := map[string]bool{}
	for _, method := range methods {
		has[method.ID] = true
	}
	if strings.TrimSpace(os.Getenv("XAI_API_KEY")) != "" && has["xai.api_key"] {
		return "xai.api_key"
	}
	if has["cached_token"] {
		return "cached_token"
	}
	return ""
}

func continuationReason(stdout string) string {
	if strings.TrimSpace(stdout) == "" {
		return ""
	}
	var payload map[string]interface{}
	if json.Unmarshal([]byte(stdout), &payload) != nil {
		return strings.TrimSpace(stdout)
	}
	for _, key := range []string{"reason", "followup_message", "message"} {
		if reason, _ := payload[key].(string); strings.TrimSpace(reason) != "" {
			return strings.TrimSpace(reason)
		}
	}
	return ""
}

func stopAgentProcess(stdin io.Closer, cmd *exec.Cmd, wait <-chan error) error {
	closeErr := stdin.Close()
	select {
	case waitErr := <-wait:
		return errors.Join(closeErr, waitErr)
	case <-time.After(2 * time.Second):
	}
	if cmd.Process == nil {
		return errors.Join(closeErr, errors.New("grok ACP process is unavailable during cleanup"))
	}
	if err := cmd.Process.Kill(); err != nil {
		return errors.Join(closeErr, fmt.Errorf("kill Grok ACP process: %w", err))
	}
	select {
	case waitErr := <-wait:
		return errors.Join(closeErr, waitErr)
	case <-time.After(2 * time.Second):
		return errors.Join(closeErr, errors.New("grok ACP process did not exit after kill"))
	}
}

type streamRenderer struct {
	mu          sync.Mutex
	writer      io.Writer
	wrote       bool
	lastNewline bool
	err         error
}

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}

func (r *streamRenderer) handle(params json.RawMessage) {
	var notification struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &notification) != nil ||
		notification.Update.SessionUpdate != "agent_message_chunk" ||
		notification.Update.Content.Text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := io.WriteString(r.writer, notification.Update.Content.Text)
	if err != nil && r.err == nil {
		r.err = err
	}
	r.wrote = true
	r.lastNewline = strings.HasSuffix(notification.Update.Content.Text, "\n")
}

func (r *streamRenderer) endTurn() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil && r.wrote && !r.lastNewline {
		_, r.err = io.WriteString(r.writer, "\n")
	}
	err := r.err
	r.wrote = false
	r.lastNewline = false
	r.err = nil
	return err
}
