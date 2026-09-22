package pco

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newJobsCmd(flags *cli.Flags, t *transport) *cobra.Command {
	jobs := &cobra.Command{
		Use:   "jobs",
		Short: "Manage server-side batch jobs over your bucket (/pco/v1/jobs)",
	}
	jobs.AddCommand(
		jobsList(flags, t),
		jobsGet(flags, t),
		jobsFiles(flags, t),
		jobsReport(flags, t),
		jobsRetry(flags, t),
		jobsCancel(flags, t),
	)
	return jobs
}

func jobsList(flags *cli.Flags, t *transport) *cobra.Command {
	var (
		status string
		cursor string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, c, err := client(cmd, flags, t)
			if err != nil {
				return err
			}
			page, err := c.ListJobs(cmd.Context(), status, cursor, limit)
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(page, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&cursor, "cursor", "", "page cursor from a previous list")
	cmd.Flags().IntVar(&limit, "limit", 0, "max jobs to return")
	return cmd
}

func jobsGet(flags *cli.Flags, t *transport) *cobra.Command {
	return &cobra.Command{
		Use:   "get JOB_ID",
		Short: "Show one job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, c, err := client(cmd, flags, t)
			if err != nil {
				return err
			}
			job, err := c.GetJob(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(job, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
}

func jobsFiles(flags *cli.Flags, t *transport) *cobra.Command {
	var (
		status string
		cursor string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "files JOB_ID",
		Short: "List the files in a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, c, err := client(cmd, flags, t)
			if err != nil {
				return err
			}
			page, err := c.ListFiles(cmd.Context(), args[0], status, cursor, limit)
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(page, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by file status (e.g. error)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "page cursor")
	cmd.Flags().IntVar(&limit, "limit", 0, "max files to return")
	return cmd
}

func jobsReport(flags *cli.Flags, t *transport) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "report JOB_ID",
		Short: "Stream a job's per-file report (JSONL)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, c, err := client(cmd, flags, t)
			if err != nil {
				return err
			}
			return c.Report(cmd.Context(), args[0], status, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by file status")
	return cmd
}

func jobsRetry(flags *cli.Flags, t *transport) *cobra.Command {
	var errorIDs []string
	cmd := &cobra.Command{
		Use:   "retry JOB_ID",
		Short: "Re-enqueue a job's failed files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, c, err := client(cmd, flags, t)
			if err != nil {
				return err
			}
			job, err := c.RetryJob(cmd.Context(), args[0], errorIDs)
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(job, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&errorIDs, "error-id", nil, "only retry files that failed with this error id (repeatable)")
	return cmd
}

func jobsCancel(flags *cli.Flags, t *transport) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel JOB_ID",
		Short: "Cancel a running job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, c, err := client(cmd, flags, t)
			if err != nil {
				return err
			}
			job, err := c.CancelJob(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(job, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
}
