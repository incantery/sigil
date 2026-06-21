# `sigil dev` — hot-module-reloading dev server (design)

Date: 2026-06-21
Status: approved, pre-implementation

## Goal

Close the inner-loop gap. Today `sigil serve` rebuilds the bundle on every HTTP
request but never notifies the browser — you edit a `.sigil` file and must hit
refresh by hand, and a build error renders as a plain-text 500. We want a real
dev server, `sigil dev`, that watches the source tree, rebuilds on change, and
performs **state-preserving, in-place hot module replacement** in the browser:
no page reload, the reactive cell state survives the swap.

Alongside it, `sigil serve` is redefined as a **production** run.

## Decisions (settled during brainstorming)

1. **True HMR, not live-reload.** State is preserved across edits — a counter
   keeps its value when you tweak the markup around it.
2. **In-place swap, no page reload.** The app mounts under a single `#app` root,
   so the client tears down and rebuilds that subtree in place. Scroll, focus,
   console, and devtools all survive.
3. **`serve` vs `dev` split.** `serve` = production (build once, serve static).
   `dev` = watch + HMR. This is an intentional behavior change to today's
   rebuild-on-request `serve`.
4. **Eval-in-place architecture (Approach 1).** The existing IIFE bundle runs
   as-is inside `new Function(bundle)()`; production emit is untouched. (Rejected:
   ES-module dynamic re-import, which would force an emit-format change rippling
   into production.)
5. **State-preservation scope (v1 = "A").** Preserve component/top-level cell
   state. Cell state created *inside* an `__each` render thunk (per-row local
   state) may reset — a documented seam toward a future "B".

## Architecture

### Why the runtime makes this feasible

From `internal/emit/emit.go`:

- `const __cell = (init) => ({ v: init, subs: new Set() });` — cells are plain
  objects created inline as the component tree builds. They have **no inherent
  identity**, which is the entire problem state-preservation must solve.
- The app mounts under one `#app` node via `__mount(node)("#app")`, so teardown
  is "empty `#app`."
- The bundle is `prelude + installStyles + module IIFEs`, and the entry module's
  mount runs as a top-level side effect. Therefore `new Function(bundle)()`
  re-runs the whole app in a **fresh scope** — no `const` redeclaration conflict.

### 1. Command split

