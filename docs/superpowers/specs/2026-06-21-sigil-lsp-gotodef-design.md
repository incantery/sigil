# `sigil lsp` type-aware slice 3b — go-to-definition (design)

Date: 2026-06-21
Status: approved, pre-implementation

## Goal

Editor support **#3 (type-aware), slice 3b**: `textDocument/definition` — jump
from a use of a name to where it was bound. This is the second of three #3 slices
(3a hover shipped; 3c semantic tokens remains). Per the brainstorm, this slice
takes the ambitious scope: it **adds source positions to binder AST nodes** (a
shared enabler that also unlocks hierarchical document symbols later) so that
go-to-def works for **locals, parameters, pattern binders, same-file top-level
definitions, AND imported names (cross-file, jumping into the dependency's
source — e.g. `std/ui`)**.

## What we reuse and the two gaps

- **Reuse:** the 3a `internal/analysis` position→node index (`Index`/`At`) to find
  the use node under the cursor; the `internal/lsp` path/overlay/dispatch
  machinery; `load.Program` (which already carries every module's `AST`, `File`,
  and public `Exports`).
- **Go-to-def needs NO type information** — it is purely structural (AST + lexical
  scope + imports), so it runs off a plain `load.Load` (no recording).
- **Gap 1 — binder nodes lack positions.** `VarParam` (`{Name}`), `VarPat`
  (`{Name}`), `RecordParamField`, `PatField`, and `Variant` carry no `Pos`, so a
  use of a parameter/pattern-local/constructor has no jump target today.
  `LetDecl`/`TypeDecl`/`Let`/`Import` already carry `Pos`.
- **Gap 2 — scope resolution is internal to the checker.** The checker resolves
  names correctly but exposes nothing per-use. We reimplement a bounded scope walk
  in `internal/analysis` rather than instrument the checker (keeps `internal/types`
  the clean nil-check recorder from 3a; analysis owns position queries).

## Decisions (settled during brainstorming)

1. **Scope option C** — add binder positions (parser change) to enable local
   go-to-def, not just top-level/imported.
2. **Include cross-file** (option B) — imported names jump into the dependency's
   source file.
3. **Resolver reimplemented in `internal/analysis`** (option A) — not by
   instrumenting the type checker. Go-to-def degrades gracefully on any scope
   edge (a wrong-but-plausible jump or no jump — never a type-safety issue).

## Architecture

### 1. Parser — binder positions

Add `Pos ast.Pos` to the five name-introducing binder nodes the resolver targets,
set by the parser from each binder's defining token:

- `VarParam` (function parameter name)
- `VarPat` (pattern variable binder)
- `RecordParamField` (record-param field binder)
- `PatField` (record-pattern field binder, incl. pun)
- `Variant` (ADT constructor)

`LetDecl`/`TypeDecl`/`Let`/`Import` already have `Pos`. This change is
backward-compatible: field-named struct literals get a zero-value `Pos`, and
`internal/ast/dump.go` omits positions, so the parse/AST dump tests stay green.
(`WildParam`/`WildPat` bind no referencable name → no position needed.)

### 2. `internal/analysis` — definition resolver

- `type Location struct { File string; Range Range }` — `File` is an absolute
  source path; `Range` is 1-based (LSP conversion happens in `internal/lsp`).
- `func Definition(prog *load.Program, line, col int) (Location, bool)`:
  1. `Index(prog.Entry.AST).At(line, col)` → the use node. Resolve only when it is
     a `*ast.Var` (lowercase use) or `*ast.Ctor` (constructor use); otherwise
     `ok == false`.
  2. **`*ast.Var name`** — resolve innermost-first:
     a. **Local scope:** a scope-tracking walk from each top-level decl body
        (seeded with that decl's parameters; `let rec` adds the decl name) down
        through `Lambda` params, `Let`/`let rec` bindings, and `Match`-arm
        patterns. The walk descends toward the cursor's use node (matched by
        pointer identity from step 1), accumulating in-scope binders; the
        innermost binder named `name` wins → its `Pos` (same file).
     b. **Same-file top-level:** a `LetDecl` named `name` → its `Pos`.
     c. **Imported:** see §3.
     First hit wins; none → `ok == false`.
  3. **`*ast.Ctor Name`** — a `Variant` named `Name`: same-file `TypeDecl`
     variants first, then imported (§3).
