package scs

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newWebhooksCmd(flags *cli.Flags) *cobra.Command {
	wh := &cobra.Command{
		Use:   "webhooks",
		Short: "Manage the webhook signing secret and send test deliveries (/files/v1/webhook-config)",
		Long: `Webhooks notify your server when a document finishes. Set the callback per request with the
convert flags (--webhook-url, --webhook-event, --webhook-header). These commands manage the account's
signing secret you verify deliveries against, and send a test delivery.`,
	}
	wh.AddCommand(
		webhooksConfig(flags),
		webhooksRotate(flags),
		webhooksTest(flags),
	)
	return wh
}

func webhooksConfig(flags *cli.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Show (creating on first call) the webhook signing secret",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			raw, err := client.WebhookConfigGet(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
}

func webhooksRotate(flags *cli.Flags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rotate-secret",
		Short: "Rotate the webhook signing secret",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			raw, err := client.WebhookSecretRotate(cmd.Context(), force)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "rotate even if the secret was rotated recently")
	return cmd
}

func webhooksTest(flags *cli.Flags) *cobra.Command {
	var headers []string
	cmd := &cobra.Command{
		Use:   "test URL",
		Short: "Send one signed test notification to a URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			hdr := map[string]string{}
			for _, h := range headers {
				k, v, ok := strings.Cut(h, "=")
				if !ok {
					return fmt.Errorf("bad --header %q, expected key=value", h)
				}
				hdr[k] = v
			}
			raw, err := client.WebhookTest(cmd.Context(), args[0], hdr)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&headers, "header", nil, "header to send with the test delivery, key=value (repeatable)")
	return cmd
}
