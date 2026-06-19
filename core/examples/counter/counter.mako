// counter — the canonical first Mako app, written entirely in the standard
// library: reactive state (std/reactive), semantic components (std/ui), and a
// mount (std/html). Nothing here is a compiler built-in.
//
// Run it:  go run ./core/cmd/serve core/examples/counter/counter.mako
// then open http://localhost:8099

import "std/reactive" (cell)
import "std/ui" (card, column, row, button, label)
import "std/html" (mount)

pub let app =
  let (count, setCount) = cell 0
  let view =
    card [
      column [
        label (fun () -> "count: ${count ()}"),
        row [
          button "-" (fun () -> setCount (count () - 1)),
          button "+" (fun () -> setCount (count () + 1))
        ]
      ]
    ]
  mount view "#app"
