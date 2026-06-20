# Sigil kernel redesign — "Go for the frontend"

Status: **agreed direction, not built** (2026-06-18). Supersedes the multi-target
framing. Clean-room core in a new tree; the existing `pkg/` stays until strangled.

## Thesis

Make sigil feel to the frontend the way Go feels to the backend:

| Go | Sigil |
| --- | --- |
| small orthogonal core language | small **reactive** expression language |
| large stdlib written *in Go* | UI + compute stdlib written *in sigil* |
| single static binary | single self-contained JS bundle, no npm |
| `gofmt`, `go test` built in | `sigil fmt`, `sigil test` (already exist) |
| `unsafe`/syscall escape hatch | **one** host primitive (`element`) + reactive intrinsics |

The pivot is **not** "drop targets" — there is no real multi-target architecture
today (only `"web"` ever existed; ~100 LOC of dead scaffolding). The pivot is:

> Today the standard library **is** the compiler. Every UI primitive (`card`,
> `button`, `stack`), every layout default, every one of ~20 IR kinds, all design
> tokens are hardcoded in Go (`pkg/ui/primitives.go`, `pkg/style/primitives.go`,
> ~6.3K LOC of `lower.go`). **Zero** sigil ships with the compiler.

The compiler core grows a little (functions, operators, a real expression grammar)
so it can shrink a lot: the ~20 hardcoded UI kinds collapse into **one** host
primitive plus sigil-defined components.

## Decisions (2026-06-18)

1. **Clean-room core** in a new package tree. iris is cut loose until it ports.
2. **Unify into one grammar** — everything is an expression; UI is an expression form.
3. **Reactivity is stdlib** built on a low-level effect primitive in the kernel.

SPA / MPA / Hybrid / Static remain — as **output modes** of one JS emitter, not targets.

## The kernel (Go) — the irreducible core

Everything else is sigil. The kernel is only:

- **Values**: Int, Float, Bool, String, List, Record, Variant (discriminated union), Function (first-class, closures).
- **Expressions** (a little ML): literals, operators (`+ - * / %`, `== != < > <= >=`, `&& || !`, `++`, `|>`), `let`/`let rec`, `fun x -> e` lambdas (curried), `match … with`, ADTs + records, calls by **juxtaposition** (`cell "alice"`), field/index access, `if … then … else` as an expression, string interpolation. Type inference (see Open question on HM).
- **Reactive intrinsics** (the "effect primitive"): exactly two.
  - `__cell initial` → a tracked reactive **pair** `(read, write)`. `read ()` registers a dependency; `write v` notifies dependents. (Solid-style signal; ML-honest — just a tuple of functions.)
  - `__effect thunk` → runs `thunk`, auto-tracks every `read ()` inside it, re-runs when any change.
  - *Everything reactive* (`computed`, `memo`, `store`, `resource`, `batch`, async) is derived from these **in sigil**.
- **Host intrinsic** (the one bridge to the platform): `element tag props children` + `text value` + `mount node root`. Reactive props/children are thunks the runtime wraps in `__effect`.
- **Modules**: Go-style string-path imports (`import "github.com/.../std/ui" (Card, Stack)`), `pub`/private visibility. The import path *is* the fetch URL — no registry.

That's the whole Go surface. Parser → core eval/emit → JS + a tiny runtime.

## The unified grammar (ML-flavored, v0 — open for reaction)

Indentation-significant, expression-oriented, a little ML: `let`/`let rec`,
`fun x -> e` lambdas (curried), `match … with`, ADTs + records, `|>`, type
inference. Application is juxtaposition (`cell "alice"`, `name ()`). A cell is a
Solid-style read/write **pair** — read `name ()`, write `setName v`. No methods,
no mutation syntax; just functions and tuples.

