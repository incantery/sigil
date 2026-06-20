// Command docs serves the Sigil documentation site — itself a Sigil app.
//
// It re-reads and recompiles docs.sigil on every request, so editing the
// source and refreshing the browser is the whole dev loop.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/incantery/sigil/pkg/lang/loader"
	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/render/html"
)

func main() {
	var sigilPath, addr string
	flag.StringVar(&sigilPath, "sigil", "examples/docs/docs.sigil",
		"path to the Sigil source file")
	flag.StringVar(&addr, "addr", ":8088", "HTTP listen address")
	flag.Parse()

	absPath, err := filepath.Abs(sigilPath)
	if err != nil {
		log.Fatalf("resolve sigil path: %v", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		log.Fatalf("sigil file not found: %v (run from the repo root or pass --sigil)", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		prog, err := loader.Load(absPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		merged, err := prog.Merge()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		doc, err := lower.Lower(merged)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := html.WriteDoc(w, "Sigil — Documentation", doc); err != nil {
			log.Printf("write: %v", err)
		}
	})

	log.Printf("sigil docs listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
