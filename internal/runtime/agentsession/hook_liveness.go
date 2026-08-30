package agentsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	hookLivenessWriteInterval  = 6 * time.Hour
	maxHookLivenessBytes       = 256 * 1024
	maxHookLivenessMarkerBytes = 2 * 1024
	maxHookObservationBytes    = 16 * 1024
	maxHookLivenessRuntimes    = 64
	// maxHookLivenessRoutes mirrors the read-side validation cap so the
	// writer never persists a record the next read would reject.
	maxHookLivenessRoutes = 32
)

const (
	hookLivenessMarkerVersion   = "hook-liveness-marker-v2"
	hookObservationStateVersion = "hook-observation-v1"
)

// HookLiveness is rate-limited proof that a runtime executed registry routes.
// It complements static hook configuration status.
type HookLiveness struct {
	Runtime      string                     `json:"runtime"`
	LastSeen     string                     `json:"last_seen"`
	Event        string                     `json:"event"`
	Routes       map[string]string          `json:"routes,omitempty"`
	Observations map[string]HookObservation `json:"observations,omitempty"`
}

// HookObservation is bounded, source-free metadata for a runtime surface that
// Reconc can observe but cannot decide. Count saturates instead of wrapping;
// LastSeen and the remaining fields describe the latest execution.
type HookObservation struct {
	Count              uint64 `json:"count"`
	LastSeen           string `json:"last_seen"`
	WorkingDirectory   string `json:"working_directory"`
	CodeBytes          int    `json:"code_bytes"`
	ExcludeFromContext bool   `json:"exclude_from_context"`
}

type hookLivenessMarker struct {
	FormatVersion   string `json:"format_version"`
	Runtime         string `json:"runtime"`
	Event           string `json:"event"`
	RouteSeen       string `json:"route_seen"`
	StateGeneration string `json:"state_generation"`
}

type hookObservationState struct {
	FormatVersion     string          `json:"format_version"`
	Runtime           string          `json:"runtime"`
	Event             string          `json:"event"`
	RouteSeen         string          `json:"route_seen"`
	PreviousRouteSeen string          `json:"previous_route_seen,omitempty"`
	Observation       HookObservation `json:"observation"`
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

func hookObservationPath(repoRoot, runtime, event string) string {
	return filepath.Join(projectDir(repoRoot), "hook-observations", runtime, event+".json")
}

// RecordHookLiveness records at most one timestamp per runtime route every six
// hours. A bounded marker and stable state generation keep the common path
// read-only without trusting a marker for a route that has been trimmed.
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
	if hookLivenessMarkerCurrent(root, runtime, event, now) {
		return nil
	}
	return withStateLock(hookLivenessLockPath(root), "hook liveness", func() error {
		records, err := readHookLivenessBaseResolved(root)
		if err != nil {
			return err
		}
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
				return writeHookLivenessMarker(root, runtime, event, routeSeen, lastSeen)
			}
		}
		current.Runtime = runtime
		current.LastSeen = now.Format(time.RFC3339Nano)
		current.Event = event
		current.Routes[event] = current.LastSeen
		removed := trimHookLivenessRoutes(&current, event)
		records[runtime] = current
		if err := writeHookLivenessRecords(root, records); err != nil {
			return err
		}
		if err := removeHookLivenessRouteArtifacts(root, runtime, removed); err != nil {
			return err
		}
		return writeHookLivenessMarker(root, runtime, event, current.LastSeen, now)
	})
}

func hookLivenessMarkerCurrent(root, runtime, event string, now time.Time) bool {
	markerPath := hookLivenessMarkerPath(root, runtime, event)
	body, err := boundedio.ReadRegularFile(markerPath, maxHookLivenessMarkerBytes)
	if err != nil {
		return false
	}
	var marker hookLivenessMarker
	if json.Unmarshal(body, &marker) != nil || marker.FormatVersion != hookLivenessMarkerVersion ||
		marker.Runtime != runtime || marker.Event != event {
		return false
	}
	routeSeen, err := time.Parse(time.RFC3339Nano, marker.RouteSeen)
	if err != nil || routeSeen.After(now) || now.Sub(routeSeen) >= hookLivenessWriteInterval {
		return false
	}
	generation, ok := hookLivenessStateGeneration(root)
	return ok && generation == marker.StateGeneration
}

