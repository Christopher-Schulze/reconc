package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
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
	return textStyler{enabled: colorEnabled(writerIsTerminal(writer))}
}

// writerIsTerminal reports whether writer, or a writer it wraps, is a terminal.
func writerIsTerminal(writer io.Writer) bool {
	file := wrappedFile(writer)
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// terminalColumns reports the width of the terminal behind writer, 80 when the
// terminal does not report its size, and 0 when writer is not a terminal.
func terminalColumns(writer io.Writer) int {
	if !writerIsTerminal(writer) {
		return 0
	}
	width, _, err := term.GetSize(int(wrappedFile(writer).Fd()))
	if err != nil || width <= 0 {
		return 80
	}
	return width
}

// wrappedFile returns the file behind writer or a writer it wraps.
func wrappedFile(writer io.Writer) *os.File {
	for {
		if file, ok := writer.(*os.File); ok {
			return file
		}
		unwrapper, ok := writer.(interface{ Unwrap() io.Writer })
		if !ok {
			return nil
		}
		next := unwrapper.Unwrap()
		if next == nil || next == writer {
			return nil
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
