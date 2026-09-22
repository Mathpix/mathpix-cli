// Package scs is the `mpx scs` service: the Mathpix OCR / document API. Its commands wrap the v3
// endpoints (convert a document or a folder, OCR one image, check status, download a format, delete
// a document). Every command resolves the shared credentials through cli.Resolve and builds a
// scsapi.Client, so the service holds no configuration state of its own.
package scs

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/scsapi"
)

// Command builds the `mpx scs` subtree. flags is the shared pointer populated by the root's
// persistent flags before any RunE runs.
func Command(flags *cli.Flags) *cobra.Command {
	scs := &cobra.Command{
		Use:   "scs",
		Short: "Mathpix OCR and document conversion (the v3 API)",
		Long: `scs is the Mathpix OCR / document API on api.mathpix.com. Convert a document or a whole folder,
OCR a single image, and manage the documents you have submitted.

  mpx scs convert paper.pdf paper.mmd
  mpx scs convert ./in/ ./out/ --map pdf:docx,md:docx
  mpx scs text equation.png
  mpx scs get PDF_ID
  mpx scs download PDF_ID docx -o paper.docx`,
	}
	scs.AddCommand(
		newConvertCmd(flags),
		newTextCmd(flags),
		newGetCmd(flags),
		newDownloadCmd(flags),
		newDeleteCmd(flags),
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
