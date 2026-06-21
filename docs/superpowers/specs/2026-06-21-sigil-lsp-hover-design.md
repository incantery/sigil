# `sigil lsp` type-aware slice 3a — analysis core + hover (design)

Date: 2026-06-21
Status: approved, pre-implementation

## Goal

Editor support **#3 (type-aware), slice 3a**: build the shared analysis core —
per-node inferred types + a position→node index — and ship **hover** on top.
This is the first of three slices that decompose #3 (3b = go-to-definition,
3c = semantic tokens), each its own spec→plan→build cycle. 3a deliberately builds
the hardest new machinery (capturing per-node types the checker currently
discards, and locating the node under a cursor when nodes have no end positions)
and proves it end-to-end with a visible feature.

## What the compiler gives us today (and the two gaps)

- `types.Check` / `types.CheckModule` (`internal/types/infer.go:146,170`) return
  only **top-level binding schemes** (`name → type`) or `Exports`. The central
  `infer(e ast.Expr, env) (Type, error)` (`infer.go:659`) computes a type for
  **every** expression node but discards it as the recursion unwinds.
  → **Gap 1:** no per-node types. We must record them and **zonk** (resolve
  through the final substitution) — mid-inference types still hold unbound
  unification vars (`TVar`).
- AST nodes carry only a **start** `ast.Pos` (1-based Line/Col), no end.
  → **Gap 2:** no node ranges, so "the node under the cursor" needs computed
  extents.
- AST expression nodes are pointers (`*ast.Ident`, `*ast.App`, …), so a
  `map[ast.Expr]Type` keyed by node identity is sound.

## Decisions (settled during brainstorming)

1. **Decompose #3** into slices; 3a = analysis core + hover.
2. **Hover content = level B:** an identifier renders `name : type`; a binding
   occurrence (top-level `let`, etc.) renders its **generalized scheme**; any
   other expression renders its inferred type. No doc comments (deferred).
3. **Instrument the existing checker** to record node→type (optional sink, zero
   overhead when off) — never a second, drifting type implementation.
4. **Record only the focused file**, not the whole program — `load` checks deps
   normally; we re-check the open file's AST with recording on, seeding deps'
   exports.
5. **New `internal/analysis` package** owns the position→node index and hover
   logic; `internal/lsp` stays protocol glue.
6. **Structural extents** for the position→node index: a node's range is
   `[node.Pos, max(child ends)]`; a leaf (identifier/literal) ends at
   `Pos + len(text)`.

## Architecture

### 1. `internal/types` — recording sink

- A recorder interface, e.g. `type Recorder interface { Record(ast.Expr, Type) }`,
  optionally held on the `Checker` (`c.rec`, nil by default). At each node,
  `infer` does `if c.rec != nil { c.rec.Record(e, t) }`. This is the only change
  to the hot path and it is a nil-check no-op in normal checking.
- New entry point:
  `CheckModuleRecording(m *ast.Module, deps *Exports) (*Exports, *TypeInfo, error)`.
  `TypeInfo` holds:
  - `Nodes map[ast.Expr]Type` — **zonked** (every recorded type resolved through
    the final substitution after inference completes).
  - the top-level binding schemes (so a binding occurrence renders its
    generalized scheme, not its monomorphic instance).
  - a printer: `TypeInfo.StringOf(node) (string, bool)`.
- Existing `Check` / `CheckModule` are byte-for-byte unchanged (rec == nil).
  Zonking reuses the existing substitution/prune logic already in the checker.

### 2. `internal/analysis` — position→node + hover

- `func Index(m *ast.Module) *NodeIndex` — walks the AST, computing each node's
  structural extent, and supports
  `func (*NodeIndex) At(line, col int) (ast.Expr, bool)` returning the **smallest
  expression node** whose extent contains the position. Leaf extents come from
  `len(name)` / `len(raw)`; composite extents from the max of child ends.
- `func Hover(prog *load.Program, focusFile string, line, col int) (Result, bool)`
  where `Result = { Markdown string; Range analysis.Range }`. It:
  1. reads `prog.EntryInfo` (the focused module's `*types.TypeInfo`, populated by
     `load` when loaded with recording — see §3);
  2. builds the `NodeIndex` over `prog.Entry.AST`, finds the node at `(line,col)`;
  3. looks up the node's zonked type; formats per **level B**; returns the node's
     range. Returns `ok == false` when there is no node, or no type for it.

