package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/policy"
)

const (
	maxMCPAuditEntries = 128
	maxMCPAuditBytes   = 128 * 1024
)

// MCPAuditEntry contains only bounded classification metadata. Tool inputs,
// results, server locators, commands, URLs, headers, and credentials never
// enter this file.
type MCPAuditEntry struct {
	At                string             `json:"at"`
	Platform          policy.MCPPlatform `json:"platform"`
	SelectorHash      string             `json:"selector_hash"`
	ServerFingerprint string             `json:"server_fingerprint,omitempty"`
	Effect            policy.MCPEffect   `json:"effect,omitempty"`
	Outcome           string             `json:"outcome"`
	Classified        bool               `json:"classified"`
	StrictAvailable   bool               `json:"strict_available"`
}

// MCPAuditSummary is the durable, redacted MCP status surface.
type MCPAuditSummary struct {
	Classified        map[string]uint64 `json:"classified"`
	Unclassified      map[string]uint64 `json:"unclassified"`
	Denied            map[string]uint64 `json:"denied"`
	Failures          map[string]uint64 `json:"failures"`
	StrictUnavailable map[string]uint64 `json:"strict_unavailable"`
	Events            []MCPAuditEntry   `json:"events"`
}

func emptyMCPAuditSummary() MCPAuditSummary {
	return MCPAuditSummary{
		Classified:        map[string]uint64{},
		Unclassified:      map[string]uint64{},
		Denied:            map[string]uint64{},
		Failures:          map[string]uint64{},
		StrictUnavailable: map[string]uint64{},
		Events:            []MCPAuditEntry{},
	}
}

func mcpAuditPath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), "mcp-audit.json")
}

func mcpAuditLockPath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), "locks", "mcp-audit.lock")
}

func recordMCPAudit(repoRoot string, envelope *MCPPayload, effect policy.MCPEffect, outcome string, classified, strictAvailable bool) error {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	return recordMCPAuditResolved(root, envelope, effect, outcome, classified, strictAvailable)
}

func recordMCPAuditResolved(root string, envelope *MCPPayload, effect policy.MCPEffect, outcome string, classified, strictAvailable bool) error {
	if envelope == nil {
		return errors.New("MCP audit envelope is nil")
	}
	entry := MCPAuditEntry{
		At:                time.Now().UTC().Format(time.RFC3339Nano),
		Platform:          envelope.Platform,
		SelectorHash:      mcpSelectorHash(envelope),
		ServerFingerprint: envelope.ServerFingerprint,
		Effect:            effect,
		Outcome:           strings.TrimSpace(outcome),
		Classified:        classified,
		StrictAvailable:   strictAvailable,
	}
	if entry.Outcome == "" {
		entry.Outcome = "observed"
	}
	lockPath := mcpAuditLockPath(root)
	if err := ensurePrivateStateDir(filepath.Dir(lockPath)); err != nil {
		return fmt.Errorf("create MCP audit lock directory: %w", err)
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open MCP audit lock: %w", err)
	}
	unlock, err := filelock.Lock(lockFile)
	if err != nil {
		closeErr := lockFile.Close()
		return errors.Join(fmt.Errorf("lock MCP audit: %w", err), closeErr)
	}
	writeErr := appendMCPAuditLocked(root, entry)
	unlockErr := unlock()
	closeErr := lockFile.Close()
	return errors.Join(writeErr, wrapOperationError("unlock MCP audit", unlockErr), wrapOperationError("close MCP audit lock", closeErr))
}

