package scs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newTextCmd(flags *cli.Flags) *cobra.Command {
	var (
		formats     string
		optionsJSON string
	)
	cmd := &cobra.Command{
		Use:   "text IMAGE",
		Short: "OCR a single image (POST /v3/text)",
		Long: `OCR one image synchronously and print the JSON result.

  mpx scs text equation.png
  mpx scs text page.jpg --formats text,data,latex_styled
  mpx scs text page.jpg --options-json '{"include_line_data": true}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			options := map[string]any{}
			if formats != "" {
				options["formats"] = splitCSV(formats)
			}
			if err := mergeOptionsJSON(options, optionsJSON); err != nil {
				return err
			}
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			raw, err := client.SubmitTextFile(cmd.Context(), f, filepath.Base(args[0]), options)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().StringVar(&formats, "formats", "", "result formats, comma-separated (e.g. text,data,latex_styled)")
	cmd.Flags().StringVar(&optionsJSON, "options-json", "", "extra API options as a JSON object")
	return cmd
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
