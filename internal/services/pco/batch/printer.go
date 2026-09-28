// Package batch is the client-side batch: a local folder (or a single file) converted through
// POST /v3/pdf from the workstation, N documents at a time, with a report and manifest in the
// same schema the deployment's own batch API writes, so a run can be resumed and so a folder
// moved from disk to a bucket looks the same. The printer here renders one human line per event
// plus a live status line — a spinner, a bar and a ticking elapsed timer, the same animation a
// single-file convert shows — and it is the renderer a cloud job's watch drives too, so local and
// server-side runs show progress identically. The machine-readable record is report.jsonl.
package batch

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Event is one thing that happened to one file, the run's summary, or a live status refresh.
type Event struct {
	Kind       string // submitted, completed, failed, skipped, detached, resumed, status, summary, note
	Key        string
	DocumentID string
	Pages      int
	ErrorID    string
	Message    string
	Outputs    []string
	Seconds    float64
	Counts     *Counts
	Throughput *float64 // pages per minute, when the source can report it
	ETASeconds *int     // seconds remaining, when known
}

// Counts is the running tally, shown live and in the summary.
type Counts struct {
	Total      int
	Completed  int
	Failed     int
	Skipped    int
	Processing int
	Pages      int
}

// Printer receives every event.
type Printer interface {
	Event(e Event)
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// New returns the terminal renderer. On a terminal it animates a live status line (spinner, bar and
// elapsed time); piped or captured output stays a clean stream of per-file lines and the summary,
// with no status line, so logs and pipelines are readable.
func New(w io.Writer) Printer {
	p := &humanPrinter{w: w, live: isTerminalWriter(w), start: time.Now()}
	if p.live {
		p.stop = make(chan struct{})
		go p.animate()
	}
	return p
}

type humanPrinter struct {
	mu          sync.Mutex
	w           io.Writer
	live        bool
	statusShown bool
	last        Event // the latest status event, redrawn under scrolling lines and on each tick
	hasStatus   bool
	start       time.Time
	spinner     int
	finished    bool
	stop        chan struct{}
}

// animate advances the spinner and redraws the pinned status line a few times a second, so the
// elapsed timer keeps moving between events (a long document does not freeze the line).
func (p *humanPrinter) animate() {
	stop := p.stop // captured once; finish closes it, and reading it here unlocked would race
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.mu.Lock()
			if p.hasStatus && !p.finished {
				p.spinner++
				p.draw()
			}
			p.mu.Unlock()
		}
	}
}

func (p *humanPrinter) Event(e Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch e.Kind {
	case "status":
		if e.Counts == nil {
			return
		}
		p.last, p.hasStatus = e, true
		if p.live {
			p.draw()
		}
	case "submitted":
		p.line(fmt.Sprintf("  … %s  submitted (%s)", e.Key, e.DocumentID))
	case "completed":
		msg := fmt.Sprintf("  ✓ %s  %d page%s", e.Key, e.Pages, plural(e.Pages))
		if len(e.Outputs) > 0 {
			msg += "  " + strings.Join(e.Outputs, " ")
		}
		if e.Seconds > 0 {
			msg += fmt.Sprintf("  (%.1fs)", e.Seconds)
		}
		p.line(msg)
	case "failed":
		p.line(fmt.Sprintf("  ✗ %s  %s: %s", e.Key, e.ErrorID, e.Message))
	case "skipped":
		p.line(fmt.Sprintf("  = %s  already done, skipped", e.Key))
	case "detached":
		p.line(fmt.Sprintf("  ↗ %s  processing on the deployment (%s)", e.Key, e.DocumentID))
	case "resumed":
		p.line(fmt.Sprintf("  ↻ %s  resuming (%s)", e.Key, e.DocumentID))
	case "summary":
		p.finish()
		if e.Counts != nil {
			fmt.Fprintf(p.w, "%d file%s: %d completed, %d failed, %d skipped, %d still processing\n",
				e.Counts.Total, plural(e.Counts.Total), e.Counts.Completed, e.Counts.Failed, e.Counts.Skipped, e.Counts.Processing)
		}
		if e.Message != "" {
			fmt.Fprintln(p.w, e.Message)
		}
	default:
		p.line(e.Message)
	}
}

// line prints a scrolling message and keeps the status line pinned to the bottom: on a terminal it
// erases the status line, prints the message, then redraws it beneath, so the animated bar stays
// visible from the first line to the last.
func (p *humanPrinter) line(msg string) {
	p.clearStatus()
	fmt.Fprintln(p.w, msg)
	if p.live && p.hasStatus {
		p.draw()
	}
}

// draw writes the current status line in place. Assumes the lock is held and the writer is live.
func (p *humanPrinter) draw() {
	frame := spinnerFrames[p.spinner%len(spinnerFrames)]
	line := frame + " " + statusText(p.last) + fmt.Sprintf("  (%s)", time.Since(p.start).Round(time.Second))
	fmt.Fprint(p.w, "\r\033[K"+line)
	p.statusShown = true
}

func (p *humanPrinter) clearStatus() {
	if p.live && p.statusShown {
		fmt.Fprint(p.w, "\r\033[K")
		p.statusShown = false
	}
}

// finish stops the animation and clears the status line, before the summary is printed.
func (p *humanPrinter) finish() {
	if !p.finished {
		p.finished = true
		if p.stop != nil {
			close(p.stop)
		}
	}
	p.clearStatus()
}

// statusText is the data portion of the live line, identical for a local run and a watched cloud
// job. draw() prepends the spinner and appends the elapsed time.
func statusText(e Event) string {
	c := e.Counts
	done := c.Completed + c.Failed + c.Skipped
	s := fmt.Sprintf("%s %d/%d files", progressBar(done, c.Total), done, c.Total)
	if c.Pages > 0 {
		s += fmt.Sprintf(" · %d pages", c.Pages)
	}
	if c.Failed > 0 {
		s += fmt.Sprintf(" · %d failed", c.Failed)
	}
	if e.Throughput != nil && *e.Throughput > 0 {
		s += fmt.Sprintf(" · %.0f pg/min", *e.Throughput)
	}
	if e.ETASeconds != nil && *e.ETASeconds > 0 {
		s += fmt.Sprintf(" · eta %s", (time.Duration(*e.ETASeconds) * time.Second).Round(time.Second))
	}
	return s
}

func progressBar(done, total int) string {
	const width = 20
	if total <= 0 {
		return "[" + strings.Repeat(" ", width) + "]"
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("=", filled) + strings.Repeat(" ", width-filled) + "]"
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
