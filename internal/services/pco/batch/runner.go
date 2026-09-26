package batch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mathpix/mathpix-cli/internal/pcoapi"
)

// Runner converts the items of one Options against one deployment.
type Runner struct {
	Client   *pcoapi.Client
	Options  Options
	Progress Printer
	// Interrupt delivers the user's Ctrl-C. On each one the runner stops handing out new files
	// at once, keeps the report as it is, and asks ConfirmStop; true ends the run (documents
	// already submitted keep processing on the deployment and are in the report for --resume),
	// false carries on. A nil ConfirmStop means stop without asking.
	Interrupt   <-chan os.Signal
	ConfirmStop func(Snapshot) bool
	// Sleep is replaceable so tests run without waiting.
	Sleep       func(context.Context, time.Duration) error
	PollInitial time.Duration
	PollMax     time.Duration
	MaxAttempts int
	paused      atomic.Bool
	stopped     atomic.Bool
	submitted   atomic.Int64
}

// Snapshot is where a run stands when the user is asked whether to stop it.
type Snapshot struct {
	Total     int
	Submitted int
	Completed int
	Failed    int
	Skipped   int
	RunDir    string
}

// Summary is what Run returns for the caller to print and to pick an exit code from.
type Summary struct {
	Counts      Counts
	Submitted   int
	RunID       string
	ReportPath  string
	RunDir      string
	Interrupted bool
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *Runner) defaults() {
	if r.Sleep == nil {
		r.Sleep = defaultSleep
	}
	if r.PollInitial == 0 {
		r.PollInitial = time.Second
	}
	if r.PollMax == 0 {
		r.PollMax = 10 * time.Second
	}
	if r.MaxAttempts == 0 {
		r.MaxAttempts = 8
	}
}

// Run discovers, submits, polls and downloads. A cancelled ctx (Ctrl-C) stops new submissions,
// leaves submitted documents processing on the deployment, and returns a Summary with
// Interrupted set; the report already holds their ids for a later --resume.
func (r *Runner) Run(ctx context.Context) (*Summary, error) {
	r.defaults()
	if r.Options.Overwrite && r.Options.Resume != "" {
		return nil, fmt.Errorf("--overwrite reprocesses everything and --resume continues a run; pick one")
	}
	if r.Options.Resume != "" && r.Options.Resume != ResumeSameCommand {
		if err := r.adoptRecord(r.Options.Resume); err != nil {
			return nil, err
		}
	}
	items, root, err := Discover(r.Options)
	if err != nil {
		return nil, err
	}
	absInputs := make([]string, 0, len(r.Options.Inputs))
	for _, input := range r.Options.Inputs {
		abs, _ := filepath.Abs(input)
		absInputs = append(absInputs, abs)
	}
	runID := RunID(r.Options, absInputs)
	if r.Options.Resume != "" && r.Options.Resume != ResumeSameCommand {
		runID = r.Options.Resume
	}
	runDir := filepath.Join(root, RunDirName, runID)
	if r.Options.DryRun {
		return r.dryRun(items, runDir), nil
	}
	manifest := LoadManifest(runDir)
	if manifest == nil && r.Options.Resume != "" {
		return nil, fmt.Errorf("no previous run to resume at %s%s", runDir, r.listRecords(root))
	}
	report, err := OpenReport(runDir)
	if err != nil {
		return nil, err
	}
	defer report.Close()
	rememberRun(runID, runDir)
	if manifest == nil {
		manifest = &Manifest{RunID: runID, Endpoint: r.Client.BaseURL, Inputs: absInputs, OutDir: r.Options.OutDir,
			Formats: r.Options.Formats, Include: r.Options.Include, Exclude: r.Options.Exclude, RequestOptions: r.Options.RequestOptions}
	} else if report.Len() > 0 && r.Options.Resume == "" {
		r.Progress.Event(Event{Kind: "note", Message: fmt.Sprintf("found the record of this command, run %s: finished files are skipped, submitted ones are picked up", runID)})
	}
	concurrency := r.Options.Concurrency
	if concurrency <= 0 {
		concurrency = r.defaultConcurrency(ctx)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	startedAt := time.Now()
	summary := &Summary{RunID: runID, ReportPath: report.Path(), RunDir: runDir}
	summary.Counts.Total = len(items)
	var mu sync.Mutex
	tally := func(kind string, pages int) {
		mu.Lock()
		defer mu.Unlock()
		switch kind {
		case "completed":
			summary.Counts.Completed++
			summary.Counts.Pages += pages
		case "failed":
			summary.Counts.Failed++
		case "skipped":
			summary.Counts.Skipped++
		case "processing":
			summary.Counts.Processing++
		}
	}
	snapshot := func() Snapshot {
		mu.Lock()
		defer mu.Unlock()
		return Snapshot{Total: len(items), Submitted: int(r.submitted.Load()), Completed: summary.Counts.Completed,
			Failed: summary.Counts.Failed, Skipped: summary.Counts.Skipped, RunDir: runDir}
	}
	// emitStatus refreshes the shared live line: the same fields (files, pages, rate, eta) a cloud
	// job's watch emits, so the two look identical. Throughput and eta are computed from progress.
	emitStatus := func() {
		mu.Lock()
		counts := summary.Counts
		mu.Unlock()
		done := counts.Completed + counts.Failed + counts.Skipped
		elapsed := time.Since(startedAt).Seconds()
		var throughput *float64
		var eta *int
		if elapsed > 0 && counts.Pages > 0 {
			rate := float64(counts.Pages) / elapsed * 60
			throughput = &rate
		}
		if elapsed > 0 && done > 0 && done < counts.Total {
			seconds := int(float64(counts.Total-done) / (float64(done) / elapsed))
			eta = &seconds
		}
		r.Progress.Event(Event{Kind: "status", Counts: &counts, Throughput: throughput, ETASeconds: eta})
	}
	go r.handleInterrupts(ctx, cancel, snapshot)
	queue := make(chan Item)
	var workers sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range queue {
				kind, pages := r.process(ctx, item, report)
				tally(kind, pages)
				emitStatus()
			}
		}()
	}
	emitStatus() // show the bar at 0/total before the first file finishes
