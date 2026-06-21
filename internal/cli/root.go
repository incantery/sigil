// Package cli implements the sigil command-line interface: a cobra command tree
// over the kernel (internal/load + internal/emit). Subcommands live in sibling
// files; cmd/sigil is a thin binary wrapper around Execute.
package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// Version is overridden at build time via
// -ldflags "-X github.com/incantery/sigil/internal/cli.Version=…".
var Version = "0.0.1-dev"

// ErrSilent signals that a subcommand has already printed its own error output
// (e.g. `sigil check --json`). Returning it yields a nonzero exit without main's
// default stderr message.
var ErrSilent = errors.New("silent")

// newRootCmd builds the sigil command tree. Using a constructor instead of a
// package-global command gives every invocation — and every test — fresh flag
// state.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "sigil",
		Short:         "The sigil frontend-web language toolchain",
		Long:          "sigil compiles a typed reactive UI language to a single npm-free JS bundle.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newBuildCmd())
	root.AddCommand(newServeCmd())
	return root
}

// Execute runs the sigil command tree. The binary wrapper surfaces the error.
func Execute() error {
	return newRootCmd().Execute()
}
