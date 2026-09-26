package pco

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/scsmigrate"
)

func newMigrateSCSCmd() *cobra.Command {
	in := scsmigrate.Inputs{}
	cmd := &cobra.Command{
		Use:   "migrate-scs",
		Short: "Turn SCS classic option files into a job spec for 'mpx pco convert --job-spec'",
		Long: `migrate-scs is a pure translation, no network: it reads the conversion_options.json, ocr_options.json,
--ext-list and input folder an SCS classic batch used and prints the equivalent /pco/v1/jobs body.
Then: mpx pco convert --job-spec job.json --dry-run, and without --dry-run for the real job.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := scsmigrate.Translate(in)
			if err != nil {
				return err
			}
			for _, note := range result.Notes {
				fmt.Fprintln(os.Stderr, "note:", note)
			}
			encoded, err := json.MarshalIndent(result.Spec, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
			return nil
		},
	}
	cmd.Flags().StringVar(&in.ConversionOptionsPath, "conversion-options", "", "SCS classic conversion_options.json")
	cmd.Flags().StringVar(&in.OCROptionsPath, "ocr-options", "", "SCS classic ocr_options.json")
	cmd.Flags().StringVar(&in.ExtList, "ext-list", "", "SCS classic --ext_list, comma-separated")
	cmd.Flags().StringVar(&in.InputFolder, "input-folder", "", "the folder SCS classic published from, e.g. s3://bucket/folder/")
	cmd.Flags().StringVar(&in.OutputFolder, "output-folder", "", "output folder (default: the input folder)")
	cmd.Flags().IntVar(&in.MaxPDFPages, "max-pdf-pages", 0, "SCS classic --max_pdf_pages")
	cmd.Flags().IntVar(&in.PerPageTimeoutSeconds, "pdf-per-page-timeout", 0, "SCS classic --pdf_per_page_timeout")
	cmd.Flags().IntVar(&in.MaxRetries, "max-consumer-retries", 0, "SCS classic --max_consumer_retries")
	return cmd
}
