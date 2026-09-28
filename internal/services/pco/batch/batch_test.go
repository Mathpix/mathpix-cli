package batch_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mathpix/mathpix-cli/internal/pcoapi"
	"github.com/mathpix/mathpix-cli/internal/services/pco/batch"
	"github.com/mathpix/mathpix-cli/internal/services/pco/fakepco"
)

type recorder struct {
	mu     sync.Mutex
	events []batch.Event
}

func (r *recorder) Event(e batch.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) kinds() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := map[string]int{}
	for _, e := range r.events {
		counts[e.Kind]++
	}
	return counts
}

// TestMain keeps every test's run index out of the developer's real ~/.mpx, since the runner
// registers each run there.
func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "pco-batch-test")
	os.Setenv("MPX_CONFIG_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func newRunner(t *testing.T, fake *fakepco.Deployment, opts batch.Options) (*batch.Runner, *recorder) {
	client, err := pcoapi.New(pcoapi.Options{Endpoint: fake.URL()})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	return &batch.Runner{Client: client, Options: opts, Progress: rec,
		Sleep: func(context.Context, time.Duration) error { return nil }}, rec
}

func writeFolder(t *testing.T) string {
	dir := t.TempDir()
	for _, name := range []string{"a.pdf", "sub/b.pdf", "bad.pdf", "notes.txt", "sub/.hidden.pdf"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte("%PDF-1.4\n/Type /Page\n/Type /Page\n"), 0o644)
	}
	return dir
}

// readReport loads the report and checks it is compacted: one line per key.
func readReport(t *testing.T, dir string) map[string]batch.Record {
	entries := map[string]batch.Record{}
	matches, _ := filepath.Glob(filepath.Join(dir, "_mathpix", "*", "report.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("expected one report, found %v", matches)
	}
	file, _ := os.Open(matches[0])
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() {
		lines++
		var record batch.Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if _, seen := entries[record.Key]; seen {
			t.Fatalf("report has two lines for %s after the run; it must be compacted to one per file", record.Key)
		}
		entries[record.Key] = record
	}
	if lines != len(entries) {
		t.Fatalf("%d lines for %d keys", lines, len(entries))
	}
	return entries
}

func TestLocalFolderRunWritesOutputsBesideInputsAndAReport(t *testing.T) {
	fake := fakepco.New()
	defer fake.Close()
	dir := writeFolder(t)
	runner, rec := newRunner(t, fake, batch.Options{Inputs: []string{dir}, Formats: []string{"docx"}, Concurrency: 2})
	summary, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Counts.Total != 3 || summary.Counts.Completed != 2 || summary.Counts.Failed != 1 {
		t.Fatalf("counts: %+v", summary.Counts)
	}
	for _, out := range []string{"a.mmd", "a.lines.json", "a.docx", "sub/b.mmd", "sub/b.docx"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(out))); err != nil {
			t.Errorf("missing output %s", out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.mmd")); err == nil {
		t.Error("a failed document must leave no .mmd")
	}
	entries := readReport(t, dir)
	if entries["bad.pdf"].Status != "failed" || *entries["bad.pdf"].ErrorID != "pdf_content_type" {
		t.Errorf("bad.pdf entry: %+v", entries["bad.pdf"])
	}
	if entries["sub/b.pdf"].Status != "completed" || entries["sub/b.pdf"].Pages != 3 || entries["sub/b.pdf"].Outputs["docx"] == "" {
		t.Errorf("sub/b.pdf entry: %+v", entries["sub/b.pdf"])
	}
	if kinds := rec.kinds(); kinds["completed"] != 2 || kinds["failed"] != 1 || kinds["submitted"] != 3 {
		t.Errorf("events: %v", kinds)
	}
	// The report line is the deployment's own file-record shape.
	var serverShape pcoapi.FileRecord
	line, _ := json.Marshal(entries["a.pdf"])
	if err := json.Unmarshal(line, &serverShape); err != nil || serverShape.Key != "a.pdf" {
		t.Fatalf("report line does not decode as the server record: %v", err)
	}
}

func TestDetachThenResumeFetchesWithoutResubmitting(t *testing.T) {
	fake := fakepco.New()
	defer fake.Close()
	dir := writeFolder(t)
	opts := batch.Options{Inputs: []string{dir}, Exclude: []string{"bad.pdf"}, Detach: true, Concurrency: 1}
	runner, rec := newRunner(t, fake, opts)
	summary, err := runner.Run(context.Background())
	if err != nil || summary.Counts.Processing != 2 || fake.Submits != 2 {
		t.Fatalf("detach: err=%v counts=%+v submits=%d", err, summary.Counts, fake.Submits)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.mmd")); err == nil {
		t.Fatal("detached run must not download")
	}
	if kinds := rec.kinds(); kinds["detached"] != 2 {
		t.Fatalf("events: %v", kinds)
	}
	// Resume by id, with none of the original flags: the exclude (and formats, out) come from the record,
	// so bad.pdf stays excluded and nothing is resubmitted.
	runner, rec = newRunner(t, fake, batch.Options{Inputs: []string{dir}, Resume: summary.RunID, Concurrency: 1})
	summary, err = runner.Run(context.Background())
	if err != nil || summary.Counts.Completed != 2 || summary.Counts.Total != 2 || fake.Submits != 2 {
		t.Fatalf("resume by id: err=%v counts=%+v submits=%d", err, summary.Counts, fake.Submits)
	}
	if kinds := rec.kinds(); kinds["resumed"] != 2 {
		t.Fatalf("resume events: %v", kinds)
	}
	// A bare --resume of the same command skips everything: outputs exist and the report says completed.
	opts.Detach, opts.Resume = false, batch.ResumeSameCommand
	runner, rec = newRunner(t, fake, opts)
	summary, _ = runner.Run(context.Background())
	if summary.Counts.Skipped != 2 || rec.kinds()["skipped"] != 2 || fake.Submits != 2 {
		t.Fatalf("second resume should skip: %+v submits=%d", summary.Counts, fake.Submits)
	}
	// --resume with a changed command has no record and must refuse rather than start over.
	runner, _ = newRunner(t, fake, batch.Options{Inputs: []string{dir}, Formats: []string{"md"}, Resume: batch.ResumeSameCommand})
	if _, err := runner.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "records here") {
		t.Fatalf("resume without a record must refuse and list records, got %v", err)
	}
}

