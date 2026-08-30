package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
)

const maxCLIOutputBytes int64 = (1 << 63) - 2

func teeToFile(w io.Writer, path string) (io.Writer, func(bool) error, error) {
	if path == "" {
		tracked := &trackedOutputWriter{writer: w}
		return tracked, func(bool) error { return tracked.Err() }, nil
	}
	file, err := os.CreateTemp("", ".reconc-cli-output-*.tmp")
	if err != nil {
		return nil, nil, err
	}
	tracked := &trackedOutputWriter{writer: io.MultiWriter(w, file)}
	finished := false
	closeOutput := func(commit bool) error {
		if finished {
			return nil
		}
		finished = true
		trackedErr := tracked.Err()
		if !commit || trackedErr != nil {
			return errors.Join(trackedErr, file.Close(), os.Remove(file.Name()))
		}
		if err := file.Sync(); err != nil {
			return errors.Join(trackedErr, err, file.Close(), os.Remove(file.Name()))
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return errors.Join(trackedErr, err, file.Close(), os.Remove(file.Name()))
		}
		_, publishErr := atomicfile.WriteStream(path, file, maxCLIOutputBytes, 0o644)
		return errors.Join(trackedErr, publishErr, file.Close(), os.Remove(file.Name()))
	}
	return tracked, closeOutput, nil
}

func joinOutputCloseError(resultErr *error, closeOutput func(bool) error) {
	*resultErr = errors.Join(*resultErr, closeOutput(*resultErr == nil))
}

func commitOutput(closeOutput func(bool) error, result error) error {
	return errors.Join(result, closeOutput(true))
}

type argValueSyntax uint8

const (
	argValueNoLeadingDash argValueSyntax = iota
	argValueLeadingDashAfterSeparator
)

// nextArgValue advances i only after finding a complete value. Values that
// begin with a dash must be explicitly escaped as "-- VALUE" by callers that
// opt into argValueLeadingDashAfterSeparator.
func nextArgValue(args []string, i *int, flag string, syntax argValueSyntax) (string, bool) {
	next := *i + 1
	if next >= len(args) {
		return "", false
	}
	value := args[next]
	if strings.HasPrefix(value, "-") {
		if syntax != argValueLeadingDashAfterSeparator || value != "--" || next+1 >= len(args) {
			return "", false
		}
		next++
		value = args[next]
	}
	*i = next
	return value, true
}

func firstStringOrDash(values []string) string {
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return "-"
	}
	return values[0]
}

// detectCIEnvironment reports whether this process is running inside a
// known CI environment. Used by --auto-claim (W7) to silently assert
// ci-green so hosted pipelines don't need a separate `reconc hook
// claim` step.
//
// Detection is conservative: we only return true for environment
// variables that ONLY CI systems set. A local developer with
// CI_GREEN=true or similar will NOT trigger a false positive.
func detectCIEnvironment() bool {
	// "CI=true" is the cross-provider convention (GitHub Actions,
	// GitLab CI, CircleCI, Travis, Drone, Buildkite, Semaphore, ...).
	if v, ok := os.LookupEnv("CI"); ok {
		vv := strings.ToLower(strings.TrimSpace(v))
		if vv == "1" || vv == "true" || vv == "on" || vv == "yes" {
			return true
		}
	}
	// Provider-specific fallback markers (always set by their
	// respective runners, even when CI= isn't).
	for _, key := range []string{
		"GITHUB_ACTIONS", "GITLAB_CI", "CIRCLECI", "TRAVIS",
		"JENKINS_URL", "BUILDKITE", "DRONE", "APPVEYOR",
		"TEAMCITY_VERSION", "BITBUCKET_BUILD_NUMBER",
	} {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			return true
		}
	}
	return false
}

// splitCommaList splits "a,b,c" into ["a","b","c"] trimming whitespace.
// Returns nil for empty input so the caller falls back to defaults.
func splitCommaList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sortedMapKeys returns a map's keys alphabetically for stable display.
func sortedMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// sortedKeys returns a map's string keys sorted ascending. Tiny helper
// used by audit stats for deterministic human output.
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// atoi parses a base-10 integer for flag handling. It delegates to
// strconv.Atoi so oversized inputs fail cleanly instead of silently
// wrapping modulo 2^64 and slipping past the callers' range guards.
func atoi(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", s)
	}
	return n, nil
}
