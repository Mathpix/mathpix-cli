package pco

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newStatusCmd(flags *cli.Flags, t *transport) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the deployment's versions, workers, license and metering (GET /pco/v1/status)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, c, err := client(cmd, flags, t)
			if err != nil {
				return err
			}
			status, err := c.Status(cmd.Context())
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(status, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
}