`load` owns the dep-merging + recording (it already orchestrates per-module
checking), so analysis never re-implements the merge: it consumes the `TypeInfo`
`load` produced.

### 3. `internal/load` — recording option

- Add `Options.Record bool`. When set, `load` checks the **entry** module via
  `types.CheckModuleRecording` (deps still use the normal `CheckModule`), and
  stores the result on `Program.EntryInfo *types.TypeInfo` (nil when `Record` is
  false). This keeps the dep-merge logic in `load` and hands analysis a ready
  `TypeInfo`. No change to the non-recording path — existing `Bundle`/`check`/
  `serve`/`dev`/diagnostics callers pass `Record: false` (the zero value) and are
  unaffected.

### 4. `internal/lsp` — wiring + protocol

- Advertise `hoverProvider: true` in `ServerCapabilities`.
- Add `HoverParams` (text-document + position), `Hover` (`{ contents, range }`),
  `MarkupContent` (`{ kind: "markdown", value }`).
- `textDocument/hover` handler: derive the path + build the overlay (reusing the
  Task-5 `uriToPath`/`docStore.overlay()` machinery), `load.Load(path,
  Options{Root, Overlay, Record: true})`, call `analysis.Hover`, and reply the
  `Hover` object — or `null` when `ok` is false.

### Data flow (hover request)

`hover(uri,pos)` → server builds overlay + `load.Load(…, Record: true)` (which
records the entry module → `Program.EntryInfo`) → `analysis.Hover` reads
`EntryInfo` → `NodeIndex.At` → zonked type → format B →
`{contents: markdown, range}` | `null`.

## Edge cases

- Cursor on whitespace / a keyword / punctuation / a node with no recorded type
  → reply `null` (never an error).
- File fails to type-check → best-effort: the parse still yields a `NodeIndex`,
  but some nodes have no zonked type; return `null` for those rather than
  guessing. (If `CheckModuleRecording` returns an error, hover may still answer
  for nodes whose types were resolved before the error, or return `null`
  throughout — either is acceptable; never error to the client.)
- Parse failure → `null` (the diagnostic from #2 already reports it).
- Generalized vs monomorphic: a binding occurrence renders the generalized
  scheme from `TypeInfo`'s top-level schemes; any other occurrence renders the
  node's zonked (monomorphic-at-use) type.

## Testing

- **`internal/types`:**
  - Recording captures a type for every expression node of a small fixture
    (count of recorded nodes matches the expression count).
  - Zonk resolves unification vars: a fixture where a node's type is only
    determined by later unification renders the final concrete type, not a raw
    `TVar`.
  - Golden: `Check` / `CheckModule` output unchanged (no regression from the
    nil-recorder hot-path branch).
- **`internal/analysis`:**
  - `NodeIndex.At` returns the expected smallest node for cursors at several
    positions inside a nested expression (e.g. on `count`, on `1`, and on the
    `+` region of `count + 1`).
  - `Hover` renders `count : Int` for a local identifier, the generalized scheme
    for a top-level `let` binding occurrence, the type for a compound
    sub-expression, and `ok == false` on whitespace.
- **`internal/lsp` integration:** drive the server over the existing pipe harness
  — `initialize` → `didOpen` → `textDocument/hover` at a known `(line,col)` →
  assert the reply's markdown contains the expected `name : type`; a hover on
  whitespace → `null` result.

## Out of scope (→ 3b / 3c / later)

Go-to-definition (needs the definition resolver — slice 3b), semantic tokens
(role classification — slice 3c), hover on type annotations and patterns, doc
comments, whole-program per-node type caching.

## Affected code

- New: `internal/analysis/` (index + hover + tests).
- Changed: `internal/types/` (recorder sink + `CheckModuleRecording` + `TypeInfo`;
  existing entry points untouched), `internal/load/` (`Options.Record` +
  `Program.EntryInfo`; non-recording path unchanged), `internal/lsp/` (hover
  handler, protocol structs, capability).
- Docs: update `editor/lsp.md` (hover added) + `CLAUDE.md` (#3 slice 3a done;
  3b/3c remaining).