- Helpers: `paramBinders(param) []binder` and `patBinders(pattern) []binder`
  flatten a parameter/pattern into `{name string, pos ast.Pos}` binders, handling
  `VarPat`, `CtorPat` args, `TuplePat`/`ListPat` elements, and `RecordPat`/
  `RecordParam` field puns. `WildParam`/`WildPat` and literal patterns contribute
  nothing.

### 3. `load` — expose entry imports (cross-file)

Add a public accessor so analysis can reach the entry's resolved imports without
touching `load`'s internals:

- `type Import struct { Dep *Module; Names []string }` (`Names == nil` ⇒ bare
  import — all of `Dep`'s public values).
- `func (m *Module) Imports() []Import`.

For an imported name, the resolver finds the `Import` whose `Dep.Exports` make the
name visible (named in `Names`, or any public value/constructor for a bare
import / always-flowing constructors), then locates the matching public
`LetDecl`/`TypeDecl`/`Variant` in `Dep.AST` → `Dep.File` + that node's `Pos`.
(`Module.AST`/`.File`/`.Exports` are already public.)

### 4. `internal/lsp` — definition handler

- Advertise `definitionProvider: true` in `ServerCapabilities`.
- Add `DefinitionParams { TextDocument, Position }` and the LSP `Location
  { URI string; Range Range }` struct.
- `textDocument/definition` handler: derive path + overlay (reuse
  `uriToPath`/`docStore.overlay()`/`filepath.Abs`), `load.Load(path,
  Options{Root, Overlay})` — **no `Record`** — call `analysis.Definition(prog,
  pos.Line+1, pos.Character+1)`. On ok, reply an LSP `Location` with
  `URI: "file://" + loc.File` and `Range` converted to 0-based, spanning the
  binder name (`start = (Line-1, Col-1)`, `end = (Line-1, Col-1+len(name))`).
  On not-ok or a load error, reply `null` (never an error).

### Data flow (definition request)

`definition(uri,pos)` → server path+overlay → `load.Load` (no Record) →
`analysis.Definition` [index→use node; local scope walk | same-file top-level |
imported dep lookup] → `Location{File, Range}` → LSP `Location{uri, 0-based range}`
| `null`.

## Edge cases

- Cursor not on a `Var`/`Ctor` (whitespace, a literal, a keyword, or on a binder
  itself) → `null`.
- A name bound by a shadowing local resolves to the local binder, never the
  shadowed top-level/import (innermost-first scope walk).
- An unresolved name (e.g. an intrinsic `__x`, or a genuinely unbound name in a
  broken file) → `null`.
- A `load` error (broken import graph) → `null` (diagnostics report the error).
- `WildParam`/`WildPat` and literal patterns are never jump targets.

## Testing

- **Parser (`internal/parse`):** a fixture where a `VarParam` and a `VarPat`
  binder carry the correct 1-based `Pos` (column matches the source); a `Variant`
  carries its constructor position. Existing parse/dump tests stay green
  (positions omitted from dumps).
- **`internal/analysis`:**
  - go-to-def on a parameter use → the parameter binder's position;
  - on a `let`-local use → the local binding;
  - on a same-file top-level use → its `LetDecl` position;
  - on a constructor use → its `Variant` position;
  - on an imported name → a `Location` whose `File` is the dependency source and
    whose `Range` is the dep decl's position;
  - **shadowing:** a parameter shadowing a same-named top-level binding resolves
    to the parameter, not the top-level decl;
  - a cursor on whitespace / a literal → `ok == false`.
- **`internal/lsp` integration:** drive the server over the pipe harness —
  `definition` on a parameter use returns a same-file `Location` at the binder;
  `definition` on an imported name returns a `Location` with the dependency's
  `file://` URI. (Build an isolated temp root with a small dep module so the
  cross-file case resolves deterministically.)

## Out of scope (→ later)

Find-references; go-to-def invoked on a binder itself (returns nothing);
multi-location results; semantic tokens (3c); completion (#4); go-to-def on type
names in type annotations.

## Affected code

- Changed: `internal/ast/ast.go` (+`Pos` on the five binder nodes),
  `internal/parse/parse.go` (set those positions),
  `internal/load/load.go` (`Import` type + `Module.Imports()` accessor),
  `internal/lsp/` (definition handler, protocol structs, capability).
- New: `internal/analysis/definition.go` (resolver + scope walk + binder helpers)
  and its test.
- Docs: `editor/lsp.md` (go-to-definition added) + `CLAUDE.md` (#3 slice 3b done;
  3c remaining; note binder positions now exist → hierarchical symbols unblocked).
