package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/incantery/sigil/pkg/ir"
	"github.com/incantery/sigil/pkg/lang/diag"
)

var checkJSON bool

var checkCmd = &cobra.Command{
	Use:   "check <file.sigil>",
	Short: "Parse and lower a Sigil file without running it",
	Long: `Validates a .sigil file: parse + lower with no server, no HTTP.
Useful in editor/AI loops where you want fast structural feedback without
the cost of starting a browser. Pass --json for machine-readable output
suitable for piping into jq or an LSP.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		doc, err := compileFile(path)
		if err != nil {
			return reportCheckError(err, path)
		}
		return reportCheckOK(doc, path)
	},
}

func reportCheckOK(doc ir.Document, path string) error {
	if checkJSON {
		out := map[string]any{
			"ok":   true,
			"file": path,
			"view": doc.Name,
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	if doc.Name != "" {
		fmt.Printf("ok  %s  (view %q)\n", path, doc.Name)
	} else {
		fmt.Printf("ok  %s\n", path)
	}
	return nil
}

func reportCheckError(err error, path string) error {
	diags := extractDiagnostics(err, path)

	if checkJSON {
		out := map[string]any{
			"ok":     false,
			"file":   path,
			"errors": diags,
		}
		_ = json.NewEncoder(os.Stdout).Encode(out)
		return ErrSilent
	}
	for _, d := range diags {
		fmt.Fprintln(os.Stderr, d.Error())
	}
	return ErrSilent
}

// extractDiagnostics pulls every *diag.Diagnostic out of an error chain,
// flattening a *diag.MultiError into its component diagnostics. Anything
// that isn't itself a Diagnostic gets wrapped in a synthetic stage="unknown"
// entry so the JSON shape stays uniform.
func extractDiagnostics(err error, path string) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	var multi *diag.MultiError
	if errors.As(err, &multi) {
		for _, d := range multi.Items {
			if d.File == "" {
				d.File = path
			}
			out = append(out, d)
		}
		return out
	}
	var d *diag.Diagnostic
	if errors.As(err, &d) {
		if d.File == "" {
			d.File = path
		}
		return []*diag.Diagnostic{d}
	}
	return []*diag.Diagnostic{{File: path, Message: err.Error()}}
}

func init() {
	checkCmd.Flags().BoolVar(&checkJSON, "json", false,
		"emit result as JSON to stdout (machine-readable)")
}
