package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/config"
)

// newConfigureCmd implements `mpx configure`, the interactive setup: it prompts for
// the credentials and settings of a profile and writes them to ~/.mpx/credentials and ~/.mpx/config.
// Pressing Enter keeps the current value. Secrets go only into the credentials file (0600).
func newConfigureCmd(flags *cli.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "configure",
		Short: "Store credentials and settings for a profile",
		Long: `Prompt for the app_id, app_key, endpoint and output format of a profile and save them under
~/.mpx (or $MPX_CONFIG_DIR). Credentials are written to the credentials file with 0600 permissions,
settings to the config file. Use --profile to configure a profile other than "default".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile := flags.Profile
			if profile == "" {
				profile = config.DefaultProfile
			}
			existing, err := config.Load(profile)
			if err != nil {
				return err
			}
			in := bufio.NewReader(cmd.InOrStdin())
			out := cmd.OutOrStdout()
			appID := prompt(in, out, "Mathpix app_id", existing.Cred(config.KeyAppID), false)
			appKey := prompt(in, out, "Mathpix app_key", existing.Cred(config.KeyAppKey), true)
			endpoint := prompt(in, out, "API endpoint", firstNonEmpty(existing.Setting(config.KeyEndpoint), cli.DefaultEndpoint), false)
			output := prompt(in, out, "Default output (text/json)", firstNonEmpty(existing.Setting(config.KeyOutput), "text"), false)
			credentials := map[string]string{config.KeyAppID: appID, config.KeyAppKey: appKey}
			settings := map[string]string{config.KeyEndpoint: endpoint, config.KeyOutput: output}
			if err := config.Save(profile, credentials, settings); err != nil {
				return err
			}
			dir, _ := config.Dir()
			fmt.Fprintf(out, "Saved profile %q to %s\n", profile, dir)
			return nil
		},
	}
}

// prompt shows the label with its current value masked when secret, reads one line, and returns the
// entered value or the current one when the line is blank.
func prompt(in *bufio.Reader, out interface{ Write([]byte) (int, error) }, label, current string, secret bool) string {
	shown := current
	if secret && current != "" {
		shown = mask(current)
	}
	if shown != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, shown)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return current
	}
	return line
}

func mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
