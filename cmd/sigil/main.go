// Sigil — entry point for the sigil CLI.
//
// The actual command surface lives in internal/cli so subcommands and the
// Bubble Tea help UI can be tested independently of the binary wrapper.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/incantery/sigil/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// Subcommands that have already printed their own diagnostic (e.g.
		// `sigil check --json`) return cli.ErrSilent — we want a nonzero
		// exit but not a duplicate stderr message.
		if !errors.Is(err, cli.ErrSilent) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
