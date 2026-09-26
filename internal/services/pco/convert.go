package pco

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/pcoapi"
	"github.com/mathpix/mathpix-cli/internal/progress"
	"github.com/mathpix/mathpix-cli/internal/services/pco/batch"
)

// watchPollInitial is the first delay between job polls when watching a cloud job; it grows to 15s.
// A test shortens it through SetWatchPollForTests.
var watchPollInitial = 2 * time.Second

type convertFlags struct {
	formats, optionsJSON, jobSpec, resume string
	concurrency                           int
	include, exclude                      []string
	dryRun, detach, overwrite             bool
}

func newConvertCmd(flags *cli.Flags, t *transport) *cobra.Command {
	cf := &convertFlags{}
	cmd := &cobra.Command{
		Use:   "convert [SRC [DST]]",
		Short: "Convert a document, a local folder, or a folder in your bucket",
		Long: `convert is the one command that starts work, and it waits for the result.

  mpx pco convert paper.pdf                     one file: outputs land beside it (image.png OCRs via /v3/text)
  mpx pco convert ./scans/ ./out/ --formats md,docx   a local folder: N files at a time, progress per file,
                                                a report under _mathpix/<run_id>/ next to the outputs
  mpx pco convert s3://bucket/scans/            a folder in your storage: a job on the deployment, watched
                                                to the end (also gs:// and Azure blob URLs)
  mpx pco convert s3://bucket/in/ s3://bucket/out/    same, writing outputs to a different bucket folder

Every completed document yields .mmd and .lines.json; --formats adds converted formats on top
(md, html, latex, docx, pptx, xlsx, tex.zip, md.zip, mmd.zip, html.zip).

A cloud folder's outputs land in the bucket, not on this machine; rerunning the same command
reattaches to its job, and 'mpx pco jobs' manages it. A local run writes its record under
_mathpix/<run_id>/ next to the outputs and prints the run id; rerunning the same command finds the
record and skips finished files. --resume makes that explicit and --resume RUN_ID picks a record by
id. --detach returns as soon as the deployment has everything; fetch results later with --resume
(local) or 'mpx pco jobs' (cloud).`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConvert(cmd, flags, t, cf, args)
		},
	}
	cmd.Flags().StringVar(&cf.formats, "formats", "", "converted formats to add on top of .mmd and .lines.json, comma-separated: --formats md,docx")
	cmd.Flags().StringVar(&cf.optionsJSON, "options-json", "", "other /v3/pdf options, inline (--options-json '{\"page_ranges\": \"1-10\"}') or a file (--options-json options.json)")
	cmd.Flags().IntVar(&cf.concurrency, "concurrency", 0, "documents in flight at once for a local folder (default: twice the deployment's GPU workers, max 16)")
	cmd.Flags().StringArrayVar(&cf.include, "include", nil, "only files matching this glob, against the name or the relative path, repeatable (--include '*.pdf')")
	cmd.Flags().StringArrayVar(&cf.exclude, "exclude", nil, "skip files matching this glob, repeatable (--exclude 'draft-*')")
	cmd.Flags().StringVar(&cf.resume, "resume", "", "continue a previous run: --resume (the record of this same command) or --resume RUN_ID; finished files are skipped, submitted ones fetched")
	cmd.Flags().Lookup("resume").NoOptDefVal = batch.ResumeSameCommand
	cmd.Flags().BoolVar(&cf.dryRun, "dry-run", false, "list what would be sent and estimate pages; nothing is sent")
	cmd.Flags().BoolVar(&cf.detach, "detach", false, "return once the deployment has the work; fetch results later with --resume (local) or mpx pco jobs (cloud)")
	cmd.Flags().BoolVar(&cf.overwrite, "overwrite", false, "reprocess every file and replace outputs that exist, e.g. after the deployment got a new image; a fresh run or job, nothing skipped")
	cmd.Flags().StringVar(&cf.jobSpec, "job-spec", "", "submit this /pco/v1/jobs body instead of a PATH (--job-spec job.json, e.g. from mpx pco migrate-scs)")
	return cmd
}

