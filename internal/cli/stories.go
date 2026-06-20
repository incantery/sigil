package cli

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/incantery/sigil/pkg/ir"
	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/lang/parser"
	"github.com/incantery/sigil/pkg/render/html"
)

var storiesAddr string

var storiesCmd = &cobra.Command{
	Use:   "stories <path>",
	Short: "Serve the project's story catalog over HTTP",
	Long: `Compiles the project's story declarations and serves a catalog viewer:
a sidebar listing every story and a frame rendering the selected one. Each
story is served as its own isolated document (own cells, own bundle) at
/story/<n>; the catalog shell at / is itself a generated Sigil app. The
source is re-compiled on every request, so the dev loop is: edit, save,
refresh browser.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		// Fail-fast at startup so compile errors (including errors inside
		// story bodies) show in the terminal, not just in the browser.
		doc, err := compileFile(path)
		if err != nil {
			return err
		}
		if len(doc.Stories) == 0 {
			return fmt.Errorf("no stories declared in %s — add one with `story \"<name>\" =`", path)
		}

		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			doc, err := compileFile(path)
			if err != nil {
				httpCompileError(w, err)
				return
			}
			cdoc, err := catalogDoc(doc)
			if err != nil {
				httpCompileError(w, fmt.Errorf("internal: generated catalog failed to compile: %w", err))
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := html.WriteDoc(w, catalogTitle(doc), cdoc); err != nil {
				log.Printf("write: %v", err)
			}
		})

		http.HandleFunc("/story/", func(w http.ResponseWriter, r *http.Request) {
			idx, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/story/"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			doc, err := compileFile(path)
			if err != nil {
				httpCompileError(w, err)
				return
			}
			if idx < 0 || idx >= len(doc.Stories) {
				http.NotFound(w, r)
				return
			}
			s := doc.Stories[idx]
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := html.WriteDoc(w, s.Name, s.Doc); err != nil {
				log.Printf("write: %v", err)
			}
		})

		log.Printf("sigil stories %s — %d stories, listening on %s",
			path, len(doc.Stories), storiesAddr)
		return http.ListenAndServe(storiesAddr, nil)
	},
}

// catalogDoc builds the catalog shell as a Sigil program: a sidebar of
// buttons that set the `selected` cell and an iframe whose src binds to
// it. Generating source (rather than hand-assembling IR) keeps the
// catalog inside the language's own checked path — the same parser,
// lowerer, profile gate, and codegen every user app goes through.
func catalogDoc(doc ir.Document) (ir.Document, error) {
	var b strings.Builder
	b.WriteString("view Catalog =\n")
	b.WriteString("  state selected = \"/story/0\"\n")
	b.WriteString("  stack horizontal gap=2 height=screen padding=md\n")
	b.WriteString("    card\n")
	b.WriteString("      stack vertical gap=1\n")
	b.WriteString("        title \"Stories\"\n")
	for i, s := range doc.Stories {
		fmt.Fprintf(&b, "        button %s on click { selected = %q }\n",
			strconv.Quote(s.Name), fmt.Sprintf("/story/%d", i))
	}
	b.WriteString("    stack vertical flex=1\n")
	b.WriteString("      iframe src=selected height=800\n")

	root, err := parser.Parse(b.String())
	if err != nil {
		return ir.Document{}, err
	}
	return lower.Lower(root)
}

// catalogTitle labels the catalog tab after the project's main view
// when it has one.
func catalogTitle(doc ir.Document) string {
	if doc.Name != "" {
		return "Stories — " + doc.Name
	}
	return "Stories"
}

// httpCompileError surfaces a compile failure as plain text in the
// browser, matching `sigil run`'s behavior.
func httpCompileError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, "sigil: %v\n", err)
}

func init() {
	storiesCmd.Flags().StringVarP(&storiesAddr, "addr", "a", ":8089",
		"HTTP listen address")
}