- **`sigil serve ENTRY.sigil`** → production. Build the bundle **once** at
  startup (fail fast if it doesn't compile), serve it as static bytes. No
  rebuild-on-request. Keeps `--root` / `--port`.
- **`sigil dev ENTRY.sigil`** → new. Watch + rebuild + in-place HMR. Same
  `--root` / `--port` flags; default port `8099`.

### 2. Dev server components

Lives in a small `internal/devserver` package, wired by `internal/cli/dev.go`.

- **Watcher.** Poll mtimes of every `.sigil` file under `--root` on a ~150ms
  tick (zero new deps; fits the npm-free/minimal ethos). Debounce bursts so one
  save = one rebuild. Watching the whole tree (a superset of the entry's import
  closure) is simpler and cheap.
- **Transport.** An SSE endpoint `/__sigil/events` — one-way server→client, plain
  `net/http`, no dependency. On a detected change: rebuild; on success push a
  `reload` message carrying the new bundle source; on failure push an `error`
  message carrying the compiler error text.
- **Shell.** The HTML page includes a persistent **client agent** (served at
  `/__sigil/agent.js`) plus the initial bundle. The agent owns the `EventSource`
  and the HMR lifecycle.

### 3. Dev build mode (`emit` gains a `Dev bool` option)

- **Why keying is by call order, not source position.** Every user cell funnels
  through one stdlib wrapper — `std/reactive`'s `cell` (and `computed`) — and
  `__cell` is a plain `ast.App` of an identifier, not a dedicated node. So the
  emitted program contains exactly **one** `__cell` call site. A per-call-site
  structural key would therefore be identical for every cell in the app and
  cannot distinguish them. The only available — and the natural — identity is the
  **order** in which `__cell` runs during a mount.
- In dev, the `Dev` flag's **sole** effect is to swap the production prelude for
  the **dev-variant prelude** (instrumented `__cell`, `__onPopState`,
  `__installStyles`, `__fetch`; see §4–5). There is **no** emit-site change: no
  second `__cell` argument, no `App`-case special-casing. The dev `__cell`
  assigns its own key from a per-mount counter at runtime.
- This call-order key has the same practical behavior promised in brainstorming:
  markup/style/handler edits create no cells, so cell order is unchanged and state
  survives; state resets only when you add, remove, or reorder `cell`
  declarations themselves (which shifts the indices of cells created after the
  edit), where a reset is intuitive.
- Production (`Dev:false`) emits **exactly** what it does today: plain prelude,
  unchanged `__cell`. A golden test guards byte-identical production output.

### 4. State preservation

- A stable global `window.__sigilDev` holds
  `{ hydration: Map<index,value>, cells: Map<index,cell>, counter: 0, disposers: [], generation: 0 }`.
  It lives outside the eval'd bundle scope so it persists across swaps. `counter`
  resets to 0 at the start of every mount; each dev `__cell` call takes the next
  index.
- Dev `__cell(init)`: let `i = counter++`; if `hydration` has `i`, start the cell
  from that saved value instead of `init`; register the live cell in `cells`
  under `i`.
- `computed` cells are hydrated by the same call-order rule but **self-heal**: a
  `computed`'s `watch` effect recomputes immediately on creation
  (`std/reactive.sigil`), overwriting any stale hydrated value with a fresh
  derivation. No special-casing needed.
- **HMR sequence** (in the agent), on a `reload` message:
  1. **snapshot** — `cells` → `{ i: cell.v }`.
  2. **dispose** — run all `disposers` (§5); bump `generation`.
  3. empty `#app`; clear `cells`; reset `counter = 0`.
  4. set `hydration = snapshot`.
  5. `new Function(bundle)()` — new cells rehydrate from `hydration` by index.
  6. clear `hydration`.
- Cells created inside `__each` render thunks are keyed by interleaved call order,
  so list-content changes shift their indices and they reset (v1 scope A).
  Documented in the dev README.

### 5. Disposal model

Emptying the `#app` subtree already kills DOM-node listeners (`__on`) and orphans
old cells/effects (their cells are gone, so effects cannot re-fire). The only true
leaks are **global** registrations, so the dev prelude tracks just those:

- `__onPopState` → push its `window` listener remover into `disposers`.
- `__installStyles` → in dev, key the injected `<style>` tag and **replace**
  rather than append, so sheets don't pile up across reloads.
- In-flight `__fetch` → guard each callback with a per-generation token so a
  response landing after an HMR is dropped.

### 6. Wire protocol + error overlay

- SSE messages are line-delimited JSON with two types:
  - `reload` — carries the new bundle source.
  - `error` — carries the compiler error text.
- On `error`, the agent shows a dismissable **overlay** over the still-running
  app (which keeps its state) instead of a blank 500. The next successful
  `reload` clears the overlay and proceeds with HMR.

## Testing

- **Go unit tests:** watcher debounce; SSE framing; `emit` with `Dev:true` swaps
  in the dev prelude (and `Dev:false` is byte-for-byte unchanged — golden test).
- **goja test:** the dev `__cell` hydration/call-order rule runs headlessly (set a
  hydration map, create cells in order, assert the Nth adopts the Nth value),
  matching the existing `goja` emit tests.
- **chromedp e2e** (skips if Chrome absent, matching the existing suite): load
  `examples/counter` under `dev`, increment the counter, rewrite a `.sigil`
  file's markup on disk, then assert the DOM updated **and** the counter value
  survived the swap.

## Out of scope (v1)

- Per-list-item local cell state preservation (scope "B").
- ES-module output / browser-native module reloading.
- Source maps, file-level dependency-graph-aware partial rebuilds (we rebuild the
  whole bundle each change — cheap at this scale).

## Affected code

- New: `internal/devserver/` (watcher, SSE, agent, shell), `internal/cli/dev.go`,
  dev-variant prelude + `Dev` option in `internal/emit`.
- Changed: `internal/cli/serve.go` (build-once/static), `internal/cli/root.go`
  (register `dev`). `internal/cli/compile.go` `bundle()` may grow a dev variant.
- Docs: `editor/`-style README note or a `docs/` entry on the `serve`/`dev` split
  and the v1 state-preservation caveat; update `CLAUDE.md` "What works / What's
  next".
