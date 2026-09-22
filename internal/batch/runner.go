package batch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mathpix/mathpix-cli/internal/progress"
	"github.com/mathpix/mathpix-cli/internal/scsapi"
)

// Engine converts one document at a time against the API. It is shared by the single-file convert
// command and the folder runner, so both take the same submit → poll → download path.
type Engine struct {
	Client      *scsapi.Client
	Options     map[string]any // request options applied to every submission (rm_spaces, --options-json, ...)
	PollInitial time.Duration
	PollMax     time.Duration
}

// NewEngine builds an Engine with sane polling defaults.
func NewEngine(client *scsapi.Client, options map[string]any) *Engine {
	return &Engine{Client: client, Options: options, PollInitial: time.Second, PollMax: 10 * time.Second}
}

// Convert produces every requested format for one item and returns the paths written. A Markdown
// source converts through /v3/converter; a local image whose outputs are all text-style
// (mmd/txt/html/tex/json) goes to /v3/text, the synchronous image endpoint; everything else, including
// an image asked for a rich format like docx, goes to /v3/pdf.
func (e *Engine) Convert(ctx context.Context, item Item) ([]string, error) {
	if item.IsMMDInput() {
		return e.convertMarkdown(ctx, item)
	}
	if imageInputExts[item.Ext] && !isURL(item.Path) && allTextNative(item.Formats) {
		return e.convertImage(ctx, item)
	}
	return e.convertDocument(ctx, item)
}

// imageInputExts are single images the synchronous /v3/text endpoint can OCR.
var imageInputExts = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "tif": true, "tiff": true, "webp": true, "gif": true, "bmp": true,
}

// textField maps an output format to the /v3/text response field that holds it and the `formats` the
// request must ask for. An empty field means "write the whole JSON result". ok is false for a format
// /v3/text cannot produce (docx, pptx, the zip bundles), so those route to /v3/pdf instead.
func textField(format string) (field string, request []string, ok bool) {
	switch format {
	case "mmd", "txt", "text":
		return "text", []string{"text"}, true
	case "html":
		return "html", []string{"text", "html"}, true
	case "tex", "latex":
		return "latex_styled", []string{"text", "latex_styled"}, true
	case "json", "lines.json":
		return "", []string{"text", "data"}, true
	}
	return "", nil, false
}

func allTextNative(formats []string) bool {
	for _, f := range formats {
		if _, _, ok := textField(f); !ok {
			return false
		}
	}
	return len(formats) > 0
}

