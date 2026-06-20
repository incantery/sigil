// Hello — the M1 walking skeleton. Authors a tiny app in Go, lowers it to
// IR, and serves the rendered HTML at http://localhost:8080.
//
// What this proves: ui → ir → render pipeline, stable IDs, semantic primitives.
// What it doesn't yet: events, state, runtime, websocket. Those come in M2.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/incantery/sigil/pkg/render/html"
	"github.com/incantery/sigil/pkg/ui"
)

func App() ui.Component {
	return ui.Card(
		ui.Title("Sigil"),
		ui.Text("Hello, world. This document was authored in Go."),
		ui.Stack(
			ui.Horizontal(),
			ui.Gap(1),
			ui.Button("Primary"),
			ui.Button("Secondary"),
		),
	)
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := html.WritePage(w, "Sigil — Hello", App()); err != nil {
			log.Printf("render error: %v", err)
		}
	})

	log.Printf("sigil hello listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
