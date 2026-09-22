// Command mpx is the Mathpix command-line interface. It is organized as `mpx <service> <command>`,
// the way the AWS CLI is: each Mathpix product is a service, and its operations are the commands
// under it. Today the only service is `scs`, the Mathpix OCR / document API on api.mathpix.com.
package main

import (
	"os"

	"github.com/mathpix/mathpix-cli/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
