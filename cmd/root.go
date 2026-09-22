// Package cmd assembles the mpx command tree. The root owns the persistent flags every service
// shares (profile and credentials); each service is a subcommand group registered in NewRootCmd,
// so adding a product later is one AddCommand line, not a change to the root.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/services/scs"
)

// Build information, set by the linker at release time (see .goreleaser.yaml).
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Execute runs the tree and returns the process exit code.
func Execute() int {
	root := NewRootCmd()
	if err := root.Execute(); err != nil {
		var exit *cli.ExitError
		if errors.As(err, &exit) {
			if exit.Message != "" {
				fmt.Fprintln(os.Stderr, "mpx:", exit.Message)
			}
			return exit.Code
		}
		fmt.Fprintln(os.Stderr, "mpx:", err)
		return 1
	}
	return 0
}

// NewRootCmd builds the command tree. Tests construct it with their own args and output.
func NewRootCmd() *cobra.Command {
	flags := &cli.Flags{}
	root := &cobra.Command{
		Use:   "mpx",
		Short: "The Mathpix command-line interface",
		Long: `mpx is the Mathpix command-line interface, organized as ` + "`mpx <service> <command>`" + ` the way the
AWS CLI is. Each Mathpix product is a service; its operations are the commands under it.

  mpx scs convert paper.pdf paper.mmd     convert one document
  mpx scs convert ./in/ ./out/ --map pdf:docx    convert a folder

Credentials come from ` + "`mpx configure`" + `, the MATHPIX_APP_ID / MATHPIX_APP_KEY environment
variables, or --app-id / --app-key.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flags.Profile, "profile", "", "named profile to use (or MPX_PROFILE; default \"default\")")
	root.PersistentFlags().StringVar(&flags.AppID, "app-id", "", "Mathpix app_id (or MATHPIX_APP_ID)")
	root.PersistentFlags().StringVar(&flags.AppKey, "app-key", "", "Mathpix app_key (or MATHPIX_APP_KEY; prefer the variable over shell history)")
	root.PersistentFlags().StringVar(&flags.Endpoint, "endpoint", "", "API base URL (or MPX_ENDPOINT; default "+cli.DefaultEndpoint+")")
	root.PersistentFlags().StringVar(&flags.Output, "output", "", "output format for status: text or json (or MPX_OUTPUT)")

	// Services. Add a product here; nothing else in the root changes.
	root.AddCommand(scs.Command(flags))

	root.AddCommand(newConfigureCmd(flags), newVersionCmd())
	return root
}
