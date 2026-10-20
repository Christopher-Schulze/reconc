package cli

import (
	"bytes"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/usercli"
)

func TestUpdateProgressPlainOutputPrintsOneLinePerFinishedPhase(t *testing.T) {
	var out bytes.Buffer
	view := &updateProgressView{out: &out}
	for _, event := range []usercli.UpdateEvent{
		{Phase: usercli.UpdatePhaseInstallation, Kind: usercli.UpdateEventStart},
		{Phase: usercli.UpdatePhaseInstallation, Kind: usercli.UpdateEventPass, Detail: "direct · /bin/reconc"},
		{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventStart},
		{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventProgress, Done: 512, Total: 3 << 20},
		{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventPass, Detail: "SHA-256 verified", Total: 3 << 20},
		{Phase: usercli.UpdatePhaseProvenance, Kind: usercli.UpdateEventStart},
	} {
		view.observe(event)
	}
	view.close()
	want := "  ✓ Installation  direct · /bin/reconc\n" +
		"  ✓ Download      3.0 MiB · SHA-256 verified\n" +
		"  ✗ Provenance\n"
	if out.String() != want {
		t.Fatalf("plain progress =\n%q\nwant\n%q", out.String(), want)
	}
}

func TestUpdateProgressLiveOutputAnimatesTheRunningPhaseInPlace(t *testing.T) {
	var out bytes.Buffer
	view := &updateProgressView{out: &out, style: textStyler{enabled: true}, live: true}
	view.observe(usercli.UpdateEvent{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventStart})
	view.observe(usercli.UpdateEvent{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventProgress, Done: 50, Total: 100})
	view.mu.Lock()
	view.drawRunning()
	view.mu.Unlock()
	view.observe(usercli.UpdateEvent{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventPass, Detail: "SHA-256 verified", Total: 100})
	view.close()
	text := out.String()
	bar := strings.Repeat("█", progressBarCells/2) + strings.Repeat("░", progressBarCells/2) + "  50%  50 B / 100 B"
	if !strings.Contains(text, ansiClearLine) || !strings.Contains(text, bar) {
		t.Fatalf("live progress did not redraw a half-filled bar in place: %q", text)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	last := lines[len(lines)-1]
	if len(lines) != 1 || !strings.HasPrefix(last, ansiClearLine) || !strings.Contains(last, "Download") ||
		!strings.Contains(last, "100 B · SHA-256 verified") {
		t.Fatalf("live progress must end with one finished line, got %q", text)
	}
}

func TestUpdateProgressBarClampsAndRoundsDown(t *testing.T) {
	for _, test := range []struct {
		done, total int64
		filled      int
		percent     string
	}{
		{done: 0, total: 10, filled: 0, percent: "  0%"},
		{done: 9, total: 10, filled: 21, percent: " 90%"},
		{done: 15, total: 10, filled: progressBarCells, percent: "100%"},
	} {
		got := progressBar(test.done, test.total)
		want := strings.Repeat("█", test.filled) + strings.Repeat("░", progressBarCells-test.filled) + " " + test.percent
		if got != want {
			t.Fatalf("progressBar(%d, %d) = %q, want %q", test.done, test.total, got, want)
		}
	}
}

func TestLifecycleTextSummaryShowsOnlyChecksThatNeedAttention(t *testing.T) {
	owner := usercli.ManagerDirect
	target := "1.1.0"
	report := &usercli.LifecycleReport{
		Operation: "update.apply", Status: usercli.LifecycleUpdated, Owner: &owner,
		CurrentVersion: "1.0.0", TargetVersion: &target,
		Skill: &usercli.SkillUpdateReport{State: usercli.SkillCurrent},
		Checks: []usercli.DiagnosticCheck{
			{Name: "receipt-binary", Status: "pass", Detail: "checksum matches"},
			{Name: "path-shadows", Status: "warn", Detail: "1 additional executable(s): /opt/reconc"},
		},
		NextAction: "Run `reconc doctor --global` to verify the updated installation.",
	}
	var out bytes.Buffer
	if err := writeLifecycleReport(&out, report, false); err != nil {
		t.Fatal(err)
	}
	want := "Updated: reconc 1.0.0 → 1.1.0 (direct), skill current\n" +
		"  WARN path-shadows: 1 additional executable(s): /opt/reconc\n" +
		"Next: Run `reconc doctor --global` to verify the updated installation.\n"
	if out.String() != want {
		t.Fatalf("lifecycle summary =\n%q\nwant\n%q", out.String(), want)
	}
}