dispatch:
	for _, item := range items {
		if !r.waitWhilePaused(ctx) {
			break dispatch // stopped: what was not handed out was never submitted
		}
		select {
		case <-ctx.Done():
			break dispatch
		case queue <- item:
		}
	}
	close(queue)
	workers.Wait()
	summary.Interrupted = r.stopped.Load()
	summary.Submitted = int(r.submitted.Load())
	if err := report.Compact(); err != nil {
		return summary, err
	}
	if summary.Counts.Processing == 0 && !summary.Interrupted {
		forgetRun(runID) // nothing left to fetch; the record itself stays beside the outputs
	}
	manifest.Counts = map[string]int{"total": summary.Counts.Total, "completed": summary.Counts.Completed,
		"failed": summary.Counts.Failed, "skipped": summary.Counts.Skipped, "processing": summary.Counts.Processing}
	if err := report.WriteManifest(manifest); err != nil {
		return summary, err
	}
	return summary, nil
}

// adoptRecord finds the record named by --resume RUN_ID and takes everything the command had
// from its manifest: inputs, OutDir, Formats, Include, Exclude, RequestOptions. The record is
// found through the per-user runs index first, so an id alone is enough on the machine that ran
// it; otherwise beside the given inputs and under --out.
func (r *Runner) adoptRecord(runID string) error {
	candidates := []string{}
	if remembered := recordDirOf(runID); remembered != "" {
		candidates = append(candidates, remembered)
	}
	roots := []string{}
	if r.Options.OutDir != "" {
		roots = append(roots, r.Options.OutDir)
	}
	for _, input := range r.Options.Inputs {
		abs, err := filepath.Abs(input)
		if err != nil {
			return err
		}
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			abs = filepath.Dir(abs)
		}
		roots = append(roots, abs)
	}
	for _, root := range roots {
		candidates = append(candidates, filepath.Join(root, RunDirName, runID))
	}
	for _, dir := range candidates {
		manifest := LoadManifest(dir)
		if manifest == nil {
			continue
		}
		if len(r.Options.Inputs) == 0 {
			r.Options.Inputs = manifest.Inputs
		}
		r.Options.OutDir, r.Options.Formats, r.Options.RequestOptions = manifest.OutDir, manifest.Formats, manifest.RequestOptions
		r.Options.Include, r.Options.Exclude = manifest.Include, manifest.Exclude
		return nil
	}
	where := "this machine's run index"
	if len(roots) > 0 {
		where += " or " + strings.Join(roots, ", ")
	}
	hint := ""
	if len(roots) > 0 {
		hint = r.listRecords(roots[len(roots)-1])
	}
	return fmt.Errorf("no run record %s in %s%s. For a run made elsewhere, give its input path and, if it used --out, the same --out", runID, where, hint)
}

