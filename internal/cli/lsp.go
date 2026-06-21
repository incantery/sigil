package cli

import (
	"github.com/incantery/sigil/internal/lsp"
	"github.com/spf13/cobra"
)

func newLspCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lsp",
		Short: "Run the sigil language server (LSP) over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return lsp.NewServer(cmd.InOrStdin(), cmd.OutOrStdout()).Run()
		},
	}
}
