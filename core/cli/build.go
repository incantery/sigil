package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
	var (
		root   string
		out    string
		asHTML bool
	)
	cmd := &cobra.Command{
		Use:   "build ENTRY.mako",
		Short: "Compile a mako module to a JS bundle (or a full HTML page)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry := args[0]
			js, err := bundle(entry, root)
			if err != nil {
				return err
			}
			output := js
			if asHTML {
				output = htmlPage(entry, js)
			}
			if out == "" {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), output)
				return err
			}
			return os.WriteFile(out, []byte(output), 0o644)
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "module root directory (where std/ lives)")
	cmd.Flags().StringVarP(&out, "out", "o", "", "write output to FILE instead of stdout")
	cmd.Flags().BoolVar(&asHTML, "html", false, "wrap the bundle in a full HTML page")
	return cmd
}