func recordHookObservationResolved(root, runtime, event, workingDirectory string, codeBytes int, excludeFromContext bool) error {
	return recordHookObservationAt(root, runtime, event, workingDirectory, codeBytes, excludeFromContext, time.Now().UTC())
}

func recordHookObservationAt(root, runtime, event, workingDirectory string, codeBytes int, excludeFromContext bool, now time.Time) error {
	runtime = normalizeRuntimeName(runtime)
	event = strings.TrimSpace(event)
	if !validLivenessComponent(runtime) || !validLivenessComponent(event) {
		return fmt.Errorf("hook observation runtime and event must be lowercase ASCII identifiers of at most 64 bytes")
	}
	if codeBytes < 0 {
		return errors.New("hook observation code size must be non-negative")
	}
	relativeDirectory, err := relativeHookObservationDirectory(root, workingDirectory)
	if err != nil {
		return err
	}
	return withStateLock(hookLivenessLockPath(root), "hook liveness", func() error {
		records, err := readHookLivenessBaseResolved(root)
		if err != nil {
			return err
		}
		current := records[runtime]
		if current.Routes == nil {
			current.Routes = map[string]string{}
			if current.Event != "" && current.LastSeen != "" {
				current.Routes[current.Event] = current.LastSeen
			}
		}
		timestamp := now.Format(time.RFC3339Nano)
		routeSeen := current.Routes[event]
		routeRecorded := routeSeen != ""
		observation := HookObservation{}
		if routeRecorded {
			observation, _, err = readHookObservation(root, runtime, event, routeSeen)
			if err != nil {
				return err
			}
			if observation.Count == 0 {
				observation = current.Observations[event]
			}
		}
		if observation.Count < ^uint64(0) {
			observation.Count++
		}
		observation.LastSeen = timestamp
		observation.WorkingDirectory = relativeDirectory
		observation.CodeBytes = codeBytes
		observation.ExcludeFromContext = excludeFromContext
		routeTime, parseErr := time.Parse(time.RFC3339Nano, routeSeen)
		routeCurrent := parseErr == nil && !routeTime.After(now) && now.Sub(routeTime) < hookLivenessWriteInterval
		if !routeCurrent {
			previousRouteSeen := routeSeen
			if routeRecorded {
				if err := writeHookObservation(root, runtime, event, timestamp, previousRouteSeen, observation); err != nil {
					return err
				}
			}
			current.Runtime = runtime
			current.LastSeen = timestamp
			current.Event = event
			current.Routes[event] = timestamp
			removed := trimHookLivenessRoutes(&current, event)
			records[runtime] = current
			if err := writeHookLivenessRecords(root, records); err != nil {
				return err
			}
			if err := removeHookLivenessRouteArtifacts(root, runtime, removed); err != nil {
				return err
			}
			routeSeen = timestamp
			routeTime = now
		}
		if !routeRecorded || routeCurrent {
			if err := writeHookObservation(root, runtime, event, routeSeen, "", observation); err != nil {
				return err
			}
		}
		return writeHookLivenessMarker(root, runtime, event, routeSeen, routeTime)
	})
}

func relativeHookObservationDirectory(root, workingDirectory string) (string, error) {
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return "", fmt.Errorf("resolve hook observation repository root: %w", err)
	}
	resolvedDirectory, err := pathidentity.ResolveExisting(workingDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve hook observation working directory: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedDirectory)
	if err != nil {
		return "", fmt.Errorf("compare hook observation working directory: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("hook observation working directory is outside the repository")
	}
	relative = filepath.ToSlash(relative)
	if len(relative) > maxPathBytes {
		return "", fmt.Errorf("hook observation working directory exceeds %d bytes", maxPathBytes)
	}
	return relative, nil
}

