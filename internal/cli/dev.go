package cli

import (
	"github.com/incantery/sigil/internal/devserver"
	"github.com/spf13/cobra"
)

func newDevCmd() *cobra.Command {
	var (
		root string
		port string
	)
	cmd := &cobra.Command{
		Use:   "dev ENTRY.sigil",
		Short: "Serve a sigil module with hot module replacement (state-preserving)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry := args[0]
			// Fail fast on a broken entry before binding a port.
			if _, err := bundleDev(entry, root); err != nil {
				return err
			}
			srv := devserver.New(entry, root, bundleDev)
			return srv.ListenAndServe(":" + port)
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "module root directory (where std/ lives)")
	cmd.Flags().StringVar(&port, "port", "8099", "port to serve on")
	return cmd
}
