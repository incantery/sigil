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
		Use:   "serve ENTRY.mako",
		Short: "Serve a mako module as a live-rebuilding dev page",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry := args[0]
			// Build once up front to fail fast on errors (before binding a port).
			if _, err := bundle(entry, root); err != nil {
				return err
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
				js, err := bundle(entry, root)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprintf(w, "build error: %v", err)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, htmlPage(entry, js))
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