// listRecords names the run records under a root, for the error a failed --resume prints.
func (r *Runner) listRecords(root string) string {
	entries, err := os.ReadDir(filepath.Join(root, RunDirName))
	if err != nil || len(entries) == 0 {
		return ""
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	return fmt.Sprintf("; records here: %s (mpx pco convert PATH --resume RUN_ID)", strings.Join(ids, ", "))
}

// handleInterrupts pauses dispatch on Ctrl-C, asks, and either cancels the run or resumes.
// A stop cancels ctx: workers polling or downloading return at once and their documents stay
// `processing` in the report, which is exactly what --resume picks up.
func (r *Runner) handleInterrupts(ctx context.Context, cancel context.CancelFunc, snapshot func() Snapshot) {
	if r.Interrupt == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-r.Interrupt:
			if !ok {
				return
			}
			r.paused.Store(true)
			stop := true
			if r.ConfirmStop != nil {
				stop = r.ConfirmStop(snapshot())
			}
			if stop {
				r.stopped.Store(true)
				cancel()
				return
			}
			r.paused.Store(false)
		}
	}
}

// waitWhilePaused blocks the dispatcher while a stop is being decided. False when the run ended.
func (r *Runner) waitWhilePaused(ctx context.Context) bool {
	for r.paused.Load() {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return ctx.Err() == nil
}

func (r *Runner) defaultConcurrency(ctx context.Context) int {
	status, err := r.Client.Status(ctx)
	if err != nil || status.Workers["gpu_expected"] <= 0 {
		return 4
	}
	concurrency := status.Workers["gpu_expected"] * 2
	if concurrency > 16 {
		concurrency = 16
	}
	return concurrency
}

// process runs one item to its end state and returns the kind it was tallied as, plus the page
// count for a completed document (0 otherwise) so the caller can total pages for the live line.
func (r *Runner) process(ctx context.Context, item Item, report *Report) (string, int) {
	started := time.Now()
	record := report.Get(item.Key)
	switch {
	case record != nil && record.Status == StatusCompleted && r.outputsPresent(record):
		r.Progress.Event(Event{Kind: "skipped", Key: item.Key, DocumentID: record.DocumentID})
		return "skipped", 0
	case record != nil && record.DocumentID != "" && record.Status == StatusProcessing:
		r.Progress.Event(Event{Kind: "resumed", Key: item.Key, DocumentID: record.DocumentID})
	default:
		record = r.submit(ctx, item, report)
		if record == nil {
			return "interrupted", 0 // nothing reached the deployment; not tallied, the next run submits it
		}
		if record.Status == StatusFailed {
			return "failed", 0
		}
	}
	if r.Options.Detach {
		r.Progress.Event(Event{Kind: "detached", Key: item.Key, DocumentID: record.DocumentID})
		return "processing", 0
	}
	status, err := r.waitForDocument(ctx, record.DocumentID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "processing", 0
		}
		return r.fail(item, record, report, "deployment_unreachable", err.Error()), 0
	}
	if status.Status == pcoapi.DocumentError {
		errorID, message := "document_error", "the deployment reported an error"
		if status.ErrorInfo != nil {
			errorID, message = status.ErrorInfo.ID, status.ErrorInfo.Message
		}
		record.Pages = status.NumPages
		return r.fail(item, record, report, errorID, message), 0
	}
	record.Pages = status.NumPages
	record.PagesFailed = status.NumPages - status.NumPagesCompleted
	outputs, downloadErr := r.download(ctx, item, record.DocumentID)
	record.Outputs = outputs
	if downloadErr != nil {
		if errors.Is(downloadErr, context.Canceled) {
			return "processing", 0
		}
		var apiErr *pcoapi.APIError
		errorID := "output_download_failed"
		if errors.As(downloadErr, &apiErr) && apiErr.ID != "" {
			errorID = apiErr.ID
		}
		return r.fail(item, record, report, errorID, downloadErr.Error()), 0
	}
	record.Status = StatusCompleted
	record.FinishedAt = nowString()
	_ = report.Append(record)
	names := make([]string, 0, len(outputs))
	for _, ext := range r.outputExtensions() {
		if out, ok := outputs[ext]; ok {
			names = append(names, filepath.Base(out))
		}
	}
	r.Progress.Event(Event{Kind: "completed", Key: item.Key, DocumentID: record.DocumentID, Pages: record.Pages,
		Outputs: names, Seconds: time.Since(started).Seconds()})
	return "completed", record.Pages
}

