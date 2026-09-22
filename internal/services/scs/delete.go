package scs

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newDeleteCmd(flags *cli.Flags) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete PDF_ID",
		Short: "Permanently delete a document's outputs and input (DELETE /v3/pdf/{id})",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			id := args[0]
			if !yes {
				fmt.Fprintf(cmd.OutOrStdout(), "Permanently delete %s and all its outputs? [y/N]: ", id)
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if strings.ToLower(strings.TrimSpace(line)) != "y" {
					fmt.Fprintln(cmd.ErrOrStderr(), "cancelled")
					return nil
				}
			}
			if _, err := client.Delete(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not prompt for confirmation")
	return cmd
}
