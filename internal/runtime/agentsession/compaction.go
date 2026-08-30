package agentsession

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/tasklifecycle"
)

const (
	compactionContextMarker      = "reconc-context-v1"
	compactionContextBeginPrefix = "<<<" + compactionContextMarker + " sha256="
	compactionContextBeginSuffix = ">>>"
	compactionContextEnd         = "<<<end-" + compactionContextMarker + ">>>"
	maxCompactionContextBytes    = 4 * 1024
	maxCompactionSummaryScan     = 64 * 1024
	maxTaskOverviewBytes         = 64 * 1024
)

// RunPostCompaction returns a small, project-neutral recovery packet instead
// of replaying logs or large task files into the model context.
func RunPostCompaction(repoRoot string, payloadBytes []byte) Result {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (compaction, warn): %s", err)}
	}
	return runPostCompactionResolved(root.path, payloadBytes)
}

func runPostCompactionResolved(root string, payloadBytes []byte) Result {
	payload, err := ParsePayload(payloadBytes)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (compaction, warn): %s", err)}
	}
	summary := cursorFirstString(payload.Raw, "summary", "compact_summary", "compactSummary")
	if hasCompactionRecoveryEnvelope(summary) {
		body, err := postCompactionJSONOutput("")
		if err != nil {
			return resultWithEncodingError(Result{ExitCode: 2}, err)
		}
		return Result{ExitCode: 0, Stdout: body}
	}
	state, err := loadSessionStateWithLockResolved(root, payload.SessionID)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (compaction, warn): %s", err)}
	}
	state, err = loadCompleteSessionEvidence(root, state)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (compaction, warn): load evidence chain: %s", err)}
	}
	repositoryRun, err := readRepositoryRunStatusResolved(root)
	if err != nil {
		return Result{ExitCode: 0, Stderr: fmt.Sprintf("reconc hook (compaction, warn): %s", err)}
	}

	lines := []string{
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
	context := compactionRecoveryEnvelope(strings.Join(dedupeContextLines(lines), "\n"))
	body, err := postCompactionJSONOutput(context)
	if err != nil {
		return resultWithEncodingError(Result{ExitCode: 2}, err)
	}
	return Result{ExitCode: 0, Stdout: body}
}

func compactionRecoveryEnvelope(body string) string {
	overhead := len(compactionContextBeginPrefix) + sha256.Size*2 + len(compactionContextBeginSuffix) +
		len(compactionContextEnd) + 2
	body = truncateUTF8(body, maxCompactionContextBytes-overhead)
	digest := sha256.Sum256([]byte(body))
	return fmt.Sprintf("%s%x%s\n%s\n%s", compactionContextBeginPrefix, digest, compactionContextBeginSuffix, body, compactionContextEnd)
}

func hasCompactionRecoveryEnvelope(summary string) bool {
	if len(summary) > maxCompactionSummaryScan {
		summary = summary[len(summary)-maxCompactionSummaryScan:]
	}
	summary = strings.TrimRight(summary, " \t\r\n")
	if !strings.HasSuffix(summary, compactionContextEnd) {
		return false
	}
	endIndex := len(summary) - len(compactionContextEnd)
	if endIndex == 0 || summary[endIndex-1] != '\n' {
		return false
	}
	beginIndex := lastLinePrefix(summary[:endIndex-1], compactionContextBeginPrefix)
	if beginIndex < 0 {
		return false
	}
	lineEnd := strings.IndexByte(summary[beginIndex:endIndex], '\n')
	if lineEnd < 0 {
		return false
	}
	lineEnd += beginIndex
	beginLine := summary[beginIndex:lineEnd]
	wantBeginBytes := len(compactionContextBeginPrefix) + sha256.Size*2 + len(compactionContextBeginSuffix)
	if len(beginLine) != wantBeginBytes || !strings.HasSuffix(beginLine, compactionContextBeginSuffix) {
		return false
	}
	wantDigest := beginLine[len(compactionContextBeginPrefix) : len(beginLine)-len(compactionContextBeginSuffix)]
	if !isLowerHexDigest(wantDigest) {
		return false
	}
	body := summary[lineEnd+1 : endIndex-1]
	digest := sha256.Sum256([]byte(body))
	return fmt.Sprintf("%x", digest) == wantDigest
}

func lastLinePrefix(value, prefix string) int {
	for searchEnd := len(value); searchEnd > 0; {
		index := strings.LastIndex(value[:searchEnd], prefix)
		if index < 0 {
			return -1
		}
		if index == 0 || value[index-1] == '\n' {
			return index
		}
		searchEnd = index
	}
	return -1
}

func isLowerHexDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if char >= '0' && char <= '9' || char >= 'a' && char <= 'f' {
			continue
		}
		return false
	}
	return true
}

func activeTaskLine(repoRoot string) string {
	overview := "docs/tasks.md"
	if cfg, err := tasklifecycle.LoadConfig(repoRoot); err == nil && cfg.OverviewPath != "" {
		overview = cfg.OverviewPath
	}
	path := filepath.Join(repoRoot, filepath.FromSlash(overview))
	data, err := boundedio.ReadFile(path, maxTaskOverviewBytes)
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

func postCompactionJSONOutput(context string) (string, error) {
	payload := map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":     "PostCompaction",
			"additionalContext": context,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal post-compaction response: %w", err)
	}
	return string(body), nil
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
	if err != nil {
		return resultWithEncodingError(result, fmt.Errorf("marshal adapted post-compaction response: %w", err))
	}
	result.Stdout = string(body)
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
	if err != nil {
		return resultWithEncodingError(result, fmt.Errorf("marshal Codex compaction response: %w", err))
	}
	result.Stdout = string(body)
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
