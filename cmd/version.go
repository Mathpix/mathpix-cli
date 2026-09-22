package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVersionCmd prints the build version, commit and date stamped in by the release build.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the mpx version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "mpx %s (commit %s, built %s)\n", Version, Commit, Date)
			return nil
		},
	}
}
