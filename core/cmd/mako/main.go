// Command mako is the mako toolchain CLI. The command surface lives in
// core/cli so it can be tested independently of this binary wrapper.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/incantery/mako/core/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// Subcommands that already printed their own diagnostic (e.g.
		// `mako check --json`) return cli.ErrSilent — exit nonzero without a
		// duplicate stderr message.
		if !errors.Is(err, cli.ErrSilent) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
