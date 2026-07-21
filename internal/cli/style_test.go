package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestTextStylerUsesANSIOnlyWhenEnabled(t *testing.T) {
	plain := newTextStyler(&bytes.Buffer{})
	if got := plain.decision("block"); got != "block" {
		t.Fatalf("non-TTY decision contains styling: %q", got)
	}
	if got := plain.statusTag("FAIL", 4); got != "FAIL" {
		t.Fatalf("non-TTY status contains styling: %q", got)
	}

	colored := textStyler{enabled: true}
	for _, got := range []string{colored.decision("pass"), colored.decision("block"), colored.ruleID("scope-lock"), colored.statusTag("WARN", 4)} {
		if !strings.Contains(got, "\x1b[") || !strings.HasSuffix(got, ansiReset) {
			t.Fatalf("enabled style lacks bounded ANSI sequence: %q", got)
		}
	}
}

func TestColorEnabledHonorsNoColorAndDumbTerminal(t *testing.T) {
	t.Run("NO_COLOR", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("TERM", "xterm-256color")
		if colorEnabled(true) {
			t.Fatal("NO_COLOR presence did not disable ANSI output")
		}
	})
	t.Run("dumb-terminal", func(t *testing.T) {
		previous, existed := os.LookupEnv("NO_COLOR")
		if err := os.Unsetenv("NO_COLOR"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv("NO_COLOR", previous)
			} else {
				_ = os.Unsetenv("NO_COLOR")
			}
		})
		t.Setenv("TERM", "dumb")
		if colorEnabled(true) {
			t.Fatal("TERM=dumb did not disable ANSI output")
		}
	})
}

func TestTextStylerUnwrapsTrackedTerminalWriter(t *testing.T) {
	file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		t.Skip("platform null device is not reported as a character device")
	}
	previous, existed := os.LookupEnv("NO_COLOR")
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("NO_COLOR", previous)
		} else {
			_ = os.Unsetenv("NO_COLOR")
		}
	})
	t.Setenv("TERM", "xterm-256color")

	style := newTextStyler(&trackedOutputWriter{writer: file})
	if !style.enabled {
		t.Fatal("tracked output writer hid an interactive terminal")
	}
}
