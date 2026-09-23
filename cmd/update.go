package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/selfupdate"
)

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update mpx to the latest release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return selfupdate.New(Version, cmd.OutOrStdout()).Update()
		},
	}
}

func newSelfUpdateWorkerCmd() *cobra.Command {
	return &cobra.Command{
		Use:    selfupdate.WorkerName,
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			selfupdate.New(Version, os.Stderr).RunWorker()
		},
	}
}
