package cli

import (
	"fmt"
	"log"
	"net/http"

	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var (
		root string
		port string
	)
	cmd := &cobra.Command{
		Use:   "serve ENTRY.sigil",
		Short: "Serve a sigil module as a static production page",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry := args[0]
			// Production: build once up front. A type/parse error aborts before
			// binding a port; there is no per-request rebuild.
			js, err := bundle(entry, root)
			if err != nil {
				return err
			}
			page := htmlPage(entry, js)
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, page)
			})
			addr := ":" + port
			log.Printf("serving %s on http://localhost%s", entry, addr)
			return http.ListenAndServe(addr, mux)
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "module root directory (where std/ lives)")
	cmd.Flags().StringVar(&port, "port", "8099", "port to serve on")
	return cmd
}