func runConvert(cmd *cobra.Command, flags *cli.Flags, t *transport, cf *convertFlags, args []string) error {
	g, err := cli.Resolve(*flags, cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	formats, err := pcoapi.ParseFormats(cf.formats)
	if err != nil {
		return err
	}
	requestOptions, err := parseRequestOptions(cf.optionsJSON)
	if err != nil {
		return err
	}
	cloud := cf.jobSpec != "" || (len(args) >= 1 && isCloudFolder(args[0]))
	// A local dry run reads only the disk, so it works before an endpoint is configured.
	var c *pcoapi.Client
	if cloud || !cf.dryRun {
		c, err = newClientFor(g, t)
		if err != nil {
			return err
		}
	}
	if cloud {
		return runConvertCloud(cmd, c, cf, args, formats, requestOptions)
	}
	return runConvertLocal(cmd, g, c, cf, args, formats, requestOptions)
}

// parseRequestOptions reads the --options-json value, inline JSON or a file, and drops
// conversion_formats, which --formats owns.
func parseRequestOptions(value string) (map[string]any, error) {
	if strings.TrimSpace(value) == "" {
		return map[string]any{}, nil
	}
	raw := []byte(value)
	if !strings.HasPrefix(strings.TrimSpace(value), "{") {
		fileBytes, err := os.ReadFile(value)
		if err != nil {
			return nil, fmt.Errorf("--options-json: %w", err)
		}
		raw = fileBytes
	}
	var options map[string]any
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, fmt.Errorf("--options-json is not a JSON object: %w", err)
	}
	delete(options, "conversion_formats")
	return options, nil
}

func runConvertLocal(cmd *cobra.Command, g *cli.Global, c *pcoapi.Client, cf *convertFlags, args []string, formats []string, requestOptions map[string]any) error {
	// A flag with an optional value only takes it as --resume=ID; with --resume ID the id arrives
	// as the last positional argument. Accept both spellings when it is a run id and not a file.
	if cf.resume == batch.ResumeSameCommand && len(args) >= 1 && looksLikeRunID(args[len(args)-1]) {
		if _, err := os.Stat(args[len(args)-1]); err != nil {
			cf.resume, args = args[len(args)-1], args[:len(args)-1] // inputs (if any) precede it; else from the record
		}
	}
	if len(args) == 0 && (cf.resume == "" || cf.resume == batch.ResumeSameCommand) {
		return errors.New("nothing to convert: give a file, a folder, a cloud folder URI, --job-spec, or --resume RUN_ID")
	}
	for _, arg := range args {
		if isCloudFolder(arg) {
			return errors.New("cloud folders and local paths cannot be mixed in one run")
		}
	}
	src, dst := "", ""
	if len(args) >= 1 {
		src = args[0]
	}
	if len(args) >= 2 {
		dst = args[1]
	}
	// The fast path preserves the single-image /v3/text route and the one-line progress indicator:
	// a lone local file (DST, if given, is its output base) with no batch-only flag. Folders, a
	// resume, and the batch-only flags go through the batch runner instead.
	single := src != "" && !isDir(src) && cf.resume == "" && !cf.dryRun && !cf.detach &&
		len(cf.include) == 0 && len(cf.exclude) == 0
	if single {
		engine := &pcoEngine{client: c, options: requestOptions, formats: formats, showProgress: g.ShowProgress()}
		return engine.runSingle(cmd, src, dst)
	}
	// SRC is the one input; DST (if any) is the output directory. When resuming by id there is no
	// SRC and the inputs come from the run's manifest.
	var inputs []string
	if src != "" {
		inputs = []string{src}
	}
	printer := batch.New(cmd.OutOrStdout())
	signals, stopSignals := interruptSignals()
	defer stopSignals()
	runner := &batch.Runner{Client: c, Progress: printer, Interrupt: signals,
		ConfirmStop: confirmStopLocalRun(cmd.ErrOrStderr(), signals), Options: batch.Options{
			Inputs: inputs, OutDir: dst, Formats: formats, RequestOptions: requestOptions, Concurrency: cf.concurrency,
			Include: cf.include, Exclude: cf.exclude, Detach: cf.detach, DryRun: cf.dryRun, Resume: cf.resume,
			Overwrite: cf.overwrite}}
	summary, err := runner.Run(cmd.Context())
	if err != nil {
		return err
	}
	if cf.dryRun {
		return nil
	}
	resumeCommand := fmt.Sprintf("mpx pco convert --resume %s", summary.RunID)
	message := fmt.Sprintf("run %s; report: %s", summary.RunID, summary.ReportPath)
	switch {
	case summary.Interrupted:
		message = fmt.Sprintf("stopped: %d of %d files were submitted and keep processing on the deployment. Fetch their results with: %s",
			summary.Submitted, summary.Counts.Total, resumeCommand)
	case cf.detach || summary.Counts.Processing > 0:
		message = fmt.Sprintf("run %s: %d document(s) processing on the deployment. Fetch them with: %s", summary.RunID, summary.Counts.Processing, resumeCommand)
	}
	printer.Event(batch.Event{Kind: "summary", Counts: &summary.Counts, Message: message})
	if summary.Counts.Failed > 0 {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("%d file(s) failed", summary.Counts.Failed)}
	}
	if summary.Interrupted {
		return &cli.ExitError{Code: 130}
	}
	return nil
}

