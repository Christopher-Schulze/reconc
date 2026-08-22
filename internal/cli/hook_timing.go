package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"reconc.dev/reconc/internal/hooks"
)

type hookRuntimeTiming struct {
	enabled    bool
	event      string
	diagnostic io.Writer
	probe      *os.File
	startedAt  time.Time
	lastMark   time.Time
	stages     []string
}

var hookTimingProbeFile = func(fd int) *os.File {
	return os.NewFile(uintptr(fd), "reconc-hook-timing")
}

func newHookRuntimeTiming(event string, stderr io.Writer) hookRuntimeTiming {
	diagnosticEnabled := strings.TrimSpace(os.Getenv("RECONC_HOOK_TIMING")) != "" ||
		strings.TrimSpace(os.Getenv("RECONC_HOOK_TIMING_THRESHOLD_MS")) != ""
	var probe *os.File
	if rawFD := strings.TrimSpace(os.Getenv("RECONC_HOOK_TIMING_FD")); rawFD != "" {
		fd, err := atoi(rawFD)
		if err == nil && fd >= 3 && fd <= 4096 {
			probe = hookTimingProbeFile(fd)
		}
	}
	if !diagnosticEnabled && probe == nil {
		return hookRuntimeTiming{}
	}
	now := time.Now()
	var diagnostic io.Writer
	if diagnosticEnabled {
		diagnostic = stderr
	}
	return hookRuntimeTiming{
		enabled:    true,
		event:      event,
		diagnostic: diagnostic,
		probe:      probe,
		startedAt:  now,
		lastMark:   now,
	}
}

func (t *hookRuntimeTiming) mark(name string) {
	if t == nil || !t.enabled {
		return
	}
	now := time.Now()
	t.stages = append(t.stages, fmt.Sprintf("%s=%s", name, now.Sub(t.lastMark).Round(time.Microsecond)))
	t.lastMark = now
}

func (t *hookRuntimeTiming) finish(exitCode int) {
	if t == nil || !t.enabled {
		return
	}
	total := time.Since(t.startedAt).Round(time.Microsecond)
	if t.probe != nil {
		fmt.Fprintf(t.probe, "duration_ns=%d\n", total.Nanoseconds())
		_ = t.probe.Close()
		t.probe = nil
	}
	if t.diagnostic == nil {
		return
	}
	if threshold := hookRuntimeTimingThreshold(); threshold > 0 && total < threshold {
		return
	}
	parts := []string{
		"reconc hook timing:",
		"event=" + t.event,
		fmt.Sprintf("exit=%d", exitCode),
		"total=" + total.String(),
	}
	parts = append(parts, t.stages...)
	fmt.Fprintln(t.diagnostic, strings.Join(parts, " "))
}

// maxHookTimingThresholdMS bounds the diagnostic threshold at one hour, far
// past any hook budget the registry grants.
const maxHookTimingThresholdMS = 60 * 60 * 1000

func hookRuntimeTimingThreshold() time.Duration {
	raw := strings.TrimSpace(os.Getenv("RECONC_HOOK_TIMING_THRESHOLD_MS"))
	if raw == "" {
		return 0
	}
	ms, err := atoi(raw)
	if err != nil || ms <= 0 {
		return 0
	}
	// Clamp before the conversion: an operator value beyond the duration range
	// wraps negative, which would turn "print only slow hooks" into "print
	// every hook".
	if ms > maxHookTimingThresholdMS {
		ms = maxHookTimingThresholdMS
	}
	return time.Duration(ms) * time.Millisecond
}

func isObservationOnlyHookEvent(event string) bool {
	route, ok := hooks.RuntimeEvent(event)
	if !ok {
		return false
	}
	if route.PlatformKind == hooks.KindCursor &&
		(route.Event == hooks.EventUserPromptSubmit || route.Event == hooks.EventSubagentStart) {
		return false
	}
	switch route.Event {
	case hooks.EventUserPromptSubmit,
		hooks.EventPermissionDenied,
		hooks.EventPermissionResult,
		hooks.EventPostToolUse,
		hooks.EventPostToolUseFailure,
		hooks.EventToolObservation,
		hooks.EventContinuation,
		hooks.EventStopFailure,
		hooks.EventInterrupt,
		hooks.EventSessionEnd,
		hooks.EventNotification,
		hooks.EventSubagentStart,
		hooks.EventSubagentStop,
		hooks.EventPreCompaction,
		hooks.EventPostCompaction,
		hooks.EventWorkspaceOpen:
		return true
	default:
		return false
	}
}

// runHookClaim appends one explicit claim to the active session state.
