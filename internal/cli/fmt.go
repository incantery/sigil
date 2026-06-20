package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/incantery/sigil/pkg/lang/format"
	"github.com/incantery/sigil/pkg/lang/parser"
)

var fmtWrite bool

var fmtCmd = &cobra.Command{
	Use:   "fmt <file.sigil>",
	Short: "Print the canonical formatting of a Sigil file",
	Long: `Parses a .sigil file and re-emits it in canonical form: fixed
2-space indent, kwargs sorted alphabetically, handlers sorted by event
name, compound assignments (count = count + 1) re-sugared back to (count
+= 1). Use --write to rewrite the file in place.

Caveat: comments are not preserved at v0 — the parser drops them and the
formatter has nothing to put back. Files without comments round-trip
faithfully.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		root, err := parser.Parse(string(src))
		if err != nil {
			// Parse errors prevent reliable formatting — surface them and
			// exit nonzero (silently — we already printed via stderr).
			for _, d := range extractDiagnostics(err, path) {
				if d.File == "" {
					d.File = path
				}
				fmt.Fprintln(os.Stderr, d.Error())
			}
			return ErrSilent
		}

		formatted := format.Source(root)

		if fmtWrite {
			if err := os.WriteFile(path, []byte(formatted), 0o644); err != nil {
				return err
			}
			return nil
		}
		fmt.Print(formatted)
		return nil
	},
}

func init() {
	fmtCmd.Flags().BoolVarP(&fmtWrite, "write", "w", false,
		"rewrite the file in place")
}
