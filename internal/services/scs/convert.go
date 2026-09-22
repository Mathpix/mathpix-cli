package scs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/batch"
	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/progress"
	"github.com/mathpix/mathpix-cli/internal/scsapi"
)

type convertFlags struct {
	mapValue    string
	concurrency int
	pageRanges  string
	rmSpaces    bool
	eqTags      bool
	optionsJSON string
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
	if err := mergeOptionsJSON(options, cf.optionsJSON); err != nil {
		return nil, err
	}
	return options, nil
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
