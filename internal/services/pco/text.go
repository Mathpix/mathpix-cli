package pco

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newTextCmd(flags *cli.Flags, t *transport) *cobra.Command {
	var optionsJSON string
	cmd := &cobra.Command{
		Use:   "text IMAGE",
		Short: "OCR a single image (POST /v3/text)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, c, err := client(cmd, flags, t)
			if err != nil {
				return err
			}
			options := map[string]any{}
			if optionsJSON != "" {
				if err := json.Unmarshal([]byte(optionsJSON), &options); err != nil {
					return fmt.Errorf("--options-json is not valid JSON: %w", err)
				}
			}
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			result, err := c.TextImage(cmd.Context(), f, filepath.Base(args[0]), options)
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
	cmd.Flags().StringVar(&optionsJSON, "options-json", "", "extra API options as a JSON object")
	return cmd
}
