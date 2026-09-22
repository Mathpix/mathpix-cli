// Package cli holds the state shared by every service command: the resolved credentials, endpoint
// and output format for the selected profile. Resolve applies the standard precedence, flag first,
// then environment, then the profile files, so a service command receives one settled Global and
// never re-reads configuration itself.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/mathpix/mathpix-cli/internal/config"
)

// DefaultEndpoint is the hosted Mathpix API. A profile or flag can point at another host, e.g. the
// EU endpoint eu.api.mathpix.com or a private deployment.
const DefaultEndpoint = "https://api.mathpix.com"

const (
	VerbosityNormal = "normal"
	VerbosityQuiet  = "quiet"
)

// ExitError carries a specific process exit code out of a command that ran to completion, such as a
// folder convert where some files failed. The root maps it to the process status.
type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string { return e.Message }

// Flags are the persistent flags declared on the root command, before resolution.
type Flags struct {
	Profile   string
	AppID     string
	AppKey    string
	Endpoint  string
	Output    string
	Verbosity string
	Quiet     bool
}

// Global is the resolved configuration handed to service commands, plus the streams to write to.
type Global struct {
	Profile   string
	AppID     string
	AppKey    string
	Endpoint  string
	Output    string
	Verbosity string

	Out io.Writer
	Err io.Writer
}

// Resolve merges the flags, the environment and the selected profile's files into one Global.
// For every field the first non-empty of flag, environment, file wins; endpoint falls back to the
// hosted API.
func Resolve(f Flags, out, errOut io.Writer) (*Global, error) {
	profile := first(f.Profile, os.Getenv("MPX_PROFILE"), config.DefaultProfile)
	p, err := config.Load(profile)
	if err != nil {
		return nil, err
	}
	verbosity := first(f.Verbosity, os.Getenv("MPX_VERBOSITY"), p.Setting(config.KeyVerbosity), VerbosityNormal)
	if f.Quiet {
		verbosity = VerbosityQuiet
	}
	g := &Global{
		Profile:   profile,
		AppID:     first(f.AppID, os.Getenv("MATHPIX_APP_ID"), p.Cred(config.KeyAppID)),
		AppKey:    first(f.AppKey, os.Getenv("MATHPIX_APP_KEY"), p.Cred(config.KeyAppKey)),
		Endpoint:  first(f.Endpoint, os.Getenv("MPX_ENDPOINT"), p.Setting(config.KeyEndpoint), DefaultEndpoint),
		Output:    first(f.Output, os.Getenv("MPX_OUTPUT"), p.Setting(config.KeyOutput), "text"),
		Verbosity: verbosity,
		Out:       out,
		Err:       errOut,
	}
	return g, nil
}

func (g *Global) ShowProgress() bool {
	return g.Verbosity != VerbosityQuiet && isTerminal(g.Err)
}

// RequireCredentials returns an actionable error when app_id/app_key are missing, naming every way
// to supply them.
func (g *Global) RequireCredentials() error {
	if g.AppID != "" && g.AppKey != "" {
		return nil
	}
	return fmt.Errorf("no API credentials for profile %q; run `mpx configure`, set MATHPIX_APP_ID and MATHPIX_APP_KEY, or pass --app-id/--app-key", g.Profile)
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
