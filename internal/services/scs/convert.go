package scs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/batch"
	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/progress"
	"github.com/mathpix/mathpix-cli/internal/scsapi"
)

// sleepCtx waits d, or returns early if the context is cancelled.
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

type convertFlags struct {
	mapValue       string
	concurrency    int
	pageRanges     string
	rmSpaces       bool
	eqTags         bool
	optionsJSON    string
	async          bool
	destination    string
	webhookURL     string
	webhookEvents  []string
	webhookHeaders []string
}

func newConvertCmd(flags *cli.Flags) *cobra.Command {
	cf := &convertFlags{}
	cmd := &cobra.Command{
		Use:   "convert SRC DST",
		Short: "Convert a document, or a folder of documents",
		Long: `Convert one document to one output, or a folder of documents to a folder of outputs.

Single file: DST's extension picks the output format.
  mpx scs convert paper.pdf paper.mmd
  mpx scs convert paper.pdf paper.docx
  mpx scs convert notes.md notes.docx        (Markdown source goes through /v3/converter)
  mpx scs convert equation.png equation.mmd  (an image goes through /v3/text)

Folder: SRC and DST are directories; --map says which input extensions to convert and to what.
  mpx scs convert ./in/ ./out/ --map pdf:docx,md:docx
  mpx scs convert ./in/ ./out/ --map pdf:docx+mmd,tiff:mmd
Without --map, every supported document is converted to mmd.

Any API option without a dedicated flag is reachable through --options-json, e.g.
  --options-json '{"rm_fonts": true, "math_inline_delimiters": ["$", "$"]}'`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			options, err := cf.options()
			if err != nil {
				return err
			}
			src, dst := args[0], args[1]
			if cf.async {
				return runAsyncSingle(cmd, client, cf, options, src, dst)
			}
			if isExistingDir(src) {
				return runFolder(cmd, g, client, cf, options, src, dst)
			}
			return runSingle(cmd, client, cf, options, src, dst)
		},
	}
	cmd.Flags().StringVar(&cf.mapValue, "map", "", "folder mode: inputExt:format[+format] pairs, comma-separated (e.g. pdf:docx,md:docx)")
	cmd.Flags().IntVar(&cf.concurrency, "concurrency", 4, "folder mode: files converted in parallel")
	cmd.Flags().StringVar(&cf.pageRanges, "page-ranges", "", "pages to process, e.g. 1-5,8 (documents only)")
	cmd.Flags().BoolVar(&cf.rmSpaces, "rm-spaces", false, "remove extra spaces from text lines")
	cmd.Flags().BoolVar(&cf.eqTags, "include-equation-tags", false, "keep equation numbers from the source")
	cmd.Flags().StringVar(&cf.optionsJSON, "options-json", "", "extra API options as a JSON object, merged over the flags")
	cmd.Flags().BoolVar(&cf.async, "async", false, "send a single file through the Files API (/files/v1) instead of /v3/pdf")
	cmd.Flags().StringVar(&cf.destination, "destination", "", "Files API only (--async): write outputs to this bucket URI (destination_uri)")
	cmd.Flags().StringVar(&cf.webhookURL, "webhook-url", "", "notify this URL when done (callback_url); works with and without --async")
	cmd.Flags().StringArrayVar(&cf.webhookEvents, "webhook-event", nil, "webhook events, e.g. file.completed (repeatable; callback_events)")
	cmd.Flags().StringArrayVar(&cf.webhookHeaders, "webhook-header", nil, "header to send with the webhook, key=value (repeatable; callback_headers)")
	return cmd
}

func (cf *convertFlags) options() (map[string]any, error) {
	options := map[string]any{}
	if cf.pageRanges != "" {
		options["page_ranges"] = cf.pageRanges
	}
	if cf.rmSpaces {
		options["rm_spaces"] = true
	}
	if cf.eqTags {
		options["include_equation_tags"] = true
	}
	if cf.webhookURL != "" {
		options["callback_url"] = cf.webhookURL
		if len(cf.webhookEvents) > 0 {
			options["callback_events"] = cf.webhookEvents
		}
		if len(cf.webhookHeaders) > 0 {
			headers := map[string]string{}
			for _, h := range cf.webhookHeaders {
				k, v, ok := strings.Cut(h, "=")
				if !ok {
					return nil, fmt.Errorf("bad --webhook-header %q, expected key=value", h)
				}
				headers[k] = v
			}
			options["callback_headers"] = headers
		}
	}
	if err := mergeOptionsJSON(options, cf.optionsJSON); err != nil {
		return nil, err
	}
	return options, nil
}