func trimHookLivenessRoutes(record *HookLiveness, preserveEvent string) []string {
	var removed []string
	for len(record.Routes) > maxHookLivenessRoutes {
		oldestEvent, oldestSeen := "", ""
		for routeEvent, seen := range record.Routes {
			if routeEvent == preserveEvent {
				continue
			}
			if oldestEvent == "" || seen < oldestSeen {
				oldestEvent, oldestSeen = routeEvent, seen
			}
		}
		delete(record.Routes, oldestEvent)
		delete(record.Observations, oldestEvent)
		removed = append(removed, oldestEvent)
	}
	return removed
}

func writeHookLivenessRecords(root string, records map[string]HookLiveness) error {
	body, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hook liveness: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxHookLivenessBytes {
		return fmt.Errorf("hook liveness exceeds %d bytes", maxHookLivenessBytes)
	}
	if _, err := atomicfile.WritePrivateIfChanged(hookLivenessPath(root), body, 0o600); err != nil {
		return fmt.Errorf("write hook liveness: %w", err)
	}
	return nil
}

func writeHookLivenessMarker(root, runtime, event, timestamp string, modTime time.Time) error {
	markerPath := hookLivenessMarkerPath(root, runtime, event)
	generation, ok := hookLivenessStateGeneration(root)
	if !ok {
		if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove unverifiable hook-liveness marker: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o700); err != nil {
		return fmt.Errorf("create hook-liveness marker directory: %w", err)
	}
	markerBody, err := json.Marshal(hookLivenessMarker{
		FormatVersion: hookLivenessMarkerVersion, Runtime: runtime, Event: event,
		RouteSeen: timestamp, StateGeneration: generation,
	})
	if err != nil {
		return fmt.Errorf("marshal hook-liveness marker: %w", err)
	}
	markerBody = append(markerBody, '\n')
	if len(markerBody) > maxHookLivenessMarkerBytes {
		return fmt.Errorf("hook-liveness marker exceeds %d bytes", maxHookLivenessMarkerBytes)
	}
	if _, err := atomicfile.WritePrivateIfChanged(markerPath, markerBody, 0o600); err != nil {
		return fmt.Errorf("write hook-liveness marker: %w", err)
	}
	if err := os.Chtimes(markerPath, modTime, modTime); err != nil {
		return fmt.Errorf("timestamp hook-liveness marker: %w", err)
	}
	return nil
}

func hookLivenessStateGeneration(root string) (string, bool) {
	path := hookLivenessPath(root)
	before, err := os.Lstat(path)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return "", false
	}
	generation, ok := stopPathMetadataGeneration(path, before)
	if !ok {
		return "", false
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() ||
		before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", false
	}
	afterGeneration, ok := stopPathMetadataGeneration(path, after)
	return generation, ok && generation == afterGeneration
}

func removeHookLivenessRouteArtifacts(root, runtime string, events []string) error {
	for _, event := range events {
		if err := os.Remove(hookLivenessMarkerPath(root, runtime, event)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove trimmed hook-liveness marker: %w", err)
		}
		if err := os.Remove(hookObservationPath(root, runtime, event)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove trimmed hook observation: %w", err)
		}
	}
	return nil
}

func writeHookObservation(root, runtime, event, routeSeen, previousRouteSeen string, observation HookObservation) error {
	path := hookObservationPath(root, runtime, event)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create hook-observation directory: %w", err)
	}
	body, err := json.Marshal(hookObservationState{
		FormatVersion:     hookObservationStateVersion,
		Runtime:           runtime,
		Event:             event,
		RouteSeen:         routeSeen,
		PreviousRouteSeen: previousRouteSeen,
		Observation:       observation,
	})
	if err != nil {
		return fmt.Errorf("marshal hook observation: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxHookObservationBytes {
		return fmt.Errorf("hook observation exceeds %d bytes", maxHookObservationBytes)
	}
	if _, err := atomicfile.WritePrivateIfChanged(path, body, 0o600); err != nil {
		return fmt.Errorf("write hook observation: %w", err)
	}
	return nil
}

