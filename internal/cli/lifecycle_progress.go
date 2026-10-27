package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"reconc.dev/reconc/internal/usercli"
)

const (
	ansiDim              = "\x1b[2m"
	ansiClearLine        = "\r\x1b[2K"
	progressBarCells     = 24
	progressFrameDelay   = 80 * time.Millisecond
	progressElapsedShown = time.Second
	phaseLabelWidth      = 13
	// rateSampleInterval spaces the speed samples so that one burst of small
	// reads cannot swing the estimate; rateSmoothing weights the newest one.
	rateSampleInterval = 250 * time.Millisecond
	rateSmoothing      = 0.3
	// averageRateShown hides an average transfer rate for downloads too short
	// to give a meaningful one.
	averageRateShown = 100 * time.Millisecond
	// stallShown replaces the last speed and remaining time once no bytes
	// arrived for this long; maxRemainingShown caps the remaining-time estimate.
	stallShown        = 2 * time.Second
	maxRemainingShown = 99 * time.Hour
)

// partialBlocks fill the bar's leading cell in eighths for a smooth bar.
var partialBlocks = [...]string{"▏", "▎", "▍", "▌", "▋", "▊", "▉"}

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var updatePhaseLabels = map[usercli.UpdatePhase]string{
	usercli.UpdatePhaseInstallation: "Installation",
	usercli.UpdatePhaseRelease:      "Release",
	usercli.UpdatePhaseSkill:        "Skill",
	usercli.UpdatePhaseDownload:     "Download",
	usercli.UpdatePhaseProvenance:   "Provenance",
	usercli.UpdatePhaseSmokeTest:    "Smoke test",
	usercli.UpdatePhasePublish:      "Publish",
}

// updateProgressView prints one line per finished update phase. On a live
// terminal it also animates the running phase in place: a spinner with the
// elapsed time, and while the binary downloads a smooth byte progress bar with
// the transferred bytes, the smoothed transfer speed and the remaining time.
// A finished live download keeps its average speed.
type updateProgressView struct {
	out   io.Writer
	style textStyler
	live  bool
	now   func() time.Time

	mu       sync.Mutex
	phase    usercli.UpdatePhase
	started  time.Time
	done     int64
	total    int64
	rateAt   time.Time
	rateDone int64
	speed    float64 // smoothed bytes per second; 0 until the first sample
	lastRead time.Time
	frame    int
	quit     chan struct{}
	exited   chan struct{}
}

func newUpdateProgressView(out io.Writer) *updateProgressView {
	live := writerIsTerminal(out) && !strings.EqualFold(os.Getenv("TERM"), "dumb")
	return &updateProgressView{out: out, style: newTextStyler(out), live: live, now: time.Now}
}

func (view *updateProgressView) clock() time.Time {
	if view.now == nil {
		return time.Now()
	}
	return view.now()
}

func (view *updateProgressView) observe(event usercli.UpdateEvent) {
	view.mu.Lock()
	defer view.mu.Unlock()
	switch event.Kind {
	case usercli.UpdateEventStart:
		view.phase, view.started, view.done, view.total = event.Phase, view.clock(), 0, 0
		view.rateAt, view.rateDone, view.speed, view.lastRead = view.started, 0, 0, view.started
		view.startAnimation()
		view.drawRunning()
	case usercli.UpdateEventProgress:
		view.done, view.total, view.lastRead = event.Done, event.Total, view.clock()
		view.sampleRate(view.lastRead)
	case usercli.UpdateEventPass:
		detail := event.Detail
		if event.Total > 0 {
			size := formatByteCount(event.Total)
			if elapsed := view.clock().Sub(view.started); view.live && elapsed >= averageRateShown {
				size += " · " + formatRate(float64(event.Total)/elapsed.Seconds())
			}
			detail = size + " · " + detail
		}
		view.finish(event.Phase, true, detail)
	}
}

// close stops the animation. A phase that is still running ended with the
// refusal or failure that the lifecycle summary reports.
func (view *updateProgressView) close() {
	view.mu.Lock()
	quit, exited := view.quit, view.exited
	view.quit, view.exited = nil, nil
	view.mu.Unlock()
	if quit != nil {
		close(quit)
		<-exited
	}
	view.mu.Lock()
	defer view.mu.Unlock()
	if view.phase != "" {
		view.finish(view.phase, false, "")
	}
}

func (view *updateProgressView) startAnimation() {
	if !view.live || view.quit != nil {
		return
	}
	quit, exited := make(chan struct{}), make(chan struct{})
	view.quit, view.exited = quit, exited
	go func() {
		defer close(exited)
		ticker := time.NewTicker(progressFrameDelay)
		defer ticker.Stop()
		for {
			select {
			case <-quit:
				return
			case <-ticker.C:
				view.mu.Lock()
				view.frame++
				view.drawRunning()
				view.mu.Unlock()
			}
		}
	}()
}

