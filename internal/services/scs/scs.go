// Package scs is the `mpx scs` service: the Mathpix OCR / document API. Its commands wrap the v3
// endpoints: convert a document, a folder, or an image; batch jobs; status; downloads; data sources;
// webhooks; app tokens; results; usage. Every command resolves the shared credentials through
// cli.Resolve and builds a scsapi.Client, so the service holds no configuration state of its own.
package scs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/scsapi"
)

// splitCSV splits a comma-separated flag value into trimmed, non-empty items.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Command builds the `mpx scs` subtree. flags is the shared pointer populated by the root's
// persistent flags before any RunE runs.
func Command(flags *cli.Flags) *cobra.Command {
	scs := &cobra.Command{
		Use:   "scs",
		Short: "Mathpix OCR and document conversion (the v3 API)",
		Long: `scs is the Mathpix OCR / document API on api.mathpix.com. Convert a document or a whole folder,
and manage the documents you have submitted.

  mpx scs convert paper.pdf paper.mmd
  mpx scs convert equation.png equation.mmd
  mpx scs convert ./in/ ./out/ --map pdf:docx,md:docx
  mpx scs get PDF_ID
  mpx scs download PDF_ID docx -o paper.docx`,
	}
	scs.AddCommand(
		newConvertCmd(flags),
		newGetCmd(flags),
		newDownloadCmd(flags),
		newDeleteCmd(flags),
		newJobsCmd(flags),
		newDataSourcesCmd(flags),
		newAppTokenCmd(flags),
		newResultsCmd(flags),
		newUsageCmd(flags),
		newWebhooksCmd(flags),
	)
	return scs
}

// resolve builds the resolved Global and a client from it, failing early when credentials are missing.
func resolve(cmd *cobra.Command, flags *cli.Flags) (*cli.Global, *scsapi.Client, error) {
	g, err := cli.Resolve(*flags, cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return nil, nil, err
	}
	if err := g.RequireCredentials(); err != nil {
		return nil, nil, err
	}
	client := scsapi.New(scsapi.Options{Endpoint: g.Endpoint, AppID: g.AppID, AppKey: g.AppKey, UserAgent: userAgent()})
	return g, client, nil
}

var version = "dev"

func userAgent() string { return "mpx/" + version }

// mergeOptionsJSON merges a raw --options-json string into an options map, so any API option is
// reachable even when the CLI has no dedicated flag for it. Explicit flags set earlier win unless the
// JSON overrides them, which is the documented escape hatch.
func mergeOptionsJSON(options map[string]any, raw string) error {
	if raw == "" {
		return nil
	}
	var extra map[string]any
	if err := json.Unmarshal([]byte(raw), &extra); err != nil {
		return fmt.Errorf("--options-json is not valid JSON: %w", err)
	}
	for k, v := range extra {
		options[k] = v
	}
	return nil
}