func TestOutDirMirrorsKeysAndTransientSubmitIsRetried(t *testing.T) {
	fake := fakepco.New()
	defer fake.Close()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "in", "deep"), 0o755)
	os.WriteFile(filepath.Join(dir, "in", "deep", "flaky.pdf"), []byte("%PDF"), 0o644)
	out := filepath.Join(dir, "out")
	runner, _ := newRunner(t, fake, batch.Options{Inputs: []string{filepath.Join(dir, "in")}, OutDir: out, Concurrency: 1})
	summary, err := runner.Run(context.Background())
	if err != nil || summary.Counts.Completed != 1 {
		t.Fatalf("err=%v counts=%+v", err, summary.Counts)
	}
	if _, err := os.Stat(filepath.Join(out, "deep", "flaky.mmd")); err != nil {
		t.Fatal("output should mirror the key under --out")
	}
	if fake.Submits != 2 {
		t.Fatalf("the 503 should have been retried once: submits=%d", fake.Submits)
	}
	if !strings.HasPrefix(summary.RunDir, out) {
		t.Fatalf("run record should live under --out, got %s", summary.RunDir)
	}
}

func TestInterruptPausesSubmissionsThenStopsOrContinues(t *testing.T) {
	for _, stop := range []bool{true, false} {
		fake := fakepco.New()
		dir := writeFolder(t)
		signals := make(chan os.Signal, 1)
		asked := make(chan batch.Snapshot, 1)
		runner, rec := newRunner(t, fake, batch.Options{Inputs: []string{dir}, Exclude: []string{"bad.pdf"}, Concurrency: 1})
		runner.Interrupt = signals
		runner.ConfirmStop = func(snap batch.Snapshot) bool { asked <- snap; return stop }
		// Interrupt as soon as the first file has been submitted: the second must not go out
		// while the question is open.
		runner.Progress = interruptOnFirstSubmit{recorder: rec, signals: signals}
		summary, err := runner.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		snap := <-asked
		if snap.Submitted != 1 || snap.Total != 2 {
			t.Fatalf("stop=%v: the question should come after exactly one submission, got %+v", stop, snap)
		}
		if stop {
			if !summary.Interrupted || summary.Submitted != 1 || fake.Submits != 1 || summary.Counts.Processing != 1 {
				t.Fatalf("stop: %+v submits=%d", summary, fake.Submits)
			}
			if entries := readReport(t, dir); entries["a.pdf"].Status != "processing" || entries["a.pdf"].DocumentID == "" {
				t.Fatalf("the submitted document must stay in the report as processing for --resume: %+v", entries["a.pdf"])
			}
		} else if summary.Interrupted || summary.Counts.Completed != 2 || fake.Submits != 2 {
			t.Fatalf("continue: %+v submits=%d", summary, fake.Submits)
		}
		fake.Close()
	}
}

type interruptOnFirstSubmit struct {
	recorder *recorder
	signals  chan os.Signal
}

func (p interruptOnFirstSubmit) Event(e batch.Event) {
	p.recorder.Event(e)
	if e.Kind == "submitted" && len(p.signals) == 0 && p.recorder.kinds()["submitted"] == 1 {
		p.signals <- os.Interrupt
	}
}
