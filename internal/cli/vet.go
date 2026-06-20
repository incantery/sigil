package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/incantery/sigil/pkg/lang/diag"
	"github.com/incantery/sigil/pkg/lang/parser"
	"github.com/incantery/sigil/pkg/vet"
)

var vetJSON bool

var vetCmd = &cobra.Command{
	Use:   "vet [paths...]",
	Short: "Static analysis across one or more Sigil files",
	Long: `Runs the parser + lower stages over each input path and reports any
findings. Path resolution mirrors Go:

  sigil vet              walks .sigil files in the current directory
  sigil vet .            same as above
  sigil vet ./...        recursive — every .sigil file under cwd
  sigil vet dir/...      recursive under dir/
  sigil vet a.sigil      specific file
  sigil vet a.sigil b.sigil  multiple files

Errors come from parse + lower. Warnings come from vet rules:
unused state cells, unused user-defined components. --json emits a
structured per-file record on stdout. Exit code is nonzero only on
errors; warnings do not fail the run.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := expandVetPaths(args)
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			fmt.Fprintln(os.Stderr, "sigil vet: no .sigil files found")
			return ErrSilent
		}

		results := make([]vetResult, 0, len(paths))
		anyErr := false
		for _, p := range paths {
			r := runVetOne(p)
			results = append(results, r)
			if len(r.Errors) > 0 {
				anyErr = true
			}
		}

		if vetJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(struct {
				Total   int         `json:"total"`
				Results []vetResult `json:"results"`
			}{
				Total:   len(results),
				Results: results,
			})
		} else {
			printVetText(results)
		}
		if anyErr {
			return ErrSilent
		}
		return nil
	},
}

// vetResult is one file's report. Errors fail the run; Warnings don't.
type vetResult struct {
	File     string             `json:"file"`
	Errors   []*diag.Diagnostic `json:"errors,omitempty"`
	Warnings []*diag.Diagnostic `json:"warnings,omitempty"`
}

// runVetOne parses + lowers a single file and runs vet rules on the
// resulting AST. Parse failure short-circuits the rule pass (no AST
// to vet). Lower failure still permits vet — the AST is intact even
// if semantics broke.
func runVetOne(path string) vetResult {
	r := vetResult{File: path}
	src, err := os.ReadFile(path)
	if err != nil {
		r.Errors = []*diag.Diagnostic{{File: path, Message: err.Error()}}
		return r
	}
	root, parseErr := parser.Parse(string(src))
	if parseErr != nil {
		r.Errors = append(r.Errors, extractDiagnostics(parseErr, path)...)
	}
	// Even if parse partially failed, the AST is populated (parser
	// pushes __error__ placeholders). Lower through the loader (not a
	// bare lower.Lower on the single file) so cross-package references
	// — dotted invocation heads, imported types/components — resolve;
	// otherwise vet would report spurious "unknown component" errors
	// on a program that compiles fine through check/run/serve.
	if root != nil {
		if _, lowerErr := compileFile(path); lowerErr != nil {
			r.Errors = append(r.Errors, extractDiagnostics(lowerErr, path)...)
		}
		for _, w := range vet.Run(root) {
			w.File = path
			r.Warnings = append(r.Warnings, w)
		}
	}
	return r
}

func printVetText(results []vetResult) {
	for _, r := range results {
		for _, d := range r.Errors {
			fmt.Fprintln(os.Stderr, d.Error())
		}
		for _, w := range r.Warnings {
			fmt.Fprintln(os.Stdout, w.Error())
		}
	}
	// Summary line — `n files checked, e errors, w warnings`.
	files := len(results)
	errCount, warnCount := 0, 0
	for _, r := range results {
		errCount += len(r.Errors)
		warnCount += len(r.Warnings)
	}
	if errCount == 0 && warnCount == 0 {
		fmt.Printf("ok  %d file(s) checked\n", files)
		return
	}
	fmt.Printf("%d file(s) checked: %d error(s), %d warning(s)\n",
		files, errCount, warnCount)
}

// expandVetPaths turns a slice of user-supplied path arguments into
// the concrete list of .sigil files to vet. Rules:
//
//   - No args: walks "." (non-recursive)
//   - "./..." or "<path>/...": recursive walk under that path
//   - directory: non-recursive contents
//   - file ending in .sigil: included as-is
//
// Sorted output for deterministic reporting.
func expandVetPaths(args []string) ([]string, error) {
	if len(args) == 0 {
		args = []string{"."}
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		out = append(out, p)
	}
	for _, raw := range args {
		recursive := false
		path := raw
		if strings.HasSuffix(path, "/...") {
			recursive = true
			path = strings.TrimSuffix(path, "/...")
			if path == "" || path == "." {
				path = "."
			}
		} else if path == "..." {
			recursive = true
			path = "."
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("sigil vet: %s: %w", raw, err)
		}
		if !info.IsDir() {
			if !strings.HasSuffix(path, ".sigil") {
				return nil, fmt.Errorf("sigil vet: %s: not a .sigil file", raw)
			}
			add(path)
			continue
		}
		if recursive {
			err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					// Skip hidden + node_modules-ish directories that
					// definitely won't have user .sigil source.
					name := d.Name()
					if p != path && (strings.HasPrefix(name, ".") || name == "node_modules") {
						return fs.SkipDir
					}
					return nil
				}
				if strings.HasSuffix(p, ".sigil") {
					add(p)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		// Non-recursive directory listing.
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sigil") {
				continue
			}
			add(filepath.Join(path, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func init() {
	vetCmd.Flags().BoolVar(&vetJSON, "json", false,
		"emit structured JSON on stdout")
}
