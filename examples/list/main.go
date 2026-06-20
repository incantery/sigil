// List — M4 walking skeleton for keyed list diffing on the client.
//
// Proves: ui.ListState[T] creates a list of independently-addressable child
// cells, ui.For renders one row per child plus an inert template, and the
// runtime appends/removes rows in place without re-rendering the unaffected
// rows.
//
// Test in browser:
//   - Increment row 2's counter several times — only row 2's number updates,
//     other rows don't blink
//   - Click "+ add counter" — a new row appears at the bottom with its own
//     cell (runtime-assigned id like "r1")
//   - Increment the new row — works the same as the original rows
//   - Click ✕ on row 1 — only row 1 is removed; the remaining rows keep their
//     state (no re-render, no state loss)
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/incantery/sigil/pkg/render/html"
	"github.com/incantery/sigil/pkg/ui"
)

func App() ui.Component {
	items := ui.ListState(0, 0, 0)

	return ui.Card(
		ui.Title("Counters"),

		ui.For(items, func(item *ui.Cell[int]) ui.Component {
			return ui.Stack(
				ui.Horizontal(),
				ui.Gap(1),
				ui.Button("−", ui.OnClick(ui.Add(item, -1))),
				item.Format(),
				ui.Button("+", ui.OnClick(ui.Add(item, 1))),
				ui.Button("✕", ui.OnClick(ui.RemoveItem(items, item))),
			)
		}),

		ui.Button("+ add counter", ui.OnClick(ui.AppendItem(items, 0))),
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
		if err := html.WritePage(w, "Sigil — List", App()); err != nil {
			log.Printf("render error: %v", err)
		}
	})

	log.Printf("sigil list listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
