# Sigil — working notes (for picking the project back up)

Sigil is a small typed reactive UI language that compiles to a single npm-free JS
bundle. A tiny kernel (Go) + a standard library written in Sigil. See `README.md`
for the pitch and `docs/kernel-redesign.md` for the design.

## Where things live

- **`core/`** — the compiler, part of the root module `github.com/incantery/sigil`
  (there is **no** `core/go.mod`). Packages: `lex`, `token`, `ast`, `parse`,
  `types` (Hindley-Milner), `peval` (partial evaluator / compile-time CSS
  extraction), `emit` (JS emitter + runtime prelude), `load` (module loader +
  linker). `core/cli` is the `sigil` CLI (`version`, `check`, `build`, `serve`),
  wrapped by the `core/cmd/sigil` binary. `core/examples/` holds runnable
  `.sigil` apps.
- **`std/`** — the standard library, in Sigil (`.sigil`): reactive, html, ui, style,
  router, http, result, list, string. Resolved by the loader against a `Root`
  dir; imports are Go-style strings, e.g. `import "std/ui" (card, button)`.

The old "sigil" kernel (`pkg/`, `internal/`, `cmd/`, `editor/`, `examples/`,
`gauntlet/`, and the observability/Tilt scaffolding) has been **deleted** — it
survives only in git history (removed after the mako→sigil rename, which made its
`sigil` naming collide with the real toolchain). The language is `core/` + `std/`.

## Build / test / run

```sh
go build ./...                       # whole repo must stay green
go test ./...                        # the language test suite (incl. headless-Chrome e2e)
go run ./core/cmd/sigil serve core/examples/counter/counter.sigil   # serves on :8099
make build                           # → bin/sigil (then: bin/sigil serve|build|check ENTRY.sigil)
```

Browser tests use chromedp and **skip** if Chrome is absent. The dep
`github.com/dop251/goja` runs emitted JS hermetically in non-browser tests.

## The kernel (≈24 intrinsics — keep it from growing)

Everything else is stdlib in Sigil. Intrinsics are `__`-prefixed:

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

A complete SPA in Sigil stdlib: reactive state, components (`std/ui`), typed
styling with design-system tokens (`std/style`, `p Sky` is a *type error*),
events + a real text-input Echo, HTTP with a `Result` boundary (`std/http`),
client routing with history + popstate + typed `:params` + **default-deny guards
enforced by the type system** (`std/router`), and data lists
(fetch → split → map → render). `core/examples/counter` is the canonical example,
guarded by `core/load` `TestCounterExample`.

## What's next (rough priority)

The old kernel is gone and the `sigil` CLI (`check`/`build`/`serve`) is in place,
so the tree is now just `core/` + `std/`. Next:

1. More guarded boundaries: `localStorage` (persistence), time, random — same
   total-decoder pattern, mostly synchronous.
2. `std/list` round-out (foldl/reverse/zip); `std/each` keyed-by-fn; controlled
   inputs (bind value back — needs property-vs-attribute handling).
3. M4: a backend op-auth model → real server enforcement + the router's
   "no auth op under a public route" cross-check (check B).
4. Editor/tooling for `core/`: an LSP and formatter (the old kernel had both —
   `pkg/lang/lsp`, `pkg/lang/format` — in git history as reference, though built
   on the superseded architecture).

## Gotchas (learned the hard way)

- In a **type annotation**, lowercase = type variable (HM). The unit type is
  `Unit`, not `unit` (`Guard of (Unit -> Bool)`).
- Block-form `if cond then <newline-block> else <newline-block>` parses now (fixed
  in `parseIf`). Single-line `if a then b else c` always worked.
- `cell []` (empty-list cell) type-checks — the checker has the value restriction.
- `chromedp.Text` trims trailing whitespace (an empty-cell `"hello, "` reads
  `"hello,"`); account for it in assertions.
- New language features should land kernel-minimal: prefer adding a stdlib `.sigil`
  function over a new intrinsic; browser-verify; keep the type checker as the
  enforcement layer.

> The detailed slice-by-slice history is in the prior project's Claude memory
> (keyed to the old sigil path) and does not auto-load here — this file is the
> portable summary.
