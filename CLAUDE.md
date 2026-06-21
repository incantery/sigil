# Sigil — working notes (for picking the project back up)

Sigil is a small typed reactive UI language that compiles to a single npm-free JS
bundle. A tiny kernel (Go) + a standard library written in Sigil. See `README.md`
for the pitch and `docs/kernel-redesign.md` for the design.

## Where things live

- **`internal/`** — the compiler, part of the root module `github.com/incantery/sigil`.
  Packages (flat): `lex`, `token`, `ast`, `parse`, `types` (Hindley-Milner),
  `peval` (partial evaluator / compile-time CSS extraction), `emit` (JS emitter +
  runtime prelude), `load` (module loader + linker), `analysis` (position→node
  index via structural extents + hover; consumes `types.TypeInfo`), `cli` (the
  `sigil` CLI: `version`, `check`, `build`, `serve`, `dev`, `lsp`). The CLI is wrapped by the `cmd/sigil`
  binary (`go run ./cmd/sigil` or `make build` → `bin/sigil`).
- **`examples/`** — runnable `.sigil` apps (e.g. `examples/counter/counter.sigil`).
- **`std/`** — the standard library, in Sigil (`.sigil`): reactive, html, ui, style,
  router, http, result, list, string. Resolved by the loader against a `Root`
  dir; imports are Go-style strings, e.g. `import "std/ui" (card, button)`.
- **`editor/`** — editor support (rebuilt for the current language; the *old*
  `editor/` was deleted with the kernel). `tree-sitter-sigil/` is the tree-sitter
  grammar (Neovim) — `grammar.js` + an offside-rule external scanner
  (`src/scanner.c`), queries, committed generated `src/parser.c`; `vscode-sigil/`
  is the VS Code extension (a TextMate grammar — VS Code has no native
  tree-sitter); `nvim/ftdetect`. Make targets: `tree-sitter{,-test,-verify}`,
  `nvim-install`, `vscode-ext`. **Gotcha:** `tree-sitter parse` caches the
  compiled scanner at `~/.cache/tree-sitter/lib/sigil.dylib` keyed by the
  grammar — after editing `src/scanner.c` you MUST `rm` that file or you test a
  stale scanner. The drift guard `make tree-sitter-verify` parses every `std/` +
  `examples/` file and fails on ERROR nodes; trust it over synthetic corpus tests.

The old "sigil" kernel (`pkg/`, `gauntlet/`, and the observability/Tilt
scaffolding) has been **deleted** — it survives only in git history. The language
is `internal/` + `cmd/sigil` + `std/`.

## Build / test / run

```sh
go build ./...                                            # whole repo must stay green
go test ./...                                             # language suite (incl. headless-Chrome e2e)
go run ./cmd/sigil serve examples/counter/counter.sigil   # serves on :8099 (build-once, production)
go run ./cmd/sigil dev examples/counter/counter.sigil     # HMR dev server on :8099
make build                                                # → bin/sigil
```

`serve` builds the bundle once at startup and serves static bytes — use it for
production. `dev` watches every `.sigil` file and performs state-preserving
in-place hot module replacement over SSE — use it during development. See
`docs/dev-server.md` for details on the serve/dev split and state-preservation
semantics.

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

- **Loader (`internal/load`):** resolves imports, cross-module typechecks in
  dependency order, links into one bundle where each module is an IIFE (so non-pub
  helpers can't collide). Imported **types + constructors always flow**; plain
  values only when named in the selective import.
- **Partial evaluator (`internal/peval`):** const-folds expressions (inline, beta,
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
(fetch → split → map → render). `examples/counter` is the canonical example,
guarded by `internal/load` `TestCounterExample`.

## What's next (rough priority)

The old kernel is gone and the `sigil` CLI (`check`/`build`/`serve`/`dev`) is in
place, so the tree is now `internal/` + `cmd/sigil` + `std/`. Next:

0. **`sigil dev` HMR dev server — DONE** (`sigil dev` watches `.sigil` files and
   does state-preserving in-place hot module replacement over SSE; `sigil serve` is
   now build-once/production). v1 caveat: per-row local state created inside an
   `each` render thunk resets on reload; keyed `each` is the follow-up.
1. More guarded boundaries: `localStorage` (persistence), time, random — same
   total-decoder pattern, mostly synchronous.
2. `std/list` round-out (foldl/reverse/zip); `std/each` keyed-by-fn; controlled
   inputs (bind value back — needs property-vs-attribute handling).
3. M4: a backend op-auth model → real server enforcement + the router's
   "no auth op under a public route" cross-check (check B).
4. Editor/tooling roadmap (4 sub-projects, each its own spec→plan→build cycle):
   **#1 tree-sitter + TextMate highlighting — DONE** (`editor/`, merged).
   **#2 LSP foundation — DONE** (`sigil lsp` stdio server over `internal/load`/
   `internal/types`; live diagnostics via a `load` overlay over unsaved buffers;
   one diagnostic per file; flat document symbols; hand-rolled JSON-RPC, no new deps;
   see `editor/lsp.md` for Neovim wiring).
   **#3 type-aware: slice 3a (analysis core + hover) — DONE** (per-node type
   recorder in `internal/types` (`CheckModuleRecording`/`TypeInfo`);
   `load.Options.Record` → `Program.EntryInfo`; new `internal/analysis` package:
   position→node index via structural extents + hover returning `name : type` or
   generalized scheme).
   **#3 type-aware: slice 3b (go-to-definition) — DONE** (binder positions added
   to the AST (`VarParam`/`VarPat`/`RecordParamField`/`PatField`/`Variant`);
   scope-aware resolver in `internal/analysis` (local → same-file top-level →
   imported/cross-file); `load.Module.Imports()`; `textDocument/definition`
   handler). Binder positions now exist, so hierarchical document symbols (the
   #2 follow-up) are unblocked.
   **#2 follow-up: hierarchical document symbols — DONE** (`type` declarations
   now nest their constructors (ADT → EnumMember children) or fields (record →
   Field children) as child symbols; `FieldType` gained a position; `Variant`
   already had one). Remaining type-aware work: **3c semantic tokens** (role
   classification); then **#4** completion. Also a formatter eventually. The old kernel's
   `pkg/lang/lsp` + `pkg/lang/format` are in git history as reference (superseded
   architecture). Follow-up idea from the #1 review: extend the keyword cross-check
   to assert every keyword appears in BOTH `highlights.scm` and
   `sigil.tmLanguage.json` (catches nvim/VS Code highlight drift automatically).

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
