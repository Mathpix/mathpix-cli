package scs

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newDownloadCmd(flags *cli.Flags) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "download PDF_ID FORMAT",
		Short: "Download one output format of a document (GET /v3/pdf/{id}.{ext})",
		Long: `Download a single format for a document already submitted. If the format is still converting the
command waits and retries until it is ready.

  mpx scs download PDF_ID mmd
  mpx scs download PDF_ID docx -o paper.docx`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			id, format := args[0], args[1]
			target := out
			if target == "" {
				target = id + "." + format
			}
			for {
				f, err := os.Create(target + ".part")
				if err != nil {
					return err
				}
				pending, retryAfter, err := client.Download(cmd.Context(), id, format, f)
				f.Close()
				if err != nil {
					os.Remove(target + ".part")
					return err
				}
				if pending {
					os.Remove(target + ".part")
					if retryAfter > 10*time.Second {
						retryAfter = 10 * time.Second
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "still converting, retrying in %s\n", retryAfter)
					time.Sleep(retryAfter)
					continue
				}
				if err := os.Rename(target+".part", target); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), target)
				return nil
			}
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "output path (default PDF_ID.FORMAT)")
	return cmd
}
