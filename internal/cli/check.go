package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/incantery/sigil/internal/load"
)

func newCheckCmd() *cobra.Command {
	var (
		root   string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "check ENTRY.sigil",
		Short: "Type-check a sigil module without bundling or running it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry := args[0]
			if _, err := load.Load(entry, load.Options{Root: root}); err != nil {
				return reportCheckError(cmd, entry, asJSON, err)
			}
			return reportCheckOK(cmd, entry, asJSON)
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "module root directory (where std/ lives)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit result as JSON to stdout")
	return cmd
}

func reportCheckOK(cmd *cobra.Command, entry string, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"ok":   true,
			"file": entry,
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "ok  %s\n", entry)
	return nil
}

func reportCheckError(cmd *cobra.Command, entry string, asJSON bool, err error) error {
	if asJSON {
		_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"ok":    false,
			"file":  entry,
			"error": err.Error(),
		})
		return ErrSilent
	}
	fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
	return ErrSilent
}
