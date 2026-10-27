package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

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
		partial     string
		percent     string
	}{
		{done: 0, total: 10, filled: 0, percent: "  0%"},
		{done: 9, total: 10, filled: 21, partial: "▌", percent: " 90%"},
		{done: 1, total: 200, filled: 0, percent: "  0%"},
		{done: 1, total: 100, filled: 0, partial: "▏", percent: "  1%"},
		{done: 15, total: 10, filled: progressBarCells, percent: "100%"},
	} {
		got := progressBar(test.done, test.total)
		empty := progressBarCells - test.filled
		if test.partial != "" {
			empty--
		}
		want := strings.Repeat("█", test.filled) + test.partial + strings.Repeat("░", empty) + " " + test.percent
		if got != want {
			t.Fatalf("progressBar(%d, %d) = %q, want %q", test.done, test.total, got, want)
		}
	}
}

// TestUpdateProgressLiveShowsSpeedAndRemainingTime drives the live view with
// a fixed clock: the running download shows the smoothed speed and remaining
// time, and the finished line keeps the size and average speed.
func TestUpdateProgressLiveShowsSpeedAndRemainingTime(t *testing.T) {
	start := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	now := start
	var out bytes.Buffer
	view := &updateProgressView{out: &out, style: textStyler{}, live: true, now: func() time.Time { return now }}
	view.observe(usercli.UpdateEvent{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventStart})
	for _, step := range []struct {
		at   time.Duration
		done int64
	}{{100 * time.Millisecond, 1 << 19}, {500 * time.Millisecond, 1 << 20}, {time.Second, 2 << 20}} {
		now = start.Add(step.at)
		view.observe(usercli.UpdateEvent{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventProgress, Done: step.done, Total: 4 << 20})
	}
	view.mu.Lock()
	view.drawRunning()
	view.mu.Unlock()
	running := out.String()
	if !strings.Contains(running, "2.0 MiB / 4.0 MiB  2.0 MiB/s · 1s left") {
		t.Fatalf("running download lacks speed and remaining time: %q", running)
	}
	now = start.Add(2 * time.Second)
	view.observe(usercli.UpdateEvent{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventPass, Detail: "SHA-256 verified", Total: 4 << 20})
	view.close()
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "4.0 MiB · 2.0 MiB/s · SHA-256 verified  2.0s") {
		t.Fatalf("finished download lacks its average speed: %q", last)
	}
}

// TestUpdateProgressLiveMarksAStalledDownload proves that a download without
// new bytes stops showing its last speed and remaining time, and that both
// return with the next bytes.
func TestUpdateProgressLiveMarksAStalledDownload(t *testing.T) {
	start := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	now := start
	var out bytes.Buffer
	view := &updateProgressView{out: &out, style: textStyler{}, live: true, now: func() time.Time { return now }}
	view.observe(usercli.UpdateEvent{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventStart})
	draw := func() string {
		out.Reset()
		view.mu.Lock()
		view.drawRunning()
		view.mu.Unlock()
		return out.String()
	}
	progress := func(at time.Duration, done int64) {
		now = start.Add(at)
		view.observe(usercli.UpdateEvent{Phase: usercli.UpdatePhaseDownload, Kind: usercli.UpdateEventProgress, Done: done, Total: 4 << 20})
	}
	progress(500*time.Millisecond, 1<<20)
	now = start.Add(time.Second)
	if running := draw(); !strings.Contains(running, "2.0 MiB/s · 2s left") {
		t.Fatalf("control: the running download lacks speed and remaining time: %q", running)
	}
	now = start.Add(500*time.Millisecond + stallShown)
	if stalled := draw(); !strings.Contains(stalled, "1.0 MiB / 4.0 MiB  stalled") || strings.Contains(stalled, "left") {
		t.Fatalf("a stalled download kept its last speed: %q", stalled)
	}
	progress(500*time.Millisecond+stallShown+time.Second, 2<<20)
	if resumed := draw(); !strings.Contains(resumed, "left") || strings.Contains(resumed, "stalled") {
		t.Fatalf("a resumed download did not show its speed again: %q", resumed)
	}
}

func TestUpdateProgressFormatsRateAndRemainingTime(t *testing.T) {
	for remaining, want := range map[time.Duration]string{
		0: "<1s", 400 * time.Millisecond: "<1s", 1500 * time.Millisecond: "2s", 59 * time.Second: "59s",
		61 * time.Second: "1m01s", 10*time.Minute + 5*time.Second: "10m05s",
		2*time.Hour + 3*time.Minute + 4*time.Second: "2h03m", maxRemainingShown: ">99h",
	} {
		if got := formatRemaining(remaining); got != want {
			t.Fatalf("formatRemaining(%s) = %q, want %q", remaining, got, want)
		}
	}
	if got := remainingTime(4<<20, 2<<20); got != 2*time.Second {
		t.Fatalf("remainingTime = %s", got)
	}
	if got := remainingTime(1<<30, 1e-9); got != maxRemainingShown {
		t.Fatalf("a near-zero speed overflowed the remaining time: %s", got)
	}
	if got := formatRate(3.5 * (1 << 20)); got != "3.5 MiB/s" {
		t.Fatalf("formatRate = %q", got)
	}
	if got := formatRate(512); got != "512 B/s" {
		t.Fatalf("formatRate = %q", got)
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
