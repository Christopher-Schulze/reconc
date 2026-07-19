package agentsession

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/tasklifecycle"
)

const (
	compactionContextMarker   = "reconc-context-v1"
	maxCompactionContextBytes = 4 * 1024
	maxTaskOverviewBytes      = 64 * 1024
)

// RunPostCompaction returns a small, project-neutral recovery packet instead
// of replaying logs or large task files into the model context.
func RunPostCompaction(repoRoot string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (compaction, warn): %s", err)}
	}
	summary := cursorFirstString(payload.Raw, "summary", "compact_summary", "compactSummary")
	if strings.Contains(summary, compactionContextMarker) {
		return Result{ExitCode: 0, Stdout: postCompactionJSONOutput("")}
	}
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (compaction, warn): %s", err)}
	}
	state, err := LoadSessionState(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (compaction, warn): %s", err)}
	}
	repositoryRun, err := ReadRepositoryRunStatus(root)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (compaction, warn): %s", err)}
	}

	lines := []string{
		compactionContextMarker,
		"Re-read the repository's agent instructions and bootstrap guide before changing files.",
		"Inspect the live task overview and active task detail; never infer completion from the compaction summary.",
	}
	if active := activeTaskLine(root); active != "" {
		lines = append(lines, "Active task: "+active)
	}
	lines = append(lines, fmt.Sprintf(
		"Session evidence: reads=%d writes=%d commands=%d command_results=%d claims=%d overflow=%t.",
		len(state.ReadPaths), len(state.WritePaths), len(state.Commands), len(state.CommandResults), len(state.Claims), state.EvidenceOverflow,
	))
	if repositoryRun.Enabled {
		lines = append(lines, "Repository run: enabled; continue only the live executable TASK.")
	} else if repositoryRun.DisabledReason != "" {
		lines = append(lines, "Repository run: disabled ("+repositoryRun.DisabledReason+").")
	} else {
		lines = append(lines, "Repository run: disabled.")
	}
	lines = append(lines, "Re-run relevant verification before claiming the task is done.")
	context := truncateUTF8(strings.Join(dedupeContextLines(lines), "\n"), maxCompactionContextBytes)
	return Result{ExitCode: 0, Stdout: postCompactionJSONOutput(context)}
}

func activeTaskLine(repoRoot string) string {
	overview := "docs/tasks.md"
	if cfg, err := tasklifecycle.LoadConfig(repoRoot); err == nil && cfg.OverviewPath != "" {
		overview = cfg.OverviewPath
	}
	path := filepath.Join(repoRoot, filepath.FromSlash(overview))
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTaskOverviewBytes))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Current:") || strings.HasPrefix(trimmed, "- [~]") {
			return truncateUTF8(trimmed, 512)
		}
	}
	return ""
}

func postCompactionJSONOutput(context string) string {
	payload := map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":     "PostCompaction",
			"additionalContext": context,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(body)
}

// AdaptPostCompactionResult rewrites the hook event name when a platform
// exposes post-compaction recovery through another context-capable lifecycle.
func AdaptPostCompactionResult(result Result, hookEventName string) Result {
	if result.Stdout == "" {
		return result
	}
	var payload map[string]interface{}
	if json.Unmarshal([]byte(result.Stdout), &payload) != nil {
		return result
	}
	hookOutput, ok := payload["hookSpecificOutput"].(map[string]interface{})
	if !ok {
		return result
	}
	hookOutput["hookEventName"] = hookEventName
	body, err := json.Marshal(payload)
	if err == nil {
		result.Stdout = string(body)
	}
	return result
}

// AdaptCodexCompactionResult emits Codex's documented common-output field.
func AdaptCodexCompactionResult(result Result) Result {
	if result.Stdout == "" {
		return result
	}
	var payload map[string]interface{}
	if json.Unmarshal([]byte(result.Stdout), &payload) != nil {
		return result
	}
	hookOutput, ok := payload["hookSpecificOutput"].(map[string]interface{})
	if !ok {
		return result
	}
	context, _ := hookOutput["additionalContext"].(string)
	if context == "" {
		result.Stdout = ""
		return result
	}
	body, err := json.Marshal(map[string]interface{}{"systemMessage": context})
	if err == nil {
		result.Stdout = string(body)
	}
	return result
}

func dedupeContextLines(lines []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	return out
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	suffix := "\n[reconc context truncated]"
	limit := maxBytes - len(suffix)
	if limit < 0 {
		limit = 0
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit] + suffix
}
