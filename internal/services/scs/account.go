package scs

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func newAppTokenCmd(flags *cli.Flags) *cobra.Command {
	var (
		expires int
		strokes bool
	)
	cmd := &cobra.Command{
		Use:   "app-token",
		Short: "Mint a short-lived app_token for direct client-side image OCR (POST /v3/app-tokens)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if expires > 0 {
				body["expires"] = expires
			}
			if strokes {
				body["include_strokes_session_id"] = true
			}
			raw, err := client.CreateAppToken(cmd.Context(), body)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().IntVar(&expires, "expires", 0, "token lifetime in seconds (30-43200; capped to 300 with --strokes)")
	cmd.Flags().BoolVar(&strokes, "strokes", false, "also return a strokes_session_id for live digital-ink pricing")
	return cmd
}

func newResultsCmd(flags *cli.Flags) *cobra.Command {
	var (
		pdf      bool
		page     int
		perPage  int
		fromDate string
		toDate   string
		appID    string
	)
	cmd := &cobra.Command{
		Use:   "results",
		Short: "List past OCR results (GET /v3/ocr-results, or /v3/pdf-results with --pdf)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			query := url.Values{}
			if page > 0 {
				query.Set("page", strconv.Itoa(page))
			}
			if perPage > 0 {
				query.Set("per_page", strconv.Itoa(perPage))
			}
			if fromDate != "" {
				query.Set("from_date", fromDate)
			}
			if toDate != "" {
				query.Set("to_date", toDate)
			}
			if appID != "" {
				query.Set("app_id", appID)
			}
			var raw []byte
			if pdf {
				raw, err = client.GetPDFResults(cmd.Context(), query)
			} else {
				raw, err = client.GetOCRResults(cmd.Context(), query)
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().BoolVar(&pdf, "pdf", false, "list past document (v3/pdf) results instead of image results")
	cmd.Flags().IntVar(&page, "page", 0, "page number")
	cmd.Flags().IntVar(&perPage, "per-page", 0, "results per page")
	cmd.Flags().StringVar(&fromDate, "from-date", "", "inclusive start, ISO datetime")
	cmd.Flags().StringVar(&toDate, "to-date", "", "exclusive end, ISO datetime")
	cmd.Flags().StringVar(&appID, "app-id-filter", "", "filter to one app_id under the group")
	return cmd
}

func newUsageCmd(flags *cli.Flags) *cobra.Command {
	var (
		fromDate string
		toDate   string
		timespan string
		groupBy  []string
	)
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Aggregated API usage for billing reconciliation (GET /v3/ocr-usage)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, err := resolve(cmd, flags)
			if err != nil {
				return err
			}
			query := url.Values{}
			if fromDate != "" {
				query.Set("from_date", fromDate)
			}
			if toDate != "" {
				query.Set("to_date", toDate)
			}
			if timespan != "" {
				query.Set("timespan", timespan)
			}
			for _, g := range groupBy {
				query.Add("group_by", g)
			}
			raw, err := client.GetOCRUsage(cmd.Context(), query)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return nil
		},
	}
	cmd.Flags().StringVar(&fromDate, "from-date", "", "inclusive start, ISO datetime")
	cmd.Flags().StringVar(&toDate, "to-date", "", "exclusive end, ISO datetime")
	cmd.Flags().StringVar(&timespan, "timespan", "", "bucket size: hour, day, month, or year")
	cmd.Flags().StringArrayVar(&groupBy, "group-by", nil, "group by usage_type, request_args_hash, or app_id (repeatable)")
	return cmd
}