// convertImage OCRs one image through /v3/text and writes each requested text-style output.
func (e *Engine) convertImage(ctx context.Context, item Item) ([]string, error) {
	requested := map[string]bool{}
	for _, f := range item.Formats {
		_, req, _ := textField(f)
		for _, r := range req {
			requested[r] = true
		}
	}
	options := map[string]any{}
	for k, v := range e.Options {
		options[k] = v
	}
	if len(requested) > 0 {
		formats := make([]string, 0, len(requested))
		for r := range requested {
			formats = append(formats, r)
		}
		sort.Strings(formats)
		options["formats"] = formats
	}
	f, err := os.Open(item.Path)
	if err != nil {
		return nil, err
	}
	raw, err := e.Client.SubmitTextFile(ctx, f, filepath.Base(item.Path), options)
	f.Close()
	if err != nil {
		return nil, err
	}
	var result map[string]any
	json.Unmarshal(raw, &result)
	if err := os.MkdirAll(filepath.Dir(item.OutputBase), 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, format := range item.Formats {
		out := item.OutputBase + "." + format
		field, _, _ := textField(format)
		var data []byte
		if field == "" {
			data = raw
		} else if s, ok := result[field].(string); ok {
			data = []byte(s)
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return written, err
		}
		written = append(written, out)
	}
	return written, nil
}

func (e *Engine) convertDocument(ctx context.Context, item Item) ([]string, error) {
	options := map[string]any{}
	for k, v := range e.Options {
		options[k] = v
	}
	if field := scsapi.ConversionFormatsField(item.Formats); len(field) > 0 {
		options["conversion_formats"] = field
	}
	var (
		id  string
		err error
	)
	if isURL(item.Path) {
		id, err = e.Client.SubmitURL(ctx, item.Path, options)
	} else {
		f, openErr := os.Open(item.Path)
		if openErr != nil {
			return nil, openErr
		}
		id, err = e.Client.SubmitFile(ctx, f, filepath.Base(item.Path), options)
		f.Close()
	}
	if err != nil {
		return nil, err
	}
	if err := e.pollDocument(ctx, id); err != nil {
		return nil, err
	}
	return e.downloadAll(ctx, item, func(ctx context.Context, ext string, w *os.File) (bool, time.Duration, error) {
		return e.Client.Download(ctx, id, ext, w)
	})
}

func (e *Engine) convertMarkdown(ctx context.Context, item Item) ([]string, error) {
	source, err := os.ReadFile(item.Path)
	if err != nil {
		return nil, err
	}
	var written []string
	// An mmd/md output of a markdown source is just the source itself; no conversion call needed.
	remaining := item.Formats[:0:0]
	for _, f := range item.Formats {
		if f == "mmd" || f == "md" {
			out := item.OutputBase + "." + f
			if err := writeFile(out, source); err != nil {
				return written, err
			}
			written = append(written, out)
			continue
		}
		remaining = append(remaining, f)
	}
	if len(remaining) == 0 {
		return written, nil
	}
	id, err := e.Client.SubmitConverter(ctx, string(source), scsapi.ConversionFormatsField(remaining), nil)
	if err != nil {
		return written, err
	}
	if err := e.pollConverter(ctx, id); err != nil {
		return written, err
	}
	paths, err := e.downloadInto(ctx, item, remaining, func(ctx context.Context, ext string, w *os.File) (bool, time.Duration, error) {
		return e.Client.DownloadConverter(ctx, id, ext, w)
	})
	return append(written, paths...), err
}

type downloadFunc func(ctx context.Context, ext string, w *os.File) (bool, time.Duration, error)

func (e *Engine) downloadAll(ctx context.Context, item Item, dl downloadFunc) ([]string, error) {
	return e.downloadInto(ctx, item, item.Formats, dl)
}

func (e *Engine) downloadInto(ctx context.Context, item Item, formats []string, dl downloadFunc) ([]string, error) {
	if err := os.MkdirAll(filepath.Dir(item.OutputBase), 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, ext := range formats {
		out := item.OutputBase + "." + ext
		if err := e.downloadOne(ctx, ext, out, dl); err != nil {
			return written, fmt.Errorf("download %s: %w", ext, err)
		}
		written = append(written, out)
	}
	return written, nil
}

// downloadOne writes one format to a .part file then renames it, so an interrupted download never
// leaves a file that looks complete. It loops while the format is still converting (202/404).
func (e *Engine) downloadOne(ctx context.Context, ext, out string, dl downloadFunc) error {
	part := out + ".part"
	for {
		f, err := os.Create(part)
		if err != nil {
			return err
		}
		pending, retryAfter, err := dl(ctx, ext, f)
		f.Close()
		if err != nil {
			os.Remove(part)
			return err
		}
		if pending {
			os.Remove(part)
			if retryAfter > e.PollMax {
				retryAfter = e.PollMax
			}
			if err := sleepCtx(ctx, retryAfter); err != nil {
				return err
			}
			continue
		}
		return os.Rename(part, out)
	}
}

func (e *Engine) pollDocument(ctx context.Context, id string) error {
	delay := e.PollInitial
	for {
		s, err := e.Client.GetStatus(ctx, id)
		if err != nil {
			return err
		}
		if s.Status == "error" {
			return fmt.Errorf("processing failed: %s", statusError(s))
		}
		if s.Terminal() {
			return nil
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return err
		}
		delay = nextDelay(delay, e.PollMax)
	}
}

func (e *Engine) pollConverter(ctx context.Context, id string) error {
	delay := e.PollInitial
	for {
		s, err := e.Client.GetConverterStatus(ctx, id)
		if err != nil {
			return err
		}
		if s.Status == "error" {
			return fmt.Errorf("conversion failed: %s", statusError(s))
		}
		if s.Status == "completed" || s.Status == "" {
			return nil
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return err
		}
		delay = nextDelay(delay, e.PollMax)
	}
}

// Result records the outcome of one item for the report and the final tally.
type Result struct {
	Key     string   `json:"key"`
	Status  string   `json:"status"`
	Outputs []string `json:"outputs,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// Run converts all items with the given concurrency, printing progress and writing a report under
// _mathpix/<runid>/. It returns the number of failures.
func (e *Engine) Run(ctx context.Context, items []Item, concurrency int, printer *progress.Printer, report *Report) int {
	if concurrency < 1 {
		concurrency = 4
	}
	var (
		mu       sync.Mutex
		failures int
		workers  sync.WaitGroup
	)
	queue := make(chan Item)
	worker := func() {
		defer workers.Done()
		for item := range queue {
			start := time.Now()
			if skip, outputs := existingOutputs(item); skip {
				printer.Skip(item.Key)
				report.Add(Result{Key: item.Key, Status: "skipped", Outputs: outputs})
				continue
			}
			outputs, err := e.Convert(ctx, item)
			elapsed := time.Since(start).Round(100 * time.Millisecond).String()
			if err != nil {
				printer.Fail(item.Key, err)
				report.Add(Result{Key: item.Key, Status: "error", Error: err.Error()})
				mu.Lock()
				failures++
				mu.Unlock()
				continue
			}
			printer.Done(item.Key, baseNames(outputs), elapsed)
			report.Add(Result{Key: item.Key, Status: "completed", Outputs: outputs})
		}
	}
	for i := 0; i < concurrency; i++ {
		workers.Add(1)
		go worker()
	}
	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		queue <- item
	}
	close(queue)
	workers.Wait()
	return failures
}

func existingOutputs(item Item) (bool, []string) {
	var outputs []string
	for _, ext := range item.Formats {
		out := item.OutputBase + "." + ext
		if _, err := os.Stat(out); err != nil {
			return false, nil
		}
		outputs = append(outputs, out)
	}
	return len(outputs) > 0, outputs
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func statusError(s *scsapi.Status) string {
	if s.ErrorInfo != nil && s.ErrorInfo.Message != "" {
		return s.ErrorInfo.Message
	}
	if s.Error != "" {
		return s.Error
	}
	return "unknown error"
}

func nextDelay(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isURL(p string) bool {
	return strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://")
}

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}
