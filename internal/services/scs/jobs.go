package scs

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/scsapi"
)

func newJobsCmd(flags *cli.Flags) *cobra.Command {
	jobs := &cobra.Command{
		Use:   "jobs",
		Short: "Batch jobs over many documents (Files API, /files/v1/jobs)",
	}
	jobs.AddCommand(
		jobsCreate(flags),
		jobsList(flags),
		jobsGet(flags),
		jobsFiles(flags),
		jobsFinalize(flags),
	)
	return jobs
}

func jobsCreate(flags *cli.Flags) *cobra.Command {
	var (
		uris        []string
		jobID       string
		formatsCSV  string
		optionsJSON string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Submit a batch of documents by URI",
		Long: `Submit many documents in one call. Pass each remote document with --uri (repeatable); s3:// and
gs:// sources need a registered data source (see 'mpx scs data-sources').

  mpx scs jobs create --uri s3://bucket/a.pdf --uri s3://bucket/b.pdf --formats docx`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			if len(uris) == 0 {
				return fmt.Errorf("pass at least one --uri")
			}
			files := make([]map[string]any, len(uris))
			for i, u := range uris {
				files[i] = map[string]any{"source_uri": u}
			}
			body := map[string]any{"files": files}
			if jobID != "" {
				body["job_id"] = jobID
			}
			if formatsCSV != "" {
				body["conversion_formats"] = scsapi.ConversionFormatsField(splitCSV(formatsCSV))
			}
			if err := mergeOptionsJSON(body, optionsJSON); err != nil {
				return err
			}
			raw, err := client.FilesCreateJob(cmd.Context(), body)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&uris, "uri", nil, "a document's source URI (repeatable)")
	cmd.Flags().StringVar(&jobID, "job-id", "", "job id (server generates one if omitted)")
	cmd.Flags().StringVar(&formatsCSV, "formats", "", "conversion formats for every file, comma-separated")
	cmd.Flags().StringVar(&optionsJSON, "options-json", "", "extra API options as a JSON object")
	return cmd
}

func jobsList(flags *cli.Flags) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			query := url.Values{}
			if limit > 0 {
				query.Set("limit", strconv.Itoa(limit))
			}
			raw, err := client.FilesListJobs(cmd.Context(), query)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max jobs to return")
	return cmd
}

func jobsGet(flags *cli.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "get JOB_ID",
		Short: "Show a job's status and counters",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			raw, err := client.FilesGetJob(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
}

func jobsFiles(flags *cli.Flags) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "files JOB_ID",
		Short: "List the files in a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			query := url.Values{}
			if status != "" {
				query.Set("status", status)
			}
			raw, err := client.FilesJobFiles(cmd.Context(), args[0], query)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by file status (e.g. error)")
	return cmd
}

func jobsFinalize(flags *cli.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "finalize JOB_ID",
		Short: "Close a job to new files so job.completed can fire",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			raw, err := client.FilesFinalizeJob(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
}
