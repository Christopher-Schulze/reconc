package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func teeToFile(w io.Writer, path string) (io.Writer, func() error, error) {
	if path == "" {
		tracked := &trackedOutputWriter{writer: w}
		return tracked, tracked.Err, nil
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	tracked := &trackedOutputWriter{writer: io.MultiWriter(w, file)}
	closeOutput := func() error {
		return errors.Join(tracked.Err(), file.Sync(), file.Close())
	}
	return tracked, closeOutput, nil
}

func joinOutputCloseError(resultErr *error, closeOutput func() error) {
	*resultErr = errors.Join(*resultErr, closeOutput())
}

// nextArgValue advances i and returns the next argument as the value
// for a flag. Returns ("", false) if i is at the end.
func nextArgValue(args []string, i *int, flag string) (string, bool) {
	*i++
	if *i >= len(args) {
		return "", false
	}
	return args[*i], true
}

// splitOnce splits s on the first occurrence of sep into 2 parts.
// Returns one part if sep is absent.
func splitOnce(s, sep string) []string {
	for i := 0; i < len(s); i++ {
		if i+len(sep) <= len(s) && s[i:i+len(sep)] == sep {
			return []string{s[:i], s[i+len(sep):]}
		}
	}
	return []string{s}
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

// itoaCLI is a local int->string used in session-briefing messages.
// Avoids pulling strconv into this file for one format site.
func itoaCLI(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// sortedMapKeys returns a map's keys alphabetically for stable display.
func sortedMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// sortedKeys returns a map's string keys sorted ascending. Tiny helper
// used by audit stats for deterministic human output.
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// sort.Strings lives in "sort"; we keep things dep-free by sorting
	// inline rather than importing sort here (stable for small N).
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
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

// joinList joins string slice with ", " separator.
func joinList(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}
