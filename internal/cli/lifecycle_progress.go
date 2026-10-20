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
)

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
// elapsed time, and a byte progress bar while the binary downloads.
type updateProgressView struct {
	out   io.Writer
	style textStyler
	live  bool

	mu      sync.Mutex
	phase   usercli.UpdatePhase
	started time.Time
	done    int64
	total   int64
	frame   int
	quit    chan struct{}
	exited  chan struct{}
}

func newUpdateProgressView(out io.Writer) *updateProgressView {
	live := writerIsTerminal(out) && !strings.EqualFold(os.Getenv("TERM"), "dumb")
	return &updateProgressView{out: out, style: newTextStyler(out), live: live}
}

func (view *updateProgressView) observe(event usercli.UpdateEvent) {
	view.mu.Lock()
	defer view.mu.Unlock()
	switch event.Kind {
	case usercli.UpdateEventStart:
		view.phase, view.started, view.done, view.total = event.Phase, time.Now(), 0, 0
		view.startAnimation()
		view.drawRunning()
	case usercli.UpdateEventProgress:
		view.done, view.total = event.Done, event.Total
	case usercli.UpdateEventPass:
		detail := event.Detail
		if event.Total > 0 {
			detail = formatByteCount(event.Total) + " · " + detail
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
	} else if elapsed := time.Since(view.started); elapsed >= progressElapsedShown {
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
		if elapsed := time.Since(view.started); elapsed >= progressElapsedShown {
			line += "  " + view.style.token(formatElapsed(elapsed), ansiDim)
		}
		line = ansiClearLine + line
	}
	fmt.Fprintln(view.out, strings.TrimRight(line, " "))
	view.phase = ""
}

func progressBar(done, total int64) string {
	if done > total {
		done = total
	}
	filled := int(done * progressBarCells / total)
	return fmt.Sprintf("%s%s %3d%%", strings.Repeat("█", filled), strings.Repeat("░", progressBarCells-filled), done*100/total)
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
