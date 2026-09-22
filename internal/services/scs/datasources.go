package scs

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newDataSourcesCmd(flags *cli.Flags) *cobra.Command {
	ds := &cobra.Command{
		Use:   "data-sources",
		Short: "Register buckets so the Files API can read/write them (/files/v1/data-sources)",
	}
	ds.AddCommand(
		dataSourcesIdentities(flags),
		dataSourcesRegister(flags),
		dataSourcesList(flags),
		dataSourcesTest(flags),
		dataSourcesDelete(flags),
	)
	return ds
}

func dataSourcesIdentities(flags *cli.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "identities",
		Short: "Show Mathpix's grant identities and your external_id, to set up a bucket's trust policy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			raw, err := client.OnboardingIdentities(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
}

func dataSourcesRegister(flags *cli.Flags) *cobra.Command {
	var configJSON string
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register a bucket/container as a data source",
		Long: `Register a bucket. Pass the data source definition as JSON (fields depend on the provider,
e.g. bucket, region, role_arn for S3):

  mpx scs data-sources register --config '{"provider":"s3","bucket":"acme-docs","region":"us-east-1","role_arn":"arn:aws:iam::..."}'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if err := mergeOptionsJSON(body, configJSON); err != nil {
				return err
			}
			if len(body) == 0 {
				return fmt.Errorf("pass the data source definition with --config '{...}'")
			}
			raw, err := client.DataSourceCreate(cmd.Context(), body)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().StringVar(&configJSON, "config", "", "data source definition as a JSON object")
	return cmd
}

func dataSourcesList(flags *cli.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered data sources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			raw, err := client.DataSourceList(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
}

func dataSourcesTest(flags *cli.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "test ID",
		Short: "Verify read/write access to a registered data source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			raw, err := client.DataSourceTest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
}

func dataSourcesDelete(flags *cli.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete ID",
		Short: "Remove a data source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			raw, err := client.DataSourceDelete(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
}