func readHookObservation(root, runtime, event, routeSeen string) (HookObservation, bool, error) {
	body, err := boundedio.ReadRegularFile(hookObservationPath(root, runtime, event), maxHookObservationBytes)
	if errors.Is(err, os.ErrNotExist) {
		return HookObservation{}, false, nil
	}
	if err != nil {
		return HookObservation{}, false, fmt.Errorf("read hook observation: %w", err)
	}
	var state hookObservationState
	if err := json.Unmarshal(body, &state); err != nil {
		return HookObservation{}, false, fmt.Errorf("parse hook observation: %w", err)
	}
	if state.FormatVersion != hookObservationStateVersion || state.Runtime != runtime || state.Event != event ||
		!validHookObservation(state.Observation) {
		return HookObservation{}, false, fmt.Errorf("invalid hook observation for %q route %q", runtime, event)
	}
	if state.RouteSeen != routeSeen && state.PreviousRouteSeen != routeSeen {
		return HookObservation{}, false, nil
	}
	if _, err := time.Parse(time.RFC3339Nano, state.RouteSeen); err != nil {
		return HookObservation{}, false, fmt.Errorf("invalid hook-observation route timestamp for %q route %q", runtime, event)
	}
	if state.PreviousRouteSeen != "" {
		if _, err := time.Parse(time.RFC3339Nano, state.PreviousRouteSeen); err != nil {
			return HookObservation{}, false, fmt.Errorf("invalid previous hook-observation route timestamp for %q route %q", runtime, event)
		}
	}
	return state.Observation, true, nil
}

func validHookObservation(observation HookObservation) bool {
	if observation.Count == 0 || observation.CodeBytes < 0 || observation.WorkingDirectory == "" ||
		len(observation.WorkingDirectory) > maxPathBytes || pathidentity.Rooted(observation.WorkingDirectory) ||
		pathidentity.EscapesLexically(observation.WorkingDirectory) {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, observation.LastSeen)
	return err == nil
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
	records, err := readHookLivenessBaseResolved(root)
	if err != nil {
		return nil, err
	}
	for runtime, record := range records {
		for event := range record.Routes {
			observation, exists, err := readHookObservation(root, runtime, event, record.Routes[event])
			if err != nil {
				return nil, err
			}
			if !exists {
				continue
			}
			if record.Observations == nil {
				record.Observations = map[string]HookObservation{}
			}
			record.Observations[event] = observation
			if observation.LastSeen > record.LastSeen {
				record.LastSeen = observation.LastSeen
				record.Event = event
			}
		}
		records[runtime] = record
	}
	return records, nil
}

func readHookLivenessBaseResolved(root string) (map[string]HookLiveness, error) {
	records := map[string]HookLiveness{}
	body, err := boundedio.ReadRegularFile(hookLivenessPath(root), maxHookLivenessBytes)
	if os.IsNotExist(err) {
		return records, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read hook liveness: %w", err)
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
		if len(record.Observations) > maxHookLivenessRoutes {
			return nil, fmt.Errorf("hook observations exceed %d routes for %q", maxHookLivenessRoutes, key)
		}
		for route, observation := range record.Observations {
			if !validLivenessComponent(route) || !validHookObservation(observation) {
				return nil, fmt.Errorf("invalid hook observation for %q route %q", key, route)
			}
			if _, exists := record.Routes[route]; !exists {
				return nil, fmt.Errorf("hook observation for %q route %q has no liveness route", key, route)
			}
		}
	}
	return records, nil
}
