// Package cli implements the mako command-line interface: a cobra command tree
// over the core kernel (core/load + core/emit). Subcommands live in sibling
// files; core/cmd/mako is a thin binary wrapper around Execute.
package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// Version is overridden at build time via
// -ldflags "-X github.com/incantery/mako/core/cli.Version=…".
var Version = "0.0.1-dev"

// ErrSilent signals that a subcommand has already printed its own error output
// (e.g. `mako check --json`). Returning it yields a nonzero exit without main's
// default stderr message.
var ErrSilent = errors.New("silent")

// newRootCmd builds the mako command tree. Using a constructor instead of a
// package-global command gives every invocation — and every test — fresh flag
// state.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "mako",
		Short:         "The mako frontend-web language toolchain",
		Long:          "mako compiles a typed reactive UI language to a single npm-free JS bundle.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newBuildCmd())
	root.AddCommand(newServeCmd())
	return root
}

// Execute runs the mako command tree. The binary wrapper surfaces the error.
func Execute() error {
	return newRootCmd().Execute()
}
