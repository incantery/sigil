package cli

import (
	"github.com/spf13/cobra"

	"github.com/incantery/sigil/internal/testrun"
)

func newTestCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "test [PATH]",
		Short: "Compile and run *_test.sigil files in goja",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			ok, err := testrun.Run(cmd.OutOrStdout(), path, root)
			if err != nil {
				return err
			}
			if !ok {
				return ErrSilent // already reported; nonzero exit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "module root directory (where std/ lives)")
	return cmd
}
