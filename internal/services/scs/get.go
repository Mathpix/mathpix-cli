package scs

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newGetCmd(flags *cli.Flags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get PDF_ID",
		Short: "Show a document's processing status (GET /v3/pdf/{id})",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			status, err := client.GetStatus(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if g.Output == "json" {
				data, _ := json.MarshalIndent(status, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "id       %s\n", status.PDFID)
			fmt.Fprintf(out, "status   %s\n", status.Status)
			fmt.Fprintf(out, "pages    %d/%d (%.0f%%)\n", status.NumPagesComplete, status.NumPages, status.PercentDone)
			for format, info := range status.ConversionStatus {
				fmt.Fprintf(out, "  %-16s %s\n", format, info.Status)
			}
			if status.ErrorInfo != nil {
				fmt.Fprintf(out, "error    %s: %s\n", status.ErrorInfo.ID, status.ErrorInfo.Message)
			}
			return nil
		},
	}
	return cmd
}