func runConvertCloud(cmd *cobra.Command, c *pcoapi.Client, cf *convertFlags, args []string, formats []string, requestOptions map[string]any) error {
	if cf.resume != "" {
		return errors.New("--resume is for local runs, whose outputs the tool downloads. A cloud folder's outputs are written into the bucket by the deployment; " +
			"rerun the same command to reattach to its job (the job id comes from the spec), or `mpx pco jobs get JOB_ID`")
	}
	spec, err := cloudJobSpec(cf, args, formats, requestOptions)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	raw, created, err := c.CreateJob(ctx, spec, cf.dryRun)
	if err != nil {
		return err
	}
	if cf.dryRun {
		indented, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(indented))
		return nil
	}
	var job pcoapi.Job
	if err := json.Unmarshal(raw, &job); err != nil {
		return fmt.Errorf("job response: %w", err)
	}
	printer := batch.New(cmd.OutOrStdout())
	switch {
	case created && cf.overwrite:
		printer.Event(batch.Event{Kind: "note", Message: fmt.Sprintf("job %s created; existing outputs will be replaced", job.JobID)})
	case created:
		printer.Event(batch.Event{Kind: "note", Message: fmt.Sprintf("job %s created", job.JobID)})
	default:
		printer.Event(batch.Event{Kind: "note", Message: fmt.Sprintf("job %s already exists for this spec; attaching to it", job.JobID)})
	}
	if cf.detach {
		printer.Event(batch.Event{Kind: "summary", Message: fmt.Sprintf("detached. Watch with: mpx pco jobs get %s", job.JobID)})
		return nil
	}
	signals, stopSignals := interruptSignals()
	defer stopSignals()
	return watchCloudJob(cmd, c, printer, job.JobID, signals)
}

// cloudFileStreamCap bounds per-file line streaming for a watched job: for a job larger than this
// the watch shows only the aggregate line, since diffing tens of thousands of files each poll is
// not worth it. Local runs always stream per file, because the client drives each one anyway.
const cloudFileStreamCap = 1000

