package cli_test

import (
	"io"
	"testing"

	"github.com/mathpix/mathpix-cli/internal/cli"
)

func TestResolveVerbosity(t *testing.T) {
	t.Setenv("MPX_CONFIG_DIR", t.TempDir())
	t.Setenv("MPX_VERBOSITY", "")

	cases := []struct {
		name  string
		flags cli.Flags
		env   string
		want  string
	}{
		{"default", cli.Flags{}, "", cli.VerbosityNormal},
		{"env", cli.Flags{}, "quiet", "quiet"},
		{"flag beats env", cli.Flags{Verbosity: "normal"}, "quiet", "normal"},
		{"quiet overrides", cli.Flags{Verbosity: "normal", Quiet: true}, "", "quiet"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MPX_VERBOSITY", c.env)
			g, err := cli.Resolve(c.flags, io.Discard, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if g.Verbosity != c.want {
				t.Errorf("verbosity = %q, want %q", g.Verbosity, c.want)
			}
		})
	}
}

func TestShowProgressOffTerminalIsFalse(t *testing.T) {
	t.Setenv("MPX_CONFIG_DIR", t.TempDir())
	g, err := cli.Resolve(cli.Flags{}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if g.ShowProgress() {
		t.Error("ShowProgress() should be false when the error stream is not a terminal")
	}
}
