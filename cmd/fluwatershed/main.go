// Command fluwatershed is the offline command line for environmental H5
// surveillance signal processing.
//
// The binary is a thin shell: it hands the arguments to the cli package and
// returns whatever exit code that package decides. Keeping main this small means
// the whole command surface is reachable from a test without spawning a process.
//
// FluWatershed is an independent study tool. It is not affiliated with, endorsed
// by, sponsored by or associated with any product, company, agency, university or
// organisation, it provides no public-health, clinical, veterinary or regulatory
// advice, and it must not be used to make real outbreak decisions.
package main

import (
	"os"

	"FluWatershed/internal/cli"
)

func main() {
	os.Exit(cli.Run(cli.Environment{Stdout: os.Stdout, Stderr: os.Stderr}, os.Args[1:]))
}
