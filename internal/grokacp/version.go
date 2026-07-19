package grokacp

import (
	"regexp"
	"strconv"
)

// NativeStopGateMinimumVersion is the first Grok Build source version whose
// Stop hook accepts a blocking decision and continues the same turn.
const NativeStopGateMinimumVersion = "0.2.106"

var grokVersionPattern = regexp.MustCompile(`(?:^|[^0-9])v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[^0-9]|$)`)

// SupportsNativeStopGate reports whether a Grok version advertises the native
// blocking Stop-hook contract. Unknown and malformed versions fail closed to
// the backward-compatible leader fallback.
func SupportsNativeStopGate(raw string) bool {
	match := grokVersionPattern.FindStringSubmatch(raw)
	if len(match) != 4 {
		return false
	}
	major, majorErr := strconv.Atoi(match[1])
	minor, minorErr := strconv.Atoi(match[2])
	patch, patchErr := strconv.Atoi(match[3])
	if majorErr != nil || minorErr != nil || patchErr != nil {
		return false
	}
	return major > 0 || minor > 2 || minor == 2 && patch >= 106
}
