// Command sigil is the sigil toolchain CLI. The command surface lives in
// internal/cli so it can be tested independently of this binary wrapper.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/incantery/sigil/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// Subcommands that already printed their own diagnostic (e.g.
		// `sigil check --json`) return cli.ErrSilent — exit nonzero without a
		// duplicate stderr message.
		if !errors.Is(err, cli.ErrSilent) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
