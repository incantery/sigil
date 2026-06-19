package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/incantery/mako/pkg/lang/lsp"
)

var lspVerbose bool

var lspCmd = &cobra.Command{
	Use:   "lsp",
	Short: "Run the Sigil language server over stdio",
	Long: `Speaks the Language Server Protocol on stdin/stdout. Wire it into any
LSP-capable editor to get compiler-accurate diagnostics (parse + lower,
the same pipeline as 'sigil check'), semantic-token syntax highlighting,
document symbols, go-to-definition, and hover.

Editor setup snippets live in editor/README.md.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if lspVerbose {
			return lsp.NewServer(os.Stdin, os.Stdout, os.Stderr, Version).Run()
		}
		return lsp.NewServer(os.Stdin, os.Stdout, nil, Version).Run()
	},
}

func init() {
	lspCmd.Flags().BoolVar(&lspVerbose, "verbose", false,
		"trace protocol activity to stderr")
}
