// Package progress prints one line per file as a folder conversion proceeds. It is deliberately
// plain (no cursor tricks) so the output is readable in a log or a redirected file.
package progress

import (
	"fmt"
	"io"
	"sync"
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
