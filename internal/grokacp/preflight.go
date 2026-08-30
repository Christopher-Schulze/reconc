package grokacp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/hooks"
)

const maxGrokInspectBytes = 4 << 20
const maxManagedGrokBytes = 1 << 20
const grokInspectTimeout = 10 * time.Second

type commandRunner func(context.Context, string, ...string) *exec.Cmd

func preflight(ctx context.Context, repoRoot, grokBinary string, command commandRunner) error {
	if err := validateManagedGrokFiles(repoRoot); err != nil {
		return err
	}

	inspectCtx, cancel := context.WithTimeout(ctx, grokInspectTimeout)
	defer cancel()
	output, err := inspectJSONWithCommand(inspectCtx, repoRoot, grokBinary, command)
	if err != nil {
		if detail := strings.TrimSpace(string(output)); detail != "" {
			return fmt.Errorf("grok inspect failed: %w; stdout: %s", err, detail)
		}
		return fmt.Errorf("grok inspect failed: %w", err)
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
	expected := hooks.GrokRuntimeEvents()
	seen := map[string]bool{}
	expectedSource := filepath.Clean(filepath.Join(repoRoot, filepath.Dir(filepath.FromSlash(hooks.GrokHooksPath))))
	for _, hook := range inspection.Hooks {
		if hook.Source.Type != "project" || !pathWithin(expectedSource, hook.Source.Path) {
			continue
		}
		for _, event := range expected {
			if hooks.GrokTargetHasRuntimeEvent(hook.Target, event) {
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

func validateManagedGrokFiles(repoRoot string) error {
	generated, err := hooks.Generate(hooks.KindGrok)
	if err != nil {
		return fmt.Errorf("generate native Grok hook: %w", err)
	}
	target := filepath.Join(repoRoot, filepath.FromSlash(generated.TargetPath))
	data, err := boundedio.ReadRegularFile(target, maxManagedGrokBytes)
	if err != nil {
		return fmt.Errorf("native Grok hook is not installed at %s; run `reconc hook install grok %s`", generated.TargetPath, repoRoot)
	}
	if string(data) != generated.Content {
		return fmt.Errorf("native Grok hook differs from the current generator; run `reconc hook install grok %s --force`", repoRoot)
	}
	wrapper := filepath.Join(repoRoot, filepath.FromSlash(hooks.WrapperPath))
	info, err := os.Stat(wrapper)
	if err != nil || info.IsDir() || (runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
		return fmt.Errorf("%s is missing or not executable; restore it before running `reconc grok`", hooks.WrapperPath)
	}
	wrapperData, err := boundedio.ReadRegularFile(wrapper, maxManagedGrokBytes)
	if err != nil {
		return fmt.Errorf("read %s: %w", hooks.WrapperPath, err)
	}
	if string(wrapperData) != hooks.GenerateWrapper().Content {
		return fmt.Errorf("%s differs from the current Reconc generator; restore the managed wrapper before running `reconc grok`", hooks.WrapperPath)
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(candidate))
	if cleaned == "." || cleaned == "" {
		return false
	}
	rel, err := filepath.Rel(root, cleaned)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// InspectJSON runs Grok's read-only inspect command with a hard output cap.
func InspectJSON(ctx context.Context, repoRoot, grokBinary string) ([]byte, error) {
	return inspectJSONWithCommand(ctx, repoRoot, grokBinary, exec.CommandContext)
}

func inspectJSONWithCommand(ctx context.Context, repoRoot, grokBinary string, command commandRunner) ([]byte, error) {
	cmd := command(ctx, grokBinary, "--cwd", repoRoot, "inspect", "--json")
	stdout, err := boundedexec.NewBuffer(maxGrokInspectBytes)
	if err != nil {
		return nil, fmt.Errorf("initialize grok inspect stdout capture: %w", err)
	}
	stderr, err := boundedexec.NewBuffer(maxGrokInspectBytes)
	if err != nil {
		return nil, fmt.Errorf("initialize grok inspect stderr capture: %w", err)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	if stdout.Truncated() {
		return stdout.Bytes(), fmt.Errorf("grok inspect stdout exceeds %d bytes", maxGrokInspectBytes)
	}
	if stderr.Truncated() {
		return stdout.Bytes(), fmt.Errorf("grok inspect stderr exceeds %d bytes", maxGrokInspectBytes)
	}
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, detail)
		}
	}
	return stdout.Bytes(), err
}