```sigil
import "github.com/incantery/sigil/std/ui" (card, stack, button, text, title)
import "github.com/incantery/sigil/std/reactive" (cell)

let echo () =
  let (name, setName) = cell "alice"
  card [
    title "Echo",
    text "hello, ${name ()}",
    stack { horizontal = true, gap = 1 } [
      button { label = "alice", click = fun () -> setName "alice" },
      button { label = "bob",   click = fun () -> setName "bob" },
    ],
  ]
```

Components are curried `props -> children -> Node`: a record of named props, then a
list of children. **Components are lowercase** — the ML rule reserves uppercase for
types and constructors (`Some`, `Home`), which is what lets the parser tell a
constructor from a function with no lookahead. Inside `[ ]`/`{ }` layout is
suspended and commas separate elements (trailing comma ok). Successive block-level
`let`s need no `in`; the last expression is the result (Elm/F# layout).

### The thesis, proven: a stdlib component defined *in sigil*

`Button` is not a compiler builtin anymore. It is sigil over the one host primitive,
using record destructuring + defaults + field punning:

```sigil
import "github.com/incantery/sigil/std/html" (element)
import "github.com/incantery/sigil/std/style" (tokens)

pub let button { label, click = fun () -> () } =
  element "button" { class = tokens.button, on = { click } } [ text label ]
```

### Reactivity, derived from the two intrinsics — *in sigil*

`__cell` returns a `(read, write)` pair; `__effect` runs a thunk and auto-tracks reads.

```sigil
// std/reactive
pub let computed compute =
  let (value, setValue) = __cell (compute ())
  __effect (fun () -> setValue (compute ()))
  value
```

`memo`, `store`, `resource` (async/suspense), `batch` all follow from `__cell` +
`__effect`. The kernel never grows a second reactive concept.

## Safety contract & effects — no TEA (decided 2026-06-18)

Elm's "no runtime errors" comes from its *type system*, not The Elm Architecture.
We keep the guarantee and drop TEA (too rigid for routes/animations), pairing the
type system with **fine-grained reactivity** (signals) instead of Model/Msg/update.

The guarantee rests on four invariants the compiler + stdlib must hold:

1. **No null** — absence is `Option`, exhaustively handled. *(M0)*
2. **Exhaustive `match`**. *(M0)*
3. **Total stdlib** — `head`/`parseInt`/`get` return `Option`/`Result`; no crashing
   partial functions; integer divide-by-zero is defined (→ 0, like Elm).
4. **Guarded boundaries** — everything from the untyped world (DOM events, fetch,
   URL/route params, storage, time, random) enters through total decoders returning
   `Result`. This replaces Elm's ports.

What we give up vs TEA: free global time-travel / replay (state is distributed
across cells, not one Model). Worth it — signals are far better for animations
(a pure derived cell) and routes (a pure signal you `match`).

**Effect discipline = effect contexts** (structural, no effect types in signatures):

- **Reading** a signal (`name ()`) is pure — allowed anywhere (rendering, derived values).
- **Writing**/effects (`set s v`, `__effect`, DOM mutation, fetch) may appear *only*
  lexically inside an `effect { }` block. A bare `fun () -> set s v` is a compile error.
- An `effect { }` block is **deferred** — evaluating it builds a closure; it does not
  run. Only the runtime runs effects: event handlers, lifecycle hooks, reactive
  `__effect`. So constructing UI is pure; effects fire only on runtime-invoked events.
- Enforcement is a syntactic pass over the typed AST (an `inEffect` flag), not an
  `Effect a` type threaded through every signature.

```sigil
let counter () =
  let (count, setCount) = cell 0
  column [
    text "count: ${count ()}",                              -- read: pure
    button { label = "+", click = effect { setCount (count () + 1) } },
  ]
```

## Styling (decided 2026-06-18)

Sigil is opinionated, Tailwind-flavored — the design system is the **type system**,
not class strings. But it is **open**: bespoke and dynamic values are first-class,
like every real design system (a closed/finite token set is not viable).

- **stdlib `std/style`** — typed utilities (`p`, `gap`, `bg`, `text`, …) over token
  enums (`Space`, `Size`, theme `Color`) for the constrained common case, **plus
  escape hatches** (`px`, `hex`, `calc`, raw) for bespoke values. `p s4 : Style`;
  `p s7` / `bg chartreus` is a type error; `p (px 17)` is allowed.
- **core** — one builtin `Style` type + a leaf intrinsic `__style : String ->
  String -> Style`, a **partial evaluator (const-folder)**, and a CSS pass in
  `core/emit`.

A `Style` value compiles by classification (not by a finite token table):

1. **Static** — token *or* bespoke literal, i.e. const-foldable → an atomic CSS
   class generated at build (`_p4`, `_p_17px`), dead-code-free.
2. **Dynamic** — reads a cell (`p (px (half (width ())))`) → reactive inline
   `style` / CSS variable via the existing `__bindAttr` primitive. No CSS rule.

The compiler **partial-evaluates** each style expression (resolving `s4 → "1rem"`,
folding `px (8+9) → "17px"`, detecting a `__get` to mark it dynamic) to decide
static vs dynamic. Static → emitted stylesheet; dynamic → reactive inline/var.
**No runtime style engine** — "the compiler is the framework" holds. The partial
evaluator is reusable core machinery (constant folding, dead-code elimination,
later compile-time specialization).

## Core surface — what's left to add (then core stops growing)

The kernel is nearly closed. Remaining genuine core additions (each is something
the platform exposes that sigil can't reach via existing primitives):

1. **Reactive structure** — `__each : Cell (List a) -> (a -> Node) -> Node` (keyed)
   and `__when : Cell Bool -> (unit -> Node) -> Node`. Static `List Node` can't do
   efficient keyed DOM reconciliation. *The urgent one — needed before real apps.*
2. **Styling** — builtin `Style` type + `__style` leaf intrinsic + a **partial
   evaluator** (const-folds style exprs to classify static vs dynamic) + CSS emit
   pass. Static → atomic CSS; dynamic → reactive inline/var. The partial evaluator
   is general-purpose core machinery, reusable for constant folding / dead-code.
3. **Guarded boundaries** — `__on` passing a *decoded* event; `fetch`/`storage`/
   `location`/`raf` boundary intrinsics whose results flow through total decoders to
   `Result`. Keeps "no runtime errors" once apps touch the outside world.
4. **Async** (later) — a promise/resource primitive for data fetching.

Everything else — router, forms, animations, components, layout — is stdlib on
these. (Animations = derived cells driven by a frame ticker; only `raf` is core.)

## Package tree (clean-room)

```
core/                 # the kernel (Go)
  lex/ parse/ ast/    # unified expression grammar
  emit/               # JS emitter + tiny runtime
  intrinsics/         # __cell, __effect, element, text, mount
std/                  # the standard library — written in sigil
  reactive/  html/  ui/  style/         # the UI framework
  list/  string/  math/  json/          # the compute stdlib
  router/  forms/  net/  (M4)
```

`pkg/` (today's compiler) stays for reference and is deleted as `core/` + `std/`
subsume it. Keep & re-home the good parts: JS-emit philosophy
(compiler-is-framework, self-contained bundle), style/token system, serve/CSP,
the test scenario IR, `contract/` backend codegen.

## Milestones

- **M0 — Typed kernel. ✅ DONE.** `core/` lexer + parser + AST + **HM type checker** + JS emit for the expression core (literals, operators, `let`/`let rec`, `fun`, `match`, ADTs, records, `Option`). A typed program compiles to JS and runs; type errors are real (mismatch / unbound / non-exhaustive / infinite-type all rejected). All tests green.
- **M1 — Reactive + host. ✅ DONE.** Intrinsics with principal types (`__cell`/`__get`/`__set`/`__effect`, `__elem`/`__text`/`__attr`/`__bindAttr`/`__on`/`__mount`), the `effect { }` block + effect-context check (read-pure / write-effect), and a tiny JS runtime (fine-grained signal graph + DOM bind). The counter renders and reacts in a real headless browser (chromedp: 0 → click → 1 → 2).
- **M1.5 — Finish the host layer. ✅ DONE.** Reactive structure intrinsics `__each` (keyed list, node reuse via `__eq`, untracked child render) + `__when` (conditional) on the signal runtime. Verified in a real browser: list reorders (abc→cba reusing nodes), grows (→abcd), conditional node toggles in/out of the DOM.
- **M2 — Stdlib bootstrap.** Module loader (resolve `import "path"` + cross-module typecheck), then `std/reactive` (cell as Solid-style read/write pair, computed/effect), `std/html`, `std/style` (typed Tailwind-flavored utilities + tokens + the core CSS pass), and `std/ui` (text, column, button, card) written **in sigil**, type-checked. Echo runs on sigil-defined components. ← thesis proven.
- **M3 — Compute stdlib.** `std/list`, `std/string`, `std/math`, `std/option` in sigil. Language is self-sufficient.
- **M4 — Re-home platform features.** Router, forms, backend/query/command/stream, theming — as typed sigil stdlib (today special-cased in `lower.go`).
- **M5 — Tooling + dogfood.** `sigil fmt`, `sigil test`, LSP (HM powers hover/type-on-hover/go-to-def), tree-sitter onto the new grammar. iris ports onto `core/`.

## Syntax decisions (ML-flavored)

Locked:
- **Cells are pairs**: `let (name, setName) = cell "alice"` — read `name ()`, write `setName v`. Pure functions + tuples; no method/mutation syntax.
- **Lambdas**: `fun x -> e`; thunk is `fun () -> e`.
- **Application**: juxtaposition (`cell "alice"`, `name ()`); components curried `props -> children`.
- **Imports**: Go-style string paths for github resolution — `import "github.com/.../std/ui" (card, stack)`. Default qualifier is the last path segment (`ui.card`) à la Go; `(names)` pulls selected names into scope; `import "…" as Ui` renames.
- **Capitalization is significant** (ML rule): lowercase = value/function/component, uppercase = type/constructor. UI components are lowercase (`card`, `button`); uppercase is reserved for `Some`/`Color`/etc.
- **Lists/records**: `[ a, b, c ]` / `{ x = 1, y = 2 }`, comma-separated with optional trailing comma; inside brackets layout is suspended. No `in` on block-level `let`.

Still open:
1. **Children delimiter**: explicit `[ … ]` list (ML-honest, shown above) vs a `:`-indent block sugar lowering to the same list. Lean: explicit `[ ]`.
2. **`let rec` vs inferred recursion**: require `let rec` (OCaml) or auto-detect self-reference (Elm). Lean: auto-detect — less ceremony for agent-written stdlib.

## Type system — Hindley-Milner (decided)

Full HM inference. Rationale is the project's north star: **built for AI coding
assistants from the ground up.** The type checker is the agent's correctness
oracle, and the strongest possible form of sigil's compiler-first enforcement:

- **Principal types as machine-checkable contracts** — an agent reading a stdlib signature gets a total spec, not a docstring it must trust.
- **Exhaustive `match`** — the bug agents make most (forgetting a variant) is a compile error.
- **No null** — absence is `Option`; the checker forces the handling.
- **Inference keeps it invisible** — the agent writes almost no annotations and still gets every guarantee.

"Small core" was about *concept count*, not LOC: HM adds a big internal component
but zero user-facing concepts. The language the agent sees stays tiny.

Principal types of the intrinsics:

```
__cell    : a -> (unit -> a, a -> unit)
__effect  : (unit -> unit) -> unit
element   : String -> props -> List Node -> Node
text      : String -> Node
```

**Props + HM** is the one real subtlety: UI props are open/optional records, which
classic HM records are not. Resolution (v0): each component declares a **nominal
record type** for its props, defaults supplied by destructuring patterns
(`{ label, click = fun () -> () }`). Keeps HM classic, yields the best agent errors
("missing field `label`", "no field `clik`") without row polymorphism. Revisit
row-polymorphic records only if nominal proves too rigid for composition.