func appendMCPAuditLocked(root string, entry MCPAuditEntry) error {
	summary, err := readMCPAuditResolved(root)
	if err != nil {
		return err
	}
	if entry.Classified {
		key := string(entry.Platform) + "/" + string(entry.Effect)
		summary.Classified[key] = saturatingIncrement(summary.Classified[key])
	} else {
		key := string(entry.Platform)
		summary.Unclassified[key] = saturatingIncrement(summary.Unclassified[key])
	}
	if entry.Outcome == "denied" {
		key := string(entry.Platform)
		summary.Denied[key] = saturatingIncrement(summary.Denied[key])
	}
	if entry.Outcome == "failure" {
		key := string(entry.Platform)
		summary.Failures[key] = saturatingIncrement(summary.Failures[key])
	}
	if !entry.StrictAvailable {
		key := string(entry.Platform)
		summary.StrictUnavailable[key] = saturatingIncrement(summary.StrictUnavailable[key])
	}
	summary.Events = append(summary.Events, entry)
	if len(summary.Events) > maxMCPAuditEntries {
		summary.Events = append([]MCPAuditEntry(nil), summary.Events[len(summary.Events)-maxMCPAuditEntries:]...)
	}
	body, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal MCP audit: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxMCPAuditBytes {
		return fmt.Errorf("MCP audit exceeds %d bytes", maxMCPAuditBytes)
	}
	if _, err := atomicfile.WriteIfChanged(mcpAuditPath(root), body, 0o600); err != nil {
		return fmt.Errorf("write MCP audit: %w", err)
	}
	return nil
}

// ReadMCPAudit returns the durable redacted MCP observation summary.
func ReadMCPAudit(repoRoot string) (MCPAuditSummary, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return MCPAuditSummary{}, err
	}
	return readMCPAuditResolved(root)
}

func readMCPAuditResolved(root string) (MCPAuditSummary, error) {
	file, err := os.Open(mcpAuditPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return emptyMCPAuditSummary(), nil
		}
		return MCPAuditSummary{}, fmt.Errorf("open MCP audit: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxMCPAuditBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return MCPAuditSummary{}, fmt.Errorf("read MCP audit: %w", errors.Join(readErr, closeErr))
	}
	if len(body) > maxMCPAuditBytes {
		return MCPAuditSummary{}, fmt.Errorf("MCP audit exceeds %d bytes", maxMCPAuditBytes)
	}
	var summary MCPAuditSummary
	if err := json.Unmarshal(body, &summary); err != nil {
		return MCPAuditSummary{}, fmt.Errorf("MCP audit is invalid JSON: %w", err)
	}
	if summary.Classified == nil {
		summary.Classified = map[string]uint64{}
	}
	if summary.Unclassified == nil {
		summary.Unclassified = map[string]uint64{}
	}
	if summary.Denied == nil {
		summary.Denied = map[string]uint64{}
	}
	if summary.Failures == nil {
		summary.Failures = map[string]uint64{}
	}
	if summary.StrictUnavailable == nil {
		summary.StrictUnavailable = map[string]uint64{}
	}
	if summary.Events == nil {
		summary.Events = []MCPAuditEntry{}
	}
	if len(summary.Events) > maxMCPAuditEntries {
		return MCPAuditSummary{}, fmt.Errorf("MCP audit exceeds %d entries", maxMCPAuditEntries)
	}
	for _, entry := range summary.Events {
		if !entry.Platform.Valid() || len(entry.SelectorHash) != 64 || entry.Outcome == "" {
			return MCPAuditSummary{}, errors.New("MCP audit contains an invalid entry")
		}
		if _, err := hex.DecodeString(entry.SelectorHash); err != nil {
			return MCPAuditSummary{}, errors.New("MCP audit contains an invalid selector hash")
		}
	}
	return summary, nil
}

func mcpSelectorHash(envelope *MCPPayload) string {
	sum := sha256.Sum256([]byte(string(envelope.Platform) + "\x00" + envelope.ServerFingerprint + "\x00" + envelope.Tool))
	return hex.EncodeToString(sum[:])
}

func saturatingIncrement(value uint64) uint64 {
	if value == ^uint64(0) {
		return value
	}
	return value + 1
}

// SortedMCPClassifiedCounts makes text and JSON-adjacent renderers stable.
func SortedMCPClassifiedCounts(summary MCPAuditSummary) []string {
	keys := make([]string, 0, len(summary.Classified))
	for key := range summary.Classified {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, fmt.Sprintf("%s=%d", key, summary.Classified[key]))
	}
	return out
}
