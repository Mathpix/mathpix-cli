package batch

import (
	"bytes"
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }
func i(v int) *int           { return &v }

// TestStatusTextIsTheSharedLine locks the one status line both a local run and a watched cloud job
// render, so the two stay identical.
func TestStatusTextIsTheSharedLine(t *testing.T) {
	got := statusText(Event{Counts: &Counts{Total: 12, Completed: 5, Failed: 1, Pages: 42}, Throughput: f64(1840), ETASeconds: i(125)})
	for _, want := range []string{"6/12 files", "42 pages", "1 failed", "1840 pg/min", "eta 2m5s"} {
		if !strings.Contains(got, want) {
			t.Errorf("status line %q missing %q", got, want)
		}
	}
	if bar := progressBar(6, 12); bar != "["+strings.Repeat("=", 10)+strings.Repeat(" ", 10)+"]" { // 6/12 of width 20 = 10 filled
		t.Errorf("bar for 6/12: %q", bar)
	}
	if progressBar(0, 0) != "["+strings.Repeat(" ", 20)+"]" {
		t.Errorf("empty bar: %q", progressBar(0, 0))
	}
	if progressBar(9, 3) != "["+strings.Repeat("=", 20)+"]" {
		t.Error("bar must clamp when done exceeds total")
	}
}

// TestNonTerminalWriterSuppressesTheLiveLine: piped/captured output is a clean stream of per-file
// lines and the summary, with no status line or escape codes, so logs and pipelines stay readable.
func TestNonTerminalWriterSuppressesTheLiveLine(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf) // a bytes.Buffer is not a terminal
	p.Event(Event{Kind: "status", Counts: &Counts{Total: 3, Completed: 1}})
	p.Event(Event{Kind: "completed", Key: "a.pdf", Pages: 2, Outputs: []string{"a.mmd"}, Seconds: 1.2})
	p.Event(Event{Kind: "failed", Key: "b.pdf", ErrorID: "pdf_content_type", Message: "bad"})
	p.Event(Event{Kind: "summary", Counts: &Counts{Total: 2, Completed: 1, Failed: 1}, Message: "done"})
	out := buf.String()
	if strings.Contains(out, "\r") || strings.Contains(out, "\033") {
		t.Fatalf("non-terminal output must carry no carriage returns or escapes:\n%q", out)
	}
	for _, want := range []string{"✓ a.pdf", "a.mmd", "✗ b.pdf", "pdf_content_type", "2 files: 1 completed, 1 failed", "done"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
