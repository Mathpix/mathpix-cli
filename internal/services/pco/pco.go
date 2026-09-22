// Package pco is the `mpx pco` service: a Mathpix Private Cloud OCR deployment. It speaks the same
// /v3 document API as scs plus the deployment-only /pco/v1 endpoints (jobs over cloud folders,
// status, usage, license). The client is the same one the standalone pco tool uses, so behavior
// matches a deployment exactly. Transport (endpoint, bearer token, mTLS) is set with this service's
// own flags, since a deployment authenticates differently from the hosted API.
package pco

import (
	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/pcoapi"
)

// transport holds the pco-only connection flags, layered on top of the global app_id/app_key.
type transport struct {
	token      string
	caCert     string
	clientCert string
	clientKey  string
	insecure   bool
}

// Command builds the `mpx pco` subtree.
func Command(flags *cli.Flags) *cobra.Command {
	t := &transport{}
	pco := &cobra.Command{
		Use:   "pco",
		Short: "Mathpix Private Cloud OCR (a deployment in your own infrastructure)",
		Long: `pco drives a Mathpix Private Cloud OCR deployment. It speaks the same document API as scs, plus
the deployment-only endpoints for jobs over a cloud folder, status, and usage.

  mpx pco --endpoint http://pco.internal:8080 convert paper.pdf paper.mmd
  mpx pco --endpoint http://pco.internal:8080 status
  mpx pco --endpoint http://pco.internal:8080 jobs list

Point --endpoint at your deployment. A deployment usually needs no credentials of its own; pass
--token or the client-certificate flags if your ingress requires them.`,
	}
	pco.PersistentFlags().StringVar(&t.token, "token", "", "bearer token your ingress expects, sent as Authorization")
	pco.PersistentFlags().StringVar(&t.caCert, "ca-cert", "", "PEM file with the CA that signed the deployment certificate")
	pco.PersistentFlags().StringVar(&t.clientCert, "client-cert", "", "PEM client certificate for mTLS")
	pco.PersistentFlags().StringVar(&t.clientKey, "client-key", "", "PEM client key for mTLS")
	pco.PersistentFlags().BoolVar(&t.insecure, "insecure", false, "skip TLS certificate verification")
	pco.AddCommand(
		newConvertCmd(flags, t),
		newTextCmd(flags, t),
		newJobsCmd(flags, t),
		newStatusCmd(flags, t),
		newUsageCmd(flags, t),
	)
	return pco
}

// client resolves the global config and builds a pcoapi client from it plus the pco transport flags.
func client(cmd *cobra.Command, flags *cli.Flags, t *transport) (*cli.Global, *pcoapi.Client, error) {
	g, err := cli.Resolve(*flags, cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return nil, nil, err
	}
	c, err := pcoapi.New(pcoapi.Options{
		Endpoint:   g.Endpoint,
		AppID:      g.AppID,
		AppKey:     g.AppKey,
		Token:      t.token,
		CACert:     t.caCert,
		ClientCert: t.clientCert,
		ClientKey:  t.clientKey,
		Insecure:   t.insecure,
	})
	if err != nil {
		return nil, nil, err
	}
	return g, c, nil
}
