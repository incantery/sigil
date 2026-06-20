// Form-echo — M4-prep walking skeleton for two-way text-input binding.
//
// Proves: ui.TextInput two-way binds to a *Cell[string]. Every keystroke fires
// an "input" action whose value comes from the event ($event.value) and
// updates the cell; the same cell drives a Text echo elsewhere on the page so
// the user sees what they typed reflected in real time.
//
// Also proves focus / caret preservation: the input element is itself a
// binding target for the same cell, but the runtime skips redundant writes to
// el.value, so the caret never jumps while typing.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/incantery/sigil/pkg/render/html"
	"github.com/incantery/sigil/pkg/ui"
)

func App() ui.Component {
	name := ui.State("")

	return ui.Card(
		ui.Title("Form echo"),
		ui.Stack(
			ui.Vertical(),
			ui.Gap(1),
			ui.TextInput(name, ui.Placeholder("type your name")),
			ui.Stack(
				ui.Horizontal(),
				ui.Gap(0),
				ui.Text("hello, "),
				name.Format(),
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
		if err := html.WritePage(w, "Sigil — Form echo", App()); err != nil {
			log.Printf("render error: %v", err)
		}
	})

	log.Printf("sigil form-echo listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