// runAsyncSingle converts one file through the Files API (/files/v1). A local file is uploaded, a
// URL or bucket URI is submitted by reference; then it polls the file's status and downloads the
// requested format.
func runAsyncSingle(cmd *cobra.Command, client *scsapi.Client, cf *convertFlags, options map[string]any, src, dst string) error {
	if isExistingDir(src) {
		return fmt.Errorf("--async converts a single file; use `mpx scs jobs` for a folder or batch")
	}
	format := scsapi.FormatOfFile(dst)
	if format == "" {
		return fmt.Errorf("cannot tell the output format from %q; end it with a known extension such as .mmd, .docx", dst)
	}
	if field := scsapi.ConversionFormatsField([]string{format}); len(field) > 0 {
		options["conversion_formats"] = field
	}
	if cf.destination != "" {
		options["destination_uri"] = cf.destination
	}
	fileID, err := submitAsync(cmd, client, options, src)
	if err != nil {
		return err
	}
	if err := pollFile(cmd, client, fileID); err != nil {
		return err
	}
	out := strings.TrimSuffix(dst, "."+format)
	target := out + "." + format
	if err := downloadFile(cmd, client, fileID, format, target); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), target)
	return nil
}

func submitAsync(cmd *cobra.Command, client *scsapi.Client, options map[string]any, src string) (string, error) {
	var raw []byte
	var err error
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "s3://") || strings.HasPrefix(src, "gs://") {
		body := map[string]any{"source_uri": src}
		for k, v := range options {
			body[k] = v
		}
		raw, err = client.FilesSubmitURI(cmd.Context(), body)
	} else {
		f, openErr := os.Open(src)
		if openErr != nil {
			return "", openErr
		}
		raw, err = client.FilesSubmitUpload(cmd.Context(), f, filepath.Base(src), options)
		f.Close()
	}
	if err != nil {
		return "", err
	}
	var out struct {
		FileID string `json:"file_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.FileID == "" {
		return "", fmt.Errorf("unexpected submit response: %s", string(raw))
	}
	return out.FileID, nil
}

func pollFile(cmd *cobra.Command, client *scsapi.Client, fileID string) error {
	delay := time.Second
	for {
		raw, err := client.FilesStatus(cmd.Context(), fileID)
		if err != nil {
			return err
		}
		var s struct {
			Status    string `json:"status"`
			Error     string `json:"error"`
			ErrorInfo *struct {
				Message string `json:"message"`
			} `json:"error_info"`
		}
		json.Unmarshal(raw, &s)
		if s.Status == "error" {
			if s.ErrorInfo != nil && s.ErrorInfo.Message != "" {
				return fmt.Errorf("processing failed: %s", s.ErrorInfo.Message)
			}
			return fmt.Errorf("processing failed: %s", s.Error)
		}
		if s.Status == "completed" {
			return nil
		}
		if err := sleepCtx(cmd.Context(), delay); err != nil {
			return err
		}
		if delay < 10*time.Second {
			delay *= 2
		}
	}
}

func downloadFile(cmd *cobra.Command, client *scsapi.Client, fileID, ext, target string) error {
	part := target + ".part"
	for {
		f, err := os.Create(part)
		if err != nil {
			return err
		}
		pending, retryAfter, err := client.FilesDownload(cmd.Context(), fileID, ext, f)
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
			if err := sleepCtx(cmd.Context(), retryAfter); err != nil {
				return err
			}
			continue
		}
		return os.Rename(part, target)
	}
}

func runSingle(cmd *cobra.Command, client *scsapi.Client, cf *convertFlags, options map[string]any, src, dst string) error {
	format := scsapi.FormatOfFile(dst)
	if format == "" {
		return fmt.Errorf("cannot tell the output format from %q; end it with a known extension such as .mmd, .docx, .tex.zip, .html", dst)
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(src), "."))
	item := batch.Item{
		Key:        filepath.Base(src),
		Path:       src,
		Ext:        ext,
		Formats:    []string{format},
		OutputBase: strings.TrimSuffix(dst, "."+format),
	}
	engine := batch.NewEngine(client, options)
	outputs, err := engine.Convert(cmd.Context(), item)
	if err != nil {
		return err
	}
	for _, o := range outputs {
		fmt.Fprintln(cmd.OutOrStdout(), o)
	}
	return nil
}

func runFolder(cmd *cobra.Command, g *cli.Global, client *scsapi.Client, cf *convertFlags, options map[string]any, srcDir, dstDir string) error {
	m, err := batch.ParseMap(cf.mapValue)
	if err != nil {
		return err
	}
	items, err := batch.Discover(srcDir, dstDir, m)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "no matching documents found")
		return nil
	}
	root, err := filepath.Abs(srcDir)
	if err != nil {
		return err
	}
	report, runID, err := batch.NewReport(root, items, m)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "converting %d files (run %s)\n", len(items), runID)
	printer := progress.New(cmd.OutOrStdout())
	engine := batch.NewEngine(client, options)
	failures := engine.Run(cmd.Context(), items, cf.concurrency, printer, report)
	fmt.Fprintf(cmd.ErrOrStderr(), "%d of %d converted, %d failed; report %s\n", len(items)-failures, len(items), failures, report.Path())
	if failures > 0 {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("%d file(s) failed", failures)}
	}
	return nil
}

func isExistingDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
