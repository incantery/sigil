package cli

import (
	"fmt"
	"log"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/incantery/sigil/pkg/render/html"
)

var runAddr string

var runCmd = &cobra.Command{
	Use:   "run <file.sigil>",
	Short: "Compile a Sigil source file and serve it over HTTP",
	Long: `Compiles a .sigil source file and serves the rendered HTML at the
given address. The file is re-read and re-compiled on every request, so the
dev loop is: edit, save, refresh browser.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		// Fail-fast at startup so parse errors show in the terminal, not
		// just in the browser.
		if _, err := compileFile(path); err != nil {
			return err
		}

		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			doc, err := compileFile(path)
			if err != nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, "sigil: %v\n", err)
				return
			}
			title := "Sigil"
			if doc.Name != "" {
				title = "Sigil — " + doc.Name
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := html.WriteDoc(w, title, doc); err != nil {
				log.Printf("write: %v", err)
			}
		})

		log.Printf("sigil run %s — listening on %s", path, runAddr)
		return http.ListenAndServe(runAddr, nil)
	},
}

func init() {
	runCmd.Flags().StringVarP(&runAddr, "addr", "a", ":8080",
		"HTTP listen address")
}
