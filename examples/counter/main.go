// Counter — M2 walking skeleton for pure-client reactivity.
//
// Proves: typed reactive cells (ui.State), declarative actions (ui.Add) wired
// into OnClick, a text binding (count.Format()) that re-renders when the cell
// changes, all driven by the embedded JS runtime. No websocket, no session,
// no Node, no npm.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/incantery/sigil/pkg/render/html"
	"github.com/incantery/sigil/pkg/ui"
)

func App() ui.Component {
	count := ui.State(0)

	return ui.Card(
		ui.Title("Counter"),
		ui.Stack(
			ui.Horizontal(),
			ui.Gap(1),
			ui.Button("−", ui.OnClick(ui.Add(count, -1))),
			count.Format(),
			ui.Button("+", ui.OnClick(ui.Add(count, 1))),
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
		if err := html.WritePage(w, "Sigil — Counter", App()); err != nil {
			log.Printf("render error: %v", err)
		}
	})

	log.Printf("sigil counter listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