// cloudJobSpec builds the /pco/v1/jobs body for a folder URI (args[0] input, optional args[1]
// output folder), or loads and adjusts --job-spec.
func cloudJobSpec(cf *convertFlags, args []string, formats []string, requestOptions map[string]any) (map[string]any, error) {
	if cf.jobSpec != "" {
		rawSpec, err := os.ReadFile(cf.jobSpec)
		if err != nil {
			return nil, fmt.Errorf("--job-spec: %w", err)
		}
		var spec map[string]any
		if err := json.Unmarshal(rawSpec, &spec); err != nil {
			return nil, fmt.Errorf("--job-spec %s is not a JSON object: %w", cf.jobSpec, err)
		}
		if len(args) >= 1 {
			spec["input"] = map[string]any{"folder": args[0]}
		}
		if cf.overwrite {
			output, _ := spec["output"].(map[string]any)
			if output == nil {
				output = map[string]any{}
			}
			output["on_existing"] = "replace"
			spec["output"] = output
			spec["client_reference"] = overwriteReference()
		}
		return spec, nil
	}
	folder := args[0]
	if !strings.HasSuffix(folder, "/") {
		folder += "/"
	}
	output := map[string]any{"folder": folder, "layout": "alongside", "on_existing": "skip"}
	spec := map[string]any{}
	if cf.overwrite {
		// The job id is a hash of the spec without on_existing, so a distinct client_reference
		// makes this a new job rather than the existing one being returned.
		output["on_existing"] = "replace"
		spec["client_reference"] = overwriteReference()
	}
	if len(args) >= 2 {
		outURI := args[1]
		if !isCloudFolder(outURI) {
			return nil, errors.New("the output folder for a cloud convert must be a folder URI (s3://, gs:// or an Azure blob URL)")
		}
		if !strings.HasSuffix(outURI, "/") {
			outURI += "/"
		}
		output["folder"] = outURI
		if outURI != folder {
			// `alongside` requires output.folder == input.folder; a separate output folder gives
			// each input its own folder under it (2401/paper.pdf -> out/2401/paper/paper.mmd).
			output["layout"] = "mirror"
		}
	}
	options := map[string]any{"extra_outputs": []string{".lines.json"}}
	if len(formats) > 0 {
		options["conversion_formats"] = pcoapi.ConversionFormats(formats)
	}
	if len(requestOptions) > 0 {
		options["ocr"] = requestOptions
	}
	input := map[string]any{"folder": folder, "recursive": true}
	if len(cf.include) > 0 {
		input["include"] = cf.include
	}
	if len(cf.exclude) > 0 {
		input["exclude"] = cf.exclude
	}
	spec["input"], spec["output"], spec["options"] = input, output, options
	return spec, nil
}

func overwriteReference() string {
	return "pco-overwrite-" + time.Now().UTC().Format("20060102T150405Z")
}

