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
	// maxHookLivenessRoutes mirrors the read-side validation cap so the
	// writer never persists a record the next read would reject.
	maxHookLivenessRoutes = 32
)

// HookLiveness is rate-limited proof that a runtime executed registry routes.
// It complements static hook configuration status.
type HookLiveness struct {
	Runtime  string            `json:"runtime"`
	LastSeen string            `json:"last_seen"`
	Event    string            `json:"event"`
	Routes   map[string]string `json:"routes,omitempty"`
}

func hookLivenessPath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), "hook-liveness.json")
}

func hookLivenessLockPath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), "locks", "hook-liveness.lock")
}

func hookLivenessMarkerPath(repoRoot, runtime, event string) string {
	return filepath.Join(projectDir(repoRoot), "hook-liveness", runtime, event+".seen")
}

// RecordHookLiveness records at most one timestamp per runtime route every six
// hours. A tiny route marker makes the common path one stat and zero writes.
func RecordHookLiveness(repoRoot, runtime, event string) error {
	root, err := ResolveRepoRootRef(repoRoot)
	if err != nil {
		return err
	}
	return RecordHookLivenessResolved(root, runtime, event)
}

// RecordHookLivenessResolved records liveness without rediscovering a root
// already validated by the hook request boundary.
func RecordHookLivenessResolved(root ResolvedRepoRoot, runtime, event string) error {
	runtime = normalizeRuntimeName(runtime)
	if runtime == "" {
		return nil
	}
	event = strings.TrimSpace(event)
	if !validLivenessComponent(runtime) || !validLivenessComponent(event) {
		return fmt.Errorf("hook-liveness runtime and event must be lowercase ASCII identifiers of at most 64 bytes")
	}
	return recordHookLivenessAt(root.path, runtime, event, time.Now().UTC())
}

func recordHookLivenessAt(root, runtime, event string, now time.Time) error {
	markerPath := hookLivenessMarkerPath(root, runtime, event)
	if info, err := os.Stat(markerPath); err == nil && !info.ModTime().After(now) && now.Sub(info.ModTime()) < hookLivenessWriteInterval {
		return nil
	}
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
	event = strings.TrimSpace(event)
	current := records[runtime]
	if current.Routes == nil {
		current.Routes = map[string]string{}
		if current.Event != "" && current.LastSeen != "" {
			current.Routes[current.Event] = current.LastSeen
		}
	}
	if routeSeen := current.Routes[event]; routeSeen != "" {
		lastSeen, parseErr := time.Parse(time.RFC3339Nano, routeSeen)
		if parseErr == nil && !lastSeen.After(now) && now.Sub(lastSeen) < hookLivenessWriteInterval {
			return writeHookLivenessMarker(markerPath, routeSeen, lastSeen)
		}
	}
	current.Runtime = runtime
	current.LastSeen = now.Format(time.RFC3339Nano)
	current.Event = event
	current.Routes[event] = current.LastSeen
	if len(current.Routes) > maxHookLivenessRoutes {
		oldestEvent, oldestSeen := "", ""
		for routeEvent, seen := range current.Routes {
			if oldestEvent == "" || seen < oldestSeen {
				oldestEvent, oldestSeen = routeEvent, seen
			}
		}
		delete(current.Routes, oldestEvent)
	}
	records[runtime] = HookLiveness{
		Runtime: current.Runtime, LastSeen: current.LastSeen, Event: current.Event,
		Routes: current.Routes,
	}
	body, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hook liveness: %w", err)
	}
	body = append(body, '\n')
	if _, err := atomicfile.WriteIfChanged(hookLivenessPath(root), body, 0o600); err != nil {
		return fmt.Errorf("write hook liveness: %w", err)
	}
	return writeHookLivenessMarker(markerPath, current.LastSeen, now)
}

func writeHookLivenessMarker(markerPath, timestamp string, modTime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o700); err != nil {
		return fmt.Errorf("create hook-liveness marker directory: %w", err)
	}
	markerBody := []byte(timestamp + "\n")
	if _, err := atomicfile.WriteIfChanged(markerPath, markerBody, 0o600); err != nil {
		return fmt.Errorf("write hook-liveness marker: %w", err)
	}
	if err := os.Chtimes(markerPath, modTime, modTime); err != nil {
		return fmt.Errorf("timestamp hook-liveness marker: %w", err)
	}
	return nil
}

func validLivenessComponent(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
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
		if !validLivenessComponent(key) || (record.Event != "" && !validLivenessComponent(record.Event)) || record.Runtime != key || strings.TrimSpace(record.LastSeen) == "" || len(record.Routes) > 32 {
			return nil, fmt.Errorf("invalid hook-liveness record for %q", key)
		}
		if _, err := time.Parse(time.RFC3339Nano, record.LastSeen); err != nil {
			return nil, fmt.Errorf("invalid hook-liveness timestamp for %q", key)
		}
		for route, lastSeen := range record.Routes {
			if !validLivenessComponent(route) {
				return nil, fmt.Errorf("invalid hook-liveness route for %q", key)
			}
			if _, err := time.Parse(time.RFC3339Nano, lastSeen); err != nil {
				return nil, fmt.Errorf("invalid hook-liveness route timestamp for %q", key)
			}
		}
	}
	return records, nil
}
