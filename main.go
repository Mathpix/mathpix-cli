// Command mpx is the Mathpix command-line interface. It is organized as `mpx <service> <command>`:
// each Mathpix product is a service, and its operations are the commands under it. The services
// today are `scs` (the Mathpix OCR / document API) and `pco` (a Private Cloud OCR deployment).
package main

import (
	"os"

	"github.com/mathpix/mathpix-cli/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
