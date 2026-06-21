# Sigil

Sigil is a small, typed, reactive language for building web UIs. It compiles to a
single self-contained JavaScript bundle — **no npm, no node, no runtime
framework**. Think "Go for the frontend": a tiny language core, with a standard
library written in Sigil itself.

## Why

- **No npm/node.** The only JavaScript in your page is what the compiler emitted,
  and you can read all of it — tiny, auditable bundles.
- **Typed end to end.** Full Hindley-Milner inference, algebraic data types, and
  exhaustive `match`; the type checker is the correctness oracle.
- **Reactive by construction.** Fine-grained signals (Solid-style), no virtual
  DOM — direct DOM updates.
- **A small kernel.** ~two dozen intrinsics; everything else — components,
  styling, routing, HTTP — is library code in `std/`, written in Sigil.

## A taste

```sigil
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
```

## Run it

```sh
go run ./cmd/sigil serve examples/counter/counter.sigil
# open http://localhost:8099
# or: make build && bin/sigil serve examples/counter/counter.sigil
```

The bundle is rebuilt on every request, so editing the source and refreshing is
the dev loop.

## Layout

- **`internal/`** — the compiler: lexer, parser, Hindley-Milner type checker,
  partial evaluator (compile-time CSS extraction), JavaScript emitter, cross-module
  loader, and the `sigil` CLI (`check`, `build`, `serve`). The binary entry point
  is `cmd/sigil`.
- **`examples/`** — runnable `.sigil` apps (e.g. `examples/counter/counter.sigil`).
- **`std/`** — the standard library, written in Sigil: `reactive`, `html`, `ui`,
  `style` (typed design-system tokens), `router` (path routing, history, typed
  `:params`, default-deny guards), `http`, `result`, `list`, `string`.
- **`docs/kernel-redesign.md`** — the design.

## Status

Early, but real. A complete single-page app — reactive state, semantic
components, typed styling, events, HTTP with a `Result` boundary, client routing
with history and typed parameters, type-enforced auth guards, and data lists
(fetch → decode → map → render) — is expressible entirely in Sigil library code
over the kernel, and every layer is verified in a real browser.

> An earlier compiler (the old "sigil" kernel, formerly in `pkg/` and `editor/`)
> has been removed; it lives on only in git history. The language lives in
> `internal/`, `cmd/sigil`, and `std/`.
