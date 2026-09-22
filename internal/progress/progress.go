// Package progress prints one line per file as a folder conversion proceeds. It is deliberately
// plain (no cursor tricks) so the output is readable in a log or a redirected file.
package progress

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Printer serializes per-file lines from concurrent workers onto one writer.
type Printer struct {
	mu  sync.Mutex
	out io.Writer
}

// New returns a Printer writing to out.
func New(out io.Writer) *Printer { return &Printer{out: out} }

// Done reports a finished file and the outputs written for it.
func (p *Printer) Done(key string, outputs []string, elapsed string) {
	p.line("  ok   %s  ->  %s  (%s)", key, join(outputs), elapsed)
}

// Skip reports a file whose outputs already existed.
func (p *Printer) Skip(key string) { p.line("  skip %s  (outputs present)", key) }

// Fail reports a file that failed.
func (p *Printer) Fail(key string, err error) { p.line("  FAIL %s  (%v)", key, err) }

func (p *Printer) line(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.out, format+"\n", args...)
}

func join(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += " "
		}
		out += s
	}
	if out == "" {
		return "(no outputs)"
	}
	return out
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type Indicator struct {
	w       io.Writer
	enabled bool
	mu      sync.Mutex
	prefix  string
	detail  string
	percent float64
	start   time.Time
	stop    chan struct{}
	done    chan struct{}
}

func NewIndicator(w io.Writer, enabled bool) *Indicator {
	return &Indicator{w: w, enabled: enabled, percent: -1}
}

func (s *Indicator) Start(prefix string) {
	if !s.enabled {
		return
	}
	s.mu.Lock()
	s.prefix = prefix
	s.start = time.Now()
	s.mu.Unlock()
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.run()
}

func (s *Indicator) Set(percent float64, detail string) {
	if !s.enabled {
		return
	}
	s.mu.Lock()
	s.percent = percent
	s.detail = detail
	s.mu.Unlock()
}

func (s *Indicator) Stop() {
	if !s.enabled || s.stop == nil {
		return
	}
	close(s.stop)
	<-s.done
	s.stop = nil
	fmt.Fprint(s.w, "\r\033[K")
}

func (s *Indicator) run() {
	defer close(s.done)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.render(spinnerFrames[i%len(spinnerFrames)])
			i++
		}
	}
}

func (s *Indicator) render(frame string) {
	s.mu.Lock()
	prefix, detail, percent, start := s.prefix, s.detail, s.percent, s.start
	s.mu.Unlock()
	line := frame + " " + prefix
	if percent >= 0 {
		line += fmt.Sprintf("  %s %3.0f%%", bar(percent), percent)
	}
	if detail != "" {
		line += "  " + detail
	}
	line += fmt.Sprintf("  (%s)", time.Since(start).Round(time.Second))
	fmt.Fprintf(s.w, "\r\033[K%s", line)
}

func bar(percent float64) string {
	const width = 20
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := int(percent/100*width + 0.5)
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}
