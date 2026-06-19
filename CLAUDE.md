# Mako — working notes (for picking the project back up)

Mako is a small typed reactive UI language that compiles to a single npm-free JS
bundle. A tiny kernel (Go) + a standard library written in Mako. See `README.md`
for the pitch and `docs/kernel-redesign.md` for the design.

## Where things live

- **`core/`** — the compiler, part of the root module `github.com/incantery/mako`
  (there is **no** `core/go.mod`). Packages: `lex`, `token`, `ast`, `parse`,
  `types` (Hindley-Milner), `peval` (partial evaluator / compile-time CSS
  extraction), `emit` (JS emitter + runtime prelude), `load` (module loader +
  linker). `core/cmd/serve` is the dev server. `core/examples/` holds runnable
  `.mako` apps.
- **`std/`** — the standard library, in Mako (`.mako`): reactive, html, ui, style,
  router, http, result, list, string. Resolved by the loader against a `Root`
  dir; imports are Go-style strings, e.g. `import "std/ui" (card, button)`.
- **`pkg/`, `internal/`, `cmd/`, `editor/`, `examples/`, `gauntlet/`** — the
  **superseded old "sigil" kernel**, kept for reference, pending removal. It still
  uses internal `sigil` naming and `__sigil_*`/`data-sigil-*` wire markers — do
  not invest in it; the language is `core/` + `std/`.

## Build / test / run

```sh
go build ./...                       # whole repo (old + new) must stay green
go test ./core/...                   # the language test suite (incl. headless-Chrome e2e)
go run ./core/cmd/serve core/examples/counter/counter.mako   # serves on :8099
```

Browser tests use chromedp and **skip** if Chrome is absent. The dep
`github.com/dop251/goja` runs emitted JS hermetically in non-browser tests.

## The kernel (≈24 intrinsics — keep it from growing)

Everything else is stdlib in Mako. Intrinsics are `__`-prefixed:

- **Reactive:** `__cell __get __set __effect` (fine-grained signals).
- **Host/DOM:** `__elem __text __attr __bindAttr __style __on __mount`;
  `__each __when` (reactive structure — take **reader thunks** `unit -> a`,
  auto-tracking like `__text`).
- **Boundaries (total decoders, no runtime errors):** `__eventValue`
  (event → String), `__fetch` (callback-continuation; (ok, body) decoded to a
  `Result` in `std/http`), `__path __pushPath __onPopState` (location/history),
  `__split __listLen __listAt __listConcat` (string/list).
- **Opaque builtin types:** `Cell Node Attr Event`; plus `Option`/`Result` ADTs.

## Architecture seams worth knowing

- **Loader (`core/load`):** resolves imports, cross-module typechecks in
  dependency order, links into one bundle where each module is an IIFE (so non-pub
  helpers can't collide). Imported **types + constructors always flow**; plain
  values only when named in the selective import.
- **Partial evaluator (`core/peval`):** const-folds expressions (inline, beta,
  match-reduction). The emitter runs it over list-literal elements; a folded
  `__style "prop" "val"` is hoisted to an atomic CSS class (`__addClass` +
  `__installStyles`). It is a pure **optimization** — `__style` has an inline
  runtime fallback. peval never inlines `let rec` (would blow the depth budget).
- **Effect discipline:** effect intrinsics (`__set __effect __mount __fetch
  __pushPath __onPopState`) are legal only lexically inside an `effect { }` block.
  Stdlib wraps them with build-and-run: `(effect { __set c v }) ()` yields an
  ordinary effectful function.

## What works today (all browser-verified)

A complete SPA in Mako stdlib: reactive state, components (`std/ui`), typed
styling with design-system tokens (`std/style`, `p Sky` is a *type error*),
events + a real text-input Echo, HTTP with a `Result` boundary (`std/http`),
client routing with history + popstate + typed `:params` + **default-deny guards
enforced by the type system** (`std/router`), and data lists
(fetch → split → map → render). `core/examples/counter` is the canonical example,
guarded by `core/load` `TestCounterExample`.

## What's next (rough priority)

1. Delete or fully scrub the old `pkg/` kernel (it's the last "sigil" residue).
2. More guarded boundaries: `localStorage` (persistence), time, random — same
   total-decoder pattern, mostly synchronous.
3. `std/list` round-out (foldl/reverse/zip); `std/each` keyed-by-fn; controlled
   inputs (bind value back — needs property-vs-attribute handling).
4. M4: a backend op-auth model → real server enforcement + the router's
   "no auth op under a public route" cross-check (check B).

## Gotchas (learned the hard way)

- In a **type annotation**, lowercase = type variable (HM). The unit type is
  `Unit`, not `unit` (`Guard of (Unit -> Bool)`).
- Block-form `if cond then <newline-block> else <newline-block>` parses now (fixed
  in `parseIf`). Single-line `if a then b else c` always worked.
- `cell []` (empty-list cell) type-checks — the checker has the value restriction.
- `chromedp.Text` trims trailing whitespace (an empty-cell `"hello, "` reads
  `"hello,"`); account for it in assertions.
- New language features should land kernel-minimal: prefer adding a stdlib `.mako`
  function over a new intrinsic; browser-verify; keep the type checker as the
  enforcement layer.

> The detailed slice-by-slice history is in the prior project's Claude memory
> (keyed to the old sigil path) and does not auto-load here — this file is the
> portable summary.