func (r *Runner) fail(item Item, record *Record, report *Report, errorID, message string) string {
	record.Status = StatusFailed
	record.ErrorID, record.ErrorMessage = &errorID, &message
	record.FinishedAt = nowString()
	_ = report.Append(record)
	r.Progress.Event(Event{Kind: "failed", Key: item.Key, DocumentID: record.DocumentID, ErrorID: errorID, Message: message})
	return "failed"
}

// submit uploads one file with retries on transient failures and records the document id.
// Returns nil when the context was cancelled before anything reached the deployment.
func (r *Runner) submit(ctx context.Context, item Item, report *Report) *Record {
	record := &Record{Key: item.Key, Status: StatusProcessing, Outputs: map[string]string{}, StartedAt: nowString()}
	options := map[string]any{}
	for key, value := range r.Options.RequestOptions {
		options[key] = value
	}
	if len(r.Options.Formats) > 0 {
		options["conversion_formats"] = pcoapi.ConversionFormats(r.Options.Formats)
	}
	var lastErr error
	for attempt := 1; attempt <= r.MaxAttempts; attempt++ {
		record.Attempts = attempt
		file, err := os.Open(item.Path)
		if err != nil {
			return r.failedRecord(item, record, report, "file_unreadable", err.Error())
		}
		submitted, err := r.Client.SubmitFile(ctx, file, filepath.Base(item.Path), options)
		file.Close()
		if err == nil {
			record.DocumentID = submitted.PDFID
			_ = report.Append(record)
			r.submitted.Add(1)
			r.Progress.Event(Event{Kind: "submitted", Key: item.Key, DocumentID: record.DocumentID})
			return record
		}
		if errors.Is(err, context.Canceled) {
			return nil
		}
		lastErr = err
		if !pcoapi.Transient(err) {
			break
		}
		if r.Sleep(ctx, backoff(attempt, r.PollInitial, r.PollMax)) != nil {
			return nil
		}
	}
	var apiErr *pcoapi.APIError
	errorID := "submit_failed"
	if errors.As(lastErr, &apiErr) && apiErr.ID != "" {
		errorID = apiErr.ID
	}
	return r.failedRecord(item, record, report, errorID, lastErr.Error())
}

func (r *Runner) failedRecord(item Item, record *Record, report *Report, errorID, message string) *Record {
	r.fail(item, record, report, errorID, message)
	return record
}

// waitForDocument polls until the document is terminal, tolerating transient errors.
func (r *Runner) waitForDocument(ctx context.Context, documentID string) (*pcoapi.DocumentStatus, error) {
	failures := 0
	for attempt := 0; ; attempt++ {
		status, err := r.Client.DocumentStatus(ctx, documentID)
		if err != nil {
			if errors.Is(err, context.Canceled) || !pcoapi.Transient(err) {
				return nil, err
			}
			failures++
			if failures >= r.MaxAttempts {
				return nil, fmt.Errorf("status of %s unavailable after %d attempts: %w", documentID, failures, err)
			}
		} else {
			failures = 0
			if status.Terminal() {
				return status, nil
			}
		}
		if err := r.Sleep(ctx, backoff(attempt, r.PollInitial, r.PollMax)); err != nil {
			return nil, err
		}
	}
}