// watchCloudJob polls a job and drives the same batch.Printer a local run uses: a per-file
// completed/failed stream (for jobs at or under cloudFileStreamCap) plus the shared live status
// line, then a summary. Exit 1 when files failed or the job did not complete. Ctrl-C asks whether
// to stop watching; the job keeps running on the deployment either way.
func watchCloudJob(cmd *cobra.Command, c *pcoapi.Client, printer batch.Printer, jobID string, signals <-chan os.Signal) error {
	ctx := cmd.Context()
	seen := map[string]bool{}
	delay := watchPollInitial
	for {
		job, err := c.GetJob(ctx, jobID)
		if err != nil {
			if pcoapi.Transient(err) && ctx.Err() == nil {
				printer.Event(batch.Event{Kind: "note", Message: "warning: " + err.Error()})
			} else {
				return err
			}
		} else {
			if d := job.Files["discovered"]; d > 0 && d <= cloudFileStreamCap {
				streamNewCloudFiles(ctx, c, printer, jobID, seen)
			}
			counts := cloudCounts(job)
			printer.Event(batch.Event{Kind: "status", Counts: &counts, Throughput: job.Throughput, ETASeconds: job.ETASeconds})
			if job.Terminal() {
				message := fmt.Sprintf("job %s %s", job.JobID, job.Status)
				if uri, ok := job.Output["report_uri"].(string); ok && uri != "" {
					message += "; report: " + uri
				}
				printer.Event(batch.Event{Kind: "summary", Counts: &counts, Message: message})
				if job.Files["failed"] > 0 || !strings.HasPrefix(job.Status, "completed") {
					return &cli.ExitError{Code: 1, Message: fmt.Sprintf("job %s ended %s", jobID, job.Status)}
				}
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return &cli.ExitError{Code: 130, Message: fmt.Sprintf("job %s keeps running on the deployment; watch again with: mpx pco jobs get %s", jobID, jobID)}
		case <-signals:
			fmt.Fprintf(cmd.ErrOrStderr(), "\ninterrupted: job %s keeps running on the deployment whether you watch it or not; `mpx pco jobs cancel %s` stops it.\n", jobID, jobID)
			if askYesNo(cmd.ErrOrStderr(), os.Stdin, signals, "Stop watching?") {
				return &cli.ExitError{Code: 130, Message: fmt.Sprintf("job %s; watch again with: mpx pco jobs get %s", jobID, jobID)}
			}
		case <-time.After(delay):
			if delay < 15*time.Second {
				delay += time.Second
			}
		}
	}
}

func cloudCounts(job *pcoapi.Job) batch.Counts {
	return batch.Counts{Total: job.Files["discovered"], Completed: job.Files["completed"], Failed: job.Files["failed"],
		Skipped: job.Files["skipped"], Processing: job.Files["processing"], Pages: job.Pages["completed"]}
}

// streamNewCloudFiles emits one line for each file that reached a terminal state since the last
// poll, so a watched job shows the same completed/failed stream a local run does.
func streamNewCloudFiles(ctx context.Context, c *pcoapi.Client, printer batch.Printer, jobID string, seen map[string]bool) {
	for _, status := range []string{"completed", "failed"} {
		cursor := ""
		for {
			page, err := c.ListFiles(ctx, jobID, status, cursor, 200)
			if err != nil {
				return
			}
			for _, f := range page.Files {
				if seen[f.DocumentID] {
					continue
				}
				seen[f.DocumentID] = true
				if f.Status == "failed" {
					printer.Event(batch.Event{Kind: "failed", Key: f.Key, ErrorID: deref(f.ErrorID), Message: deref(f.ErrorMessage)})
				} else {
					printer.Event(batch.Event{Kind: "completed", Key: f.Key, Pages: f.Pages, Outputs: outputBaseNames(f.Outputs)})
				}
			}
			if page.NextCursor == nil || *page.NextCursor == "" {
				break
			}
			cursor = *page.NextCursor
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func outputBaseNames(outputs map[string]string) []string {
	names := make([]string, 0, len(outputs))
	for _, uri := range outputs {
		if i := strings.LastIndex(uri, "/"); i >= 0 {
			names = append(names, uri[i+1:])
		} else {
			names = append(names, uri)
		}
	}
	sort.Strings(names)
	return names
}

// looksLikeRunID matches the 12 hex characters of a run record's name.
func looksLikeRunID(value string) bool {
	if len(value) != 12 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

type pcoEngine struct {
	client       *pcoapi.Client
	options      map[string]any
	formats      []string
	showProgress bool
	onProgress   func(percent float64, detail string)
}

// extensions returns the download extensions for one document: the always-generated ones plus the
// extension each requested conversion format downloads as.
func (e *pcoEngine) extensions() []string {
	exts := append([]string{}, pcoapi.DirectExtensions...)
	for _, f := range e.formats {
		if ext, ok := pcoapi.ExtensionByFormat[f]; ok {
			exts = append(exts, ext)
		}
	}
	return exts
}

func (e *pcoEngine) convertOne(ctx context.Context, path, outputBase string) ([]string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if imageInputExts[ext] && !strings.HasPrefix(path, "http") && allTextNative(e.extensions()) {
		return e.convertImageOne(ctx, path, outputBase)
	}
	options := map[string]any{}
	for k, v := range e.options {
		options[k] = v
	}
	if len(e.formats) > 0 {
		options["conversion_formats"] = pcoapi.ConversionFormats(e.formats)
	}
	var (
		resp *pcoapi.SubmitResponse
		err  error
	)
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		resp, err = e.client.SubmitURL(ctx, path, options)
	} else {
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil, openErr
		}
		resp, err = e.client.SubmitFile(ctx, f, filepath.Base(path), options)
		f.Close()
	}
	if err != nil {
		return nil, err
	}
	if err := e.poll(ctx, resp.PDFID); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(outputBase), 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, ext := range e.extensions() {
		out := outputBase + "." + ext
		if err := e.download(ctx, resp.PDFID, ext, out); err != nil {
			return written, fmt.Errorf("download %s: %w", ext, err)
		}
		written = append(written, out)
	}
	return written, nil
}

func pcoProgress(percent float64, done, total int) (float64, string) {
	if percent <= 0 && total > 0 {
		percent = float64(done) / float64(total) * 100
	}
	if total > 0 {
		return percent, fmt.Sprintf("%d/%d pages", done, total)
	}
	if percent > 0 {
		return percent, ""
	}
	return -1, ""
}

func (e *pcoEngine) poll(ctx context.Context, id string) error {
	delay := time.Second
	for {
		s, err := e.client.DocumentStatus(ctx, id)
		if err != nil {
			return err
		}
		if s.Status == "error" {
			if s.ErrorInfo != nil {
				return fmt.Errorf("processing failed: %s", s.ErrorInfo.Message)
			}
			return fmt.Errorf("processing failed")
		}
		if e.onProgress != nil {
			e.onProgress(pcoProgress(s.PercentDone, s.NumPagesCompleted, s.NumPages))
		}
		if s.Terminal() {
			return nil
		}
		if err := sleep(ctx, delay); err != nil {
			return err
		}
		if delay < 10*time.Second {
			delay *= 2
		}
	}
}

func (e *pcoEngine) download(ctx context.Context, id, ext, out string) error {
	part := out + ".part"
	for {
		f, err := os.Create(part)
		if err != nil {
			return err
		}
		pending, retryAfter, err := e.client.DownloadOutput(ctx, id, ext, f)
		f.Close()
		if err != nil {
			os.Remove(part)
			return err
		}
		if pending {
			os.Remove(part)
			if retryAfter > 10*time.Second {
				retryAfter = 10 * time.Second
			}
			if err := sleep(ctx, retryAfter); err != nil {
				return err
			}
			continue
		}
		return os.Rename(part, out)
	}
}

func (e *pcoEngine) runSingle(cmd *cobra.Command, src, dst string) error {
	base := strings.TrimSuffix(src, filepath.Ext(src))
	if dst != "" {
		base = trimKnownExt(dst)
	}
	ind := progress.NewIndicator(cmd.ErrOrStderr(), e.showProgress)
	ind.Start("converting " + filepath.Base(src))
	e.onProgress = ind.Set
	outputs, err := e.convertOne(cmd.Context(), src, base)
	ind.Stop()
	if err != nil {
		return err
	}
	for _, o := range outputs {
		fmt.Fprintln(cmd.OutOrStdout(), o)
	}
	return nil
}

func isCloudFolder(p string) bool {
	return strings.HasPrefix(p, "s3://") || strings.HasPrefix(p, "gs://") || strings.HasPrefix(p, "https://") && strings.Contains(p, "blob.core.windows.net")
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func trimKnownExt(dst string) string {
	for _, ext := range pcoapi.ExtensionByFormat {
		if strings.HasSuffix(strings.ToLower(dst), "."+ext) {
			return dst[:len(dst)-len(ext)-1]
		}
	}
	for _, ext := range pcoapi.DirectExtensions {
		if strings.HasSuffix(strings.ToLower(dst), "."+ext) {
			return dst[:len(dst)-len(ext)-1]
		}
	}
	return strings.TrimSuffix(dst, filepath.Ext(dst))
}

func sleep(ctx context.Context, d time.Duration) error {
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