func (view *updateProgressView) drawRunning() {
	if !view.live || view.phase == "" {
		return
	}
	spinner := view.style.token(spinnerFrames[view.frame%len(spinnerFrames)], ansiBold+ansiCyan)
	status := ""
	if view.total > 0 {
		status = progressBar(view.done, view.total) + "  " +
			formatByteCount(view.done) + " / " + formatByteCount(view.total)
		if view.clock().Sub(view.lastRead) >= stallShown {
			status += view.style.token("  stalled", ansiDim)
		} else if view.speed > 0 {
			remaining := remainingTime(view.total-view.done, view.speed)
			status += view.style.token("  "+formatRate(view.speed)+" · "+formatRemaining(remaining)+" left", ansiDim)
		}
	} else if elapsed := view.clock().Sub(view.started); elapsed >= progressElapsedShown {
		status = view.style.token(formatElapsed(elapsed), ansiDim)
	}
	fmt.Fprintf(view.out, "%s  %s %-*s %s", ansiClearLine, spinner, phaseLabelWidth, updatePhaseLabels[view.phase], status)
}

func (view *updateProgressView) finish(phase usercli.UpdatePhase, passed bool, detail string) {
	mark := view.style.token("✓", ansiBold+ansiGreen)
	if !passed {
		mark = view.style.token("✗", ansiBold+ansiRed)
	}
	line := fmt.Sprintf("  %s %-*s %s", mark, phaseLabelWidth, updatePhaseLabels[phase], detail)
	if view.live {
		if elapsed := view.clock().Sub(view.started); elapsed >= progressElapsedShown {
			line += "  " + view.style.token(formatElapsed(elapsed), ansiDim)
		}
		line = ansiClearLine + line
	}
	fmt.Fprintln(view.out, strings.TrimRight(line, " "))
	view.phase = ""
}

// sampleRate folds the bytes read since the previous sample into the smoothed
// speed once a full sample interval has passed.
func (view *updateProgressView) sampleRate(now time.Time) {
	elapsed := now.Sub(view.rateAt)
	if elapsed < rateSampleInterval {
		return
	}
	current := float64(view.done-view.rateDone) / elapsed.Seconds()
	if view.speed == 0 {
		view.speed = current
	} else {
		view.speed = rateSmoothing*current + (1-rateSmoothing)*view.speed
	}
	view.rateAt, view.rateDone = now, view.done
}

// progressBar renders done/total as full cells, one leading cell filled in
// eighths and the percentage rounded down.
func progressBar(done, total int64) string {
	if done > total {
		done = total
	}
	eighths := int(done * progressBarCells * 8 / total)
	full, part := eighths/8, eighths%8
	bar := strings.Repeat("█", full)
	if part > 0 {
		bar += partialBlocks[part-1]
		full++
	}
	return fmt.Sprintf("%s%s %3d%%", bar, strings.Repeat("░", progressBarCells-full), done*100/total)
}

func formatRate(bytesPerSecond float64) string {
	return formatByteCount(int64(bytesPerSecond)) + "/s"
}

// remainingTime estimates the time left at the smoothed speed, capped at
// maxRemainingShown so that a near-zero speed cannot overflow the duration.
func remainingTime(left int64, bytesPerSecond float64) time.Duration {
	seconds := float64(max(left, 0)) / bytesPerSecond
	if seconds >= maxRemainingShown.Seconds() {
		return maxRemainingShown
	}
	return time.Duration(seconds * float64(time.Second))
}

// formatRemaining rounds the remaining time to whole seconds, or to minutes
// from one hour on.
func formatRemaining(remaining time.Duration) string {
	seconds := int64(remaining.Round(time.Second) / time.Second)
	switch {
	case remaining >= maxRemainingShown:
		return ">99h"
	case seconds < 1:
		return "<1s"
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
	default:
		return fmt.Sprintf("%dh%02dm", seconds/3600, seconds%3600/60)
	}
}

func formatByteCount(count int64) string {
	const unit = 1 << 10
	if count < unit {
		return fmt.Sprintf("%d B", count)
	}
	value := float64(count) / unit
	for _, suffix := range []string{"KiB", "MiB"} {
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
		value /= unit
	}
	return fmt.Sprintf("%.1f GiB", value)
}

func formatElapsed(elapsed time.Duration) string {
	return fmt.Sprintf("%.1fs", elapsed.Seconds())
}