func (r *Runner) outputExtensions() []string {
	extensions := append([]string(nil), pcoapi.DirectExtensions...)
	for _, format := range r.Options.Formats {
		extensions = append(extensions, pcoapi.ExtensionByFormat[format])
	}
	return extensions
}

// download fetches every output beside the input (or under --out), waiting on 202 for formats
// that are still converting. Direct outputs come first so a failed conversion still leaves the
// MMD on disk.
func (r *Runner) download(ctx context.Context, item Item, documentID string) (map[string]string, error) {
	outputs := map[string]string{}
	if err := os.MkdirAll(filepath.Dir(item.OutputBase), 0o755); err != nil {
		return outputs, err
	}
	for _, ext := range r.outputExtensions() {
		target := item.OutputBase + "." + ext
		if err := r.downloadOne(ctx, documentID, ext, target); err != nil {
			return outputs, fmt.Errorf(".%s: %w", ext, err)
		}
		outputs[ext] = target
	}
	return outputs, nil
}

func (r *Runner) downloadOne(ctx context.Context, documentID, ext, target string) error {
	temp := target + ".part"
	for attempt := 0; ; attempt++ {
		file, err := os.Create(temp)
		if err != nil {
			return err
		}
		pending, retryAfter, err := r.Client.DownloadOutput(ctx, documentID, ext, file)
		file.Close()
		if err == nil && !pending {
			return os.Rename(temp, target)
		}
		os.Remove(temp)
		if err != nil {
			if errors.Is(err, context.Canceled) || !pcoapi.Transient(err) {
				return err
			}
			if attempt+1 >= r.MaxAttempts {
				return err
			}
			retryAfter = backoff(attempt, r.PollInitial, r.PollMax)
		}
		if retryAfter > r.PollMax {
			retryAfter = r.PollMax
		}
		if err := r.Sleep(ctx, retryAfter); err != nil {
			return err
		}
	}
}

func (r *Runner) outputsPresent(record *Record) bool {
	if len(record.Outputs) == 0 {
		return false
	}
	for _, ext := range r.outputExtensions() {
		path, ok := record.Outputs[ext]
		if !ok {
			return false
		}
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

var pdfPagePattern = regexp.MustCompile(`/Type\s*/Page[^s]`)

// dryRun lists what would be sent and estimates pages for PDFs by counting page objects; other
// document types count as one page each, which is what the deployment's own estimate does for
// files it cannot open cheaply.
func (r *Runner) dryRun(items []Item, runDir string) *Summary {
	pages := 0
	byType := map[string]int{}
	for _, item := range items {
		ext := strings.ToLower(filepath.Ext(item.Path))
		byType[ext]++
		count := 1
		if ext == ".pdf" {
			if raw, err := os.ReadFile(item.Path); err == nil {
				if found := len(pdfPagePattern.FindAll(raw, -1)); found > 0 {
					count = found
				}
			}
		}
		pages += count
		r.Progress.Event(Event{Kind: "note", Key: item.Key, Pages: count, Message: fmt.Sprintf("  %s  ~%d page%s", item.Key, count, pluralS(count))})
	}
	types := make([]string, 0, len(byType))
	for ext, count := range byType {
		types = append(types, fmt.Sprintf("%d %s", count, strings.TrimPrefix(ext, ".")))
	}
	r.Progress.Event(Event{Kind: "summary",
		Message: fmt.Sprintf("dry run: %d file%s (%s), about %d pages; nothing sent. The run record would be %s",
			len(items), pluralS(len(items)), strings.Join(types, ", "), pages, runDir)})
	return &Summary{Counts: Counts{Total: len(items)}, RunDir: runDir}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func backoff(attempt int, initial, max time.Duration) time.Duration {
	d := initial
	for i := 0; i < attempt && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}

func nowString() *string {
	s := time.Now().UTC().Format(time.RFC3339)
	return &s
}
