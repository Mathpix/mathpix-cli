package pco

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/pcoapi"
	"github.com/mathpix/mathpix-cli/internal/progress"
)

var docExts = map[string]bool{
	"pdf": true, "png": true, "jpg": true, "jpeg": true, "tif": true, "tiff": true,
	"webp": true, "gif": true, "bmp": true, "docx": true, "pptx": true, "xlsx": true, "epub": true,
}

func newConvertCmd(flags *cli.Flags, t *transport) *cobra.Command {
	var (
		formatsCSV  string
		concurrency int
		optionsJSON string
	)
	cmd := &cobra.Command{
		Use:   "convert SRC [DST]",
		Short: "Convert a document, a local folder, or a folder in your bucket",
		Long: `Convert against a deployment.

  mpx pco --endpoint URL convert paper.pdf paper.mmd
  mpx pco --endpoint URL convert ./scans/ ./out/ --formats docx,md
  mpx pco --endpoint URL convert s3://bucket/scans/ --formats md

A file or a local folder is sent to the deployment's /v3/pdf; outputs are written locally. A cloud
folder (s3://, gs:// or an Azure blob URL) becomes a server-side job over the deployment's own
storage; DST is not used and the outputs land in your bucket.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, c, err := client(cmd, flags, t)
			if err != nil {
				return err
			}
			formats, err := pcoapi.ParseFormats(formatsCSV)
			if err != nil {
				return err
			}
			options := map[string]any{}
			if optionsJSON != "" {
				if err := json.Unmarshal([]byte(optionsJSON), &options); err != nil {
					return fmt.Errorf("--options-json is not valid JSON: %w", err)
				}
			}
			src := args[0]
			if isCloudFolder(src) {
				return runCloudJob(cmd, c, src, formats, options)
			}
			dst := ""
			if len(args) == 2 {
				dst = args[1]
			}
			engine := &pcoEngine{client: c, options: options, formats: formats}
			if isDir(src) {
				return engine.runFolder(cmd, src, dst, concurrency)
			}
			return engine.runSingle(cmd, src, dst)
		},
	}
	cmd.Flags().StringVar(&formatsCSV, "formats", "", "conversion formats, comma-separated (e.g. docx,md,tex.zip)")
	cmd.Flags().IntVar(&concurrency, "concurrency", 4, "folder mode: files converted in parallel")
	cmd.Flags().StringVar(&optionsJSON, "options-json", "", "extra API options as a JSON object")
	return cmd
}

type pcoEngine struct {
	client  *pcoapi.Client
	options map[string]any
	formats []string
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
	outputs, err := e.convertOne(cmd.Context(), src, base)
	if err != nil {
		return err
	}
	for _, o := range outputs {
		fmt.Fprintln(cmd.OutOrStdout(), o)
	}
	return nil
}

func (e *pcoEngine) runFolder(cmd *cobra.Command, srcDir, dstDir string, concurrency int) error {
	root, err := filepath.Abs(srcDir)
	if err != nil {
		return err
	}
	var items [][2]string // {path, outputBase}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "_mathpix" || (strings.HasPrefix(d.Name(), ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if !docExts[ext] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		base := strings.TrimSuffix(path, filepath.Ext(path))
		if dstDir != "" {
			base = strings.TrimSuffix(filepath.Join(dstDir, rel), filepath.Ext(rel))
		}
		items = append(items, [2]string{path, base})
		return nil
	})
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "no documents found")
		return nil
	}
	if concurrency < 1 {
		concurrency = 4
	}
	printer := progress.New(cmd.OutOrStdout())
	var (
		mu       sync.Mutex
		failures int
		wg       sync.WaitGroup
	)
	queue := make(chan [2]string)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range queue {
				start := time.Now()
				outputs, err := e.convertOne(cmd.Context(), it[0], it[1])
				key := filepath.Base(it[0])
				if err != nil {
					printer.Fail(key, err)
					mu.Lock()
					failures++
					mu.Unlock()
					continue
				}
				names := make([]string, len(outputs))
				for i, o := range outputs {
					names[i] = filepath.Base(o)
				}
				printer.Done(key, names, time.Since(start).Round(100*time.Millisecond).String())
			}
		}()
	}
	for _, it := range items {
		if cmd.Context().Err() != nil {
			break
		}
		queue <- it
	}
	close(queue)
	wg.Wait()
	fmt.Fprintf(cmd.ErrOrStderr(), "%d of %d converted, %d failed\n", len(items)-failures, len(items), failures)
	if failures > 0 {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("%d file(s) failed", failures)}
	}
	return nil
}

// runCloudJob submits a folder in the customer's bucket as a server-side job and watches it.
func runCloudJob(cmd *cobra.Command, c *pcoapi.Client, folder string, formats []string, options map[string]any) error {
	spec := map[string]any{"input": map[string]any{"folder": folder}}
	jobOptions := map[string]any{}
	for k, v := range options {
		jobOptions[k] = v
	}
	if len(formats) > 0 {
		jobOptions["conversion_formats"] = pcoapi.ConversionFormats(formats)
	}
	if len(jobOptions) > 0 {
		spec["options"] = jobOptions
	}
	raw, created, err := c.CreateJob(cmd.Context(), spec, false)
	if err != nil {
		return err
	}
	var created0 pcoapi.Job
	json.Unmarshal(raw, &created0)
	if created {
		fmt.Fprintf(cmd.ErrOrStderr(), "job %s created\n", created0.JobID)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "job %s already exists, watching\n", created0.JobID)
	}
	delay := 2 * time.Second
	for {
		job, err := c.GetJob(cmd.Context(), created0.JobID)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "  %s  files: %v\n", job.Status, job.Files)
		if job.Terminal() {
			out, _ := json.MarshalIndent(job, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			if job.Status == "error" {
				return &cli.ExitError{Code: 1, Message: "job ended in error"}
			}
			return nil
		}
		if err := sleep(cmd.Context(), delay); err != nil {
			return err
		}
		if delay < 15*time.Second {
			delay += time.Second
		}
	}
}

func isCloudFolder(p string) bool {
	return strings.HasPrefix(p, "s3://") || strings.HasPrefix(p, "gs://") || strings.HasPrefix(p, "https://") && strings.Contains(p, "blob.core.windows.net")
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func trimKnownExt(dst string) string {
	for f, ext := range pcoapi.ExtensionByFormat {
		_ = f
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
