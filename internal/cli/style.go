package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

type textStyler struct {
	enabled bool
}

func newTextStyler(writer io.Writer) textStyler {
	for {
		if file, ok := writer.(*os.File); ok {
			info, err := file.Stat()
			return textStyler{enabled: colorEnabled(err == nil && info.Mode()&os.ModeCharDevice != 0)}
		}
		unwrapper, ok := writer.(interface{ Unwrap() io.Writer })
		if !ok {
			return textStyler{}
		}
		next := unwrapper.Unwrap()
		if next == nil || next == writer {
			return textStyler{}
		}
		writer = next
	}
}

func colorEnabled(terminal bool) bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	return terminal
}

func (style textStyler) token(value, color string) string {
	if !style.enabled || value == "" {
		return value
	}
	return color + value + ansiReset
}

func (style textStyler) decision(value string) string {
	switch strings.ToLower(value) {
	case "pass", "allow", "allowed", "complete", "configured", "done", "yes":
		return style.token(value, ansiBold+ansiGreen)
	case "warn", "warning", "drift", "installed", "partial":
		return style.token(value, ansiBold+ansiYellow)
	case "block", "blocked", "fail", "failed", "error", "rolled_back", "degraded", "no":
		return style.token(value, ansiBold+ansiRed)
	default:
		return value
	}
}

func (style textStyler) ruleID(value string) string {
	return style.token(value, ansiBold+ansiCyan)
}

func (style textStyler) statusTag(status string, width int) string {
	padded := fmt.Sprintf("%-*s", width, status)
	switch strings.ToUpper(status) {
	case "OK", "PASS":
		return style.token(padded, ansiBold+ansiGreen)
	case "WARN":
		return style.token(padded, ansiBold+ansiYellow)
	case "FAIL":
		return style.token(padded, ansiBold+ansiRed)
	default:
		return padded
	}
}
