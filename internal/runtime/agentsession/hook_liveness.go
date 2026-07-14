package agentsession

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/filelock"
)

const (
	hookLivenessWriteInterval = 6 * time.Hour
	maxHookLivenessBytes      = 64 * 1024
	maxHookLivenessRuntimes   = 16
)

// HookLiveness is rate-limited proof that a runtime loaded and executed a
// Reconc session-start route. It complements static hook configuration status.
type HookLiveness struct {
	Runtime  string `json:"runtime"`
	LastSeen string `json:"last_seen"`
	Event    string `json:"event"`
}

func hookLivenessPath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), "hook-liveness.json")
}

func hookLivenessLockPath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), "locks", "hook-liveness.lock")
}

// RecordHookLiveness records at most one timestamp per runtime every six
// hours. Session-start is deliberately the only hot-path caller.
func RecordHookLiveness(repoRoot, runtime, event string) error {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	runtime = normalizeRuntimeName(runtime)
	if runtime == "" {
		return nil
	}
	if len(runtime) > 64 || len(strings.TrimSpace(event)) > 64 {
		return fmt.Errorf("hook-liveness runtime and event must be at most 64 bytes")
	}
	return recordHookLivenessAt(root, runtime, event, time.Now().UTC())
}

func recordHookLivenessAt(root, runtime, event string, now time.Time) error {
	lockPath := hookLivenessLockPath(root)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return fmt.Errorf("create hook-liveness lock directory: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open hook-liveness lock: %w", err)
	}
	defer file.Close()
	unlock, err := filelock.Lock(file)
	if err != nil {
		return fmt.Errorf("lock hook liveness: %w", err)
	}
	defer func() { _ = unlock() }()

	records, err := readHookLivenessResolved(root)
	if err != nil {
		return err
	}
	if current, ok := records[runtime]; ok {
		lastSeen, parseErr := time.Parse(time.RFC3339Nano, current.LastSeen)
		if parseErr == nil && !lastSeen.After(now) && now.Sub(lastSeen) < hookLivenessWriteInterval {
			return nil
		}
	}
	records[runtime] = HookLiveness{
		Runtime: runtime, LastSeen: now.Format(time.RFC3339Nano),
		Event: strings.TrimSpace(event),
	}
	body, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hook liveness: %w", err)
	}
	body = append(body, '\n')
	if _, err := atomicfile.WriteIfChanged(hookLivenessPath(root), body, 0o600); err != nil {
		return fmt.Errorf("write hook liveness: %w", err)
	}
	return nil
}

// ReadHookLiveness returns live activation evidence without mutating state.
func ReadHookLiveness(repoRoot string) (map[string]HookLiveness, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	return readHookLivenessResolved(root)
}

func readHookLivenessResolved(root string) (map[string]HookLiveness, error) {
	records := map[string]HookLiveness{}
	file, err := os.Open(hookLivenessPath(root))
	if os.IsNotExist(err) {
		return records, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read hook liveness: %w", err)
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxHookLivenessBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read hook liveness: %w", err)
	}
	if len(body) > maxHookLivenessBytes {
		return nil, fmt.Errorf("hook liveness exceeds %d bytes", maxHookLivenessBytes)
	}
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, fmt.Errorf("parse hook liveness: %w", err)
	}
	if len(records) > maxHookLivenessRuntimes {
		return nil, fmt.Errorf("hook liveness exceeds %d runtimes", maxHookLivenessRuntimes)
	}
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		record := records[key]
		if len(key) > 64 || len(record.Event) > 64 || record.Runtime != key || strings.TrimSpace(record.LastSeen) == "" {
			return nil, fmt.Errorf("invalid hook-liveness record for %q", key)
		}
		if _, err := time.Parse(time.RFC3339Nano, record.LastSeen); err != nil {
			return nil, fmt.Errorf("invalid hook-liveness timestamp for %q", key)
		}
	}
	return records, nil
}
