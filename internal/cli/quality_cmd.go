package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// runSpec implements `reconc spec check [repo] [--file PATH] [--max-age-days N]` (W49).
//
// Verifies that a project-convention specification file exists and is
// reasonably fresh. Default target is docs/spec.md but any path works
// via --file. Exit 1 on missing file or staleness breach.
func runSpec(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc spec: missing subcommand (check)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc spec check [repo] [--file PATH] [--max-age-days N] [--json]")
			fmt.Fprintln(stdout, "Defaults: --file docs/spec.md (no max-age).")
			return nil
		}
	}
	sub := args[0]
	if sub != "check" {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc spec: unknown subcommand %q (expected 'check')", sub)}
	}

	repo := "."
	file := "docs/spec.md"
	maxAgeDays := 0
	jsonOut := false
	rest := args[1:]
	i := 0
	for i < len(rest) {
		a := rest[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--file":
			if i+1 >= len(rest) {
				return &CLIError{ExitCode: 1, Message: "reconc spec check: --file requires a path"}
			}
			file = rest[i+1]
			i++
		case "--max-age-days":
			if i+1 >= len(rest) {
				return &CLIError{ExitCode: 1, Message: "reconc spec check: --max-age-days requires an integer"}
			}
			n, err := atoi(rest[i+1])
			if err != nil || n <= 0 {
				return &CLIError{ExitCode: 1, Message: "reconc spec check: --max-age-days must be a positive integer"}
			}
			maxAgeDays = n
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc spec check: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc spec check: " + err.Error()}
	}
	target := filepath.Join(abs, file)
	info, err := os.Stat(target)
	result := map[string]interface{}{
		"repo_root": abs,
		"file":      file,
		"exists":    false,
		"stale":     false,
	}
	ok := true
	var reason string
	if err != nil {
		reason = "file not found"
		ok = false
	} else {
		result["exists"] = true
		result["size_bytes"] = info.Size()
		result["modified"] = info.ModTime().UTC().Format(time.RFC3339)
		if maxAgeDays > 0 {
			ageDays := int(time.Since(info.ModTime()).Hours() / 24)
			result["age_days"] = ageDays
			if ageDays > maxAgeDays {
				result["stale"] = true
				reason = fmt.Sprintf("last modified %d days ago (max %d)", ageDays, maxAgeDays)
				ok = false
			}
		}
	}
	result["ok"] = ok
	if reason != "" {
		result["reason"] = reason
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		if !ok {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}
	if ok {
		fmt.Fprintf(stdout, "[OK  ] %s present\n", file)
		if m, ok2 := result["modified"].(string); ok2 {
			fmt.Fprintf(stdout, "       modified %s\n", m)
		}
		return nil
	}
	fmt.Fprintf(stdout, "[FAIL] %s: %s\n", file, reason)
	return &CLIError{ExitCode: 1, Message: ""}
}

// runCoverage implements `reconc coverage check [repo] [--file PATH] [--min-pct N]` (W50).
//
// Reads a simple coverage file, extracts the first numeric percentage,
// and compares it to --min-pct (default 80). Supports the most common
// artefact formats: "XX.X%" text files, single-line number, `go test
// -cover` "ok  pkg  1.2s  coverage: 87.5% of statements" output.
// Exit 1 on missing file / below minimum.
func runCoverage(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc coverage: missing subcommand (check)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc coverage check [repo] [--file PATH] [--min-pct N] [--json]")
			fmt.Fprintln(stdout, "Defaults: --file coverage.txt, --min-pct 80.")
			fmt.Fprintln(stdout, "Supports text with XX.X%, bare number, or `go test -cover` output.")
			return nil
		}
	}
	sub := args[0]
	if sub != "check" {
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc coverage: unknown subcommand %q (expected 'check')", sub)}
	}

	repo := "."
	file := "coverage.txt"
	minPct := 80.0
	jsonOut := false
	rest := args[1:]
	i := 0
	for i < len(rest) {
		a := rest[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--file":
			if i+1 >= len(rest) {
				return &CLIError{ExitCode: 1, Message: "reconc coverage check: --file requires a path"}
			}
			file = rest[i+1]
			i++
		case "--min-pct":
			if i+1 >= len(rest) {
				return &CLIError{ExitCode: 1, Message: "reconc coverage check: --min-pct requires a value"}
			}
			v, err := parseFloatPct(rest[i+1])
			if err != nil {
				return &CLIError{ExitCode: 1, Message: "reconc coverage check: --min-pct must be a number between 0 and 100"}
			}
			minPct = v
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc coverage check: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc coverage check: " + err.Error()}
	}
	target := filepath.Join(abs, file)
	data, err := os.ReadFile(target)
	if err != nil {
		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(map[string]interface{}{
				"repo_root": abs, "file": file, "ok": false,
				"reason": "file not found",
			})
		} else {
			fmt.Fprintf(stdout, "[FAIL] %s: file not found\n", file)
		}
		return &CLIError{ExitCode: 1, Message: ""}
	}
	pct, ok := firstPercent(string(data))
	if !ok {
		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(map[string]interface{}{
				"repo_root": abs, "file": file, "ok": false,
				"reason": "no percentage value found in file",
			})
		} else {
			fmt.Fprintf(stdout, "[FAIL] %s: no percentage value found\n", file)
		}
		return &CLIError{ExitCode: 1, Message: ""}
	}

	passed := pct >= minPct
	result := map[string]interface{}{
		"repo_root": abs,
		"file":      file,
		"min_pct":   minPct,
		"found_pct": pct,
		"ok":        passed,
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		if !passed {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}
	status := "OK  "
	if !passed {
		status = "FAIL"
	}
	fmt.Fprintf(stdout, "[%s] coverage %.1f%% (min %.1f%%)\n", status, pct, minPct)
	if !passed {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

// firstPercent extracts the coverage percentage from s. Accepts:
//   - "87.5%"       -> 87.5
//   - "coverage: 87.5% of statements"  -> 87.5
//   - "87.5"        -> 87.5
//
// A number directly followed by '%' wins over any earlier bare number,
// so a real `go test -cover` line ("ok  pkg  0.012s  coverage: 87.5% of
// statements") yields the coverage, not the duration. Files without any
// percent sign fall back to the first bare number.
//
// Returns (pct, true) on success, (0, false) otherwise.
func firstPercent(s string) (float64, bool) {
	if pct, ok := scanCoverageNumber(s, true); ok {
		return pct, true
	}
	return scanCoverageNumber(s, false)
}

// scanCoverageNumber finds the first digit run (optionally with a
// decimal point) in s. With requirePercent set, only a run immediately
// followed by '%' counts.
func scanCoverageNumber(s string, requirePercent bool) (float64, bool) {
	var buf []byte
	seenDigit := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			buf = append(buf, c)
			seenDigit = true
		case c == '.' && seenDigit:
			buf = append(buf, c)
		default:
			if seenDigit && (!requirePercent || c == '%') {
				return parseFloatSimple(string(buf))
			}
			buf = nil
			seenDigit = false
		}
	}
	if seenDigit && !requirePercent {
		return parseFloatSimple(string(buf))
	}
	return 0, false
}

// parseFloatSimple parses a numeric string like "87.5" into a float.
// Uses basic char-walk to avoid a strconv import here (keeping this
// file's dep surface minimal).
func parseFloatSimple(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	var whole, frac int64
	fracDigits := 0
	seenDot := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			if seenDot {
				return 0, false
			}
			seenDot = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, false
		}
		if seenDot {
			frac = frac*10 + int64(c-'0')
			fracDigits++
		} else {
			whole = whole*10 + int64(c-'0')
		}
	}
	result := float64(whole)
	if fracDigits > 0 {
		div := 1.0
		for i := 0; i < fracDigits; i++ {
			div *= 10
		}
		result += float64(frac) / div
	}
	return result, true
}

// parseFloatPct validates a --min-pct argument (0-100, float).
func parseFloatPct(s string) (float64, error) {
	v, ok := parseFloatSimple(s)
	if !ok || v < 0 || v > 100 {
		return 0, fmt.Errorf("invalid percentage %q", s)
	}
	return v, nil
}
