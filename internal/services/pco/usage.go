package pco

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newUsageCmd(flags *cli.Flags, t *transport) *cobra.Command {
	var (
		from   string
		to     string
		export bool
	)
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Pages processed per day, the numbers an invoice is based on (GET /pco/v1/usage)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, c, err := client(cmd, flags, t)
			if err != nil {
				return err
			}
			var raw []byte
			if export {
				raw, err = c.UsageExport(cmd.Context(), from, to)
			} else {
				raw, err = c.Usage(cmd.Context(), from, to)
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "start date, YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "end date, YYYY-MM-DD")
	cmd.Flags().BoolVar(&export, "export", false, "the signed statement an airgapped deployment sends to Mathpix (GET /pco/v1/usage/export)")
	return cmd
}
