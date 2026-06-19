package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/incantery/mako/examples/studio/api"
	"github.com/incantery/mako/pkg/lang/loader"
	"github.com/incantery/mako/pkg/lang/lower"
	"github.com/incantery/mako/pkg/render/html"
)

func main() {
	var sigilPath, addr string
	flag.StringVar(&sigilPath, "sigil", "examples/studio/studio.mako",
		"path to the Sigil source file")
	flag.StringVar(&addr, "addr", ":9090", "HTTP listen address")
	flag.Parse()

	absPath, err := filepath.Abs(sigilPath)
	if err != nil {
		log.Fatalf("resolve sigil path: %v", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		log.Fatalf("sigil file not found: %v (run from the repo root or pass --sigil)", err)
	}

	mux := http.NewServeMux()
	api.Mount(mux)

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
		if err := html.WriteDoc(w, "Sigil Studio", doc); err != nil {
			log.Printf("write: %v", err)
		}
	})

	log.Printf("sigil studio listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
