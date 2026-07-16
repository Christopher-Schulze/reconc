package grokacp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reconc.dev/reconc/internal/hooks"
)

const maxGrokInspectBytes = 4 << 20
const grokInspectTimeout = 10 * time.Second

type commandRunner func(context.Context, string, ...string) *exec.Cmd

func preflight(ctx context.Context, repoRoot, grokBinary string, command commandRunner) error {
	reports, err := hooks.InspectPlatforms(repoRoot)
	if err != nil {
		return fmt.Errorf("inspect Reconc hooks: %w", err)
	}
	var grokStatus *hooks.PlatformStatus
	for index := range reports {
		if reports[index].Kind == hooks.KindGrok {
			grokStatus = &reports[index]
			break
		}
	}
	if grokStatus == nil || grokStatus.State != hooks.StateConfigured {
		detail := "native Grok hook is not installed"
		if grokStatus != nil {
			detail = grokStatus.Detail
		}
		return fmt.Errorf("%s; run `reconc hook install grok %s` and ensure tools/reconc/bin/hook exists", detail, repoRoot)
	}

	inspectCtx, cancel := context.WithTimeout(ctx, grokInspectTimeout)
	defer cancel()
	output, err := inspectJSONWithCommand(inspectCtx, repoRoot, grokBinary, command)
	if err != nil {
		return fmt.Errorf("grok inspect failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var inspection struct {
		ProjectTrusted bool `json:"projectTrusted"`
		Hooks          []struct {
			Target string `json:"target"`
			Source struct {
				Type string `json:"type"`
				Path string `json:"path"`
			} `json:"source"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(output, &inspection); err != nil {
		return fmt.Errorf("decode grok inspect output: %w", err)
	}
	if !inspection.ProjectTrusted {
		return fmt.Errorf("grok does not trust this project; run Grok once with `--trust` or use `/hooks-trust`")
	}
	platform, ok := hooks.PlatformForKind(hooks.KindGrok)
	if !ok {
		return fmt.Errorf("internal: Grok hook platform is not registered")
	}
	expected := make([]string, 0, len(platform.Capabilities))
	for _, capability := range platform.Capabilities {
		expected = append(expected, capability.RuntimeEvents...)
	}
	seen := map[string]bool{}
	expectedSource := filepath.Clean(filepath.Join(repoRoot, ".grok"))
	for _, hook := range inspection.Hooks {
		if hook.Source.Type != "project" && !strings.HasPrefix(filepath.Clean(hook.Source.Path), expectedSource) {
			continue
		}
		for _, event := range expected {
			if strings.Contains(hook.Target, event) {
				seen[event] = true
			}
		}
	}
	missing := make([]string, 0)
	for _, event := range expected {
		if !seen[event] {
			missing = append(missing, event)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("grok did not load native Reconc routes from .grok/hooks/reconc.json: %s; reload `/hooks` and verify project trust", strings.Join(missing, ", "))
}

// InspectJSON runs Grok's read-only inspect command with a hard output cap.
func InspectJSON(ctx context.Context, repoRoot, grokBinary string) ([]byte, error) {
	return inspectJSONWithCommand(ctx, repoRoot, grokBinary, exec.CommandContext)
}

func inspectJSONWithCommand(ctx context.Context, repoRoot, grokBinary string, command commandRunner) ([]byte, error) {
	cmd := command(ctx, grokBinary, "--cwd", repoRoot, "inspect", "--json")
	output := &cappedOutput{limit: maxGrokInspectBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if output.truncatedOutput() {
		return output.bytes(), fmt.Errorf("grok inspect output exceeds %d bytes", maxGrokInspectBytes)
	}
	return output.bytes(), err
}

type cappedOutput struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (w *cappedOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		_, _ = w.buffer.Write(data[:remaining])
	}
	if remaining < len(data) {
		w.truncated = true
	}
	return len(data), nil
}

func (w *cappedOutput) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...)
}

func (w *cappedOutput) truncatedOutput() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}
