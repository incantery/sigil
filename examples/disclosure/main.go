// Disclosure — M3 walking skeleton for client-side conditional rendering.
//
// Proves: ui.If(cell, ...) actually mounts/unmounts its subtree as the cell
// flips, AND cells referenced from inside an initially-closed If still wire
// up correctly on first mount (the bound text reflects the *current* cell
// value, not the value at page load).
//
// The example has two independent cells (`expanded` and `count`) and binds
// `count` both outside the If (always visible) and inside the If (only
// visible when expanded). Increment a few times with the If closed, then
// open the If — the inner binding should show the same number as the outer
// one, confirming late-mount bindings sync to current state.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/incantery/mako/pkg/render/html"
	"github.com/incantery/mako/pkg/ui"
)

func App() ui.Component {
	expanded := ui.State(false)
	count := ui.State(0)

	return ui.Card(
		ui.Title("Disclosure"),

		ui.Stack(
			ui.Horizontal(),
			ui.Gap(1),
			ui.Button("Toggle", ui.OnClick(ui.Toggle(expanded))),
			ui.Text("count:"),
			count.Format(),
			ui.Button("−", ui.OnClick(ui.Add(count, -1))),
			ui.Button("+", ui.OnClick(ui.Add(count, 1))),
		),

		ui.If(expanded,
			ui.Stack(
				ui.Vertical(),
				ui.Gap(1),
				ui.Text("Hidden by default. Click Toggle to reveal."),
				ui.Stack(
					ui.Horizontal(),
					ui.Gap(1),
					ui.Text("count again (bound from inside the If):"),
					count.Format(),
				),
			),
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
		if err := html.WritePage(w, "Sigil — Disclosure", App()); err != nil {
			log.Printf("render error: %v", err)
		}
	})

	log.Printf("sigil disclosure listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
