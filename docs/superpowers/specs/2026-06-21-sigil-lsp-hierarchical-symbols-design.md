# `sigil lsp` — hierarchical document symbols (design)

Date: 2026-06-21
Status: approved, pre-implementation

## Goal

Upgrade the LSP's flat document-symbol outline so a `type` declaration shows its
members as nested child symbols: an ADT's constructors and a record's fields.
This closes the deferred follow-up from editor support #2 (document symbols were
flat because constructor/field nodes lacked positions) — now unblocked because
slice 3b added `Variant.Pos`, leaving only `FieldType.Pos` to add here.

## What we reuse and the one gap

- **Reuse:** the existing `internal/lsp/symbols.go` (`documentSymbols(text)`),
  the `textDocument/documentSymbol` handler (already wired in #2), and `Variant.Pos`
  (added in slice 3b).
- **Gap:** the record type field node `ast.FieldType` (`{Name, Type}`) has no
  `Pos`. Add it (a one-node parser change, mirroring 3b's binder positions).
- **LSP containment rule:** a hierarchical `DocumentSymbol`'s child ranges MUST be
  contained within the parent's `range`. So a type symbol's `range` must span from
  its position to its furthest member, not just the name.

## Decisions (settled during brainstorming)

1. Only `TypeDecl` gains children; `LetDecl` (functions/values) stay leaf symbols.
2. ADT constructors → `EnumMember` children; record fields → `Field` children.
3. `selectionRange` stays keyword-based (at `TypeDecl.Pos`), consistent with the
   current flat behavior — exact type-name position is deferred (would need a
   `TypeDecl` name position).

## Architecture

### 1. Parser — `FieldType.Pos`

Add `Pos ast.Pos` as the first field of `ast.FieldType`, set at
`internal/parse/parse.go:296` from the field-name token (`pos(name)`). Additive
and dump-safe (the AST dump omits positions, so parse/dump tests stay green);
no checker/emitter change.

### 2. Protocol — nesting (`internal/lsp/protocol.go`)

- Add `Children []DocumentSymbol \`json:"children,omitempty"\`` to `DocumentSymbol`.
- Add kind constants: `SymbolKindField = 8`, `SymbolKindEnumMember = 22`.

### 3. `symbols.go` — hierarchical types

- `LetDecl` (named): unchanged — leaf `Function` (has params) or `Variable`.
- `TypeDecl`:
  - **ADT** (variants present): parent kind `Enum`; one `EnumMember` child per
    `Variant`, each ranged at `Variant.Pos` spanning the constructor name.
  - **Record** (`Record != nil`): parent kind `Struct`; one `Field` child per
    `FieldType`, each ranged at `FieldType.Pos` spanning the field name.
  - **Parent containment:** the type symbol's `range` runs from `TypeDecl.Pos` to
    the furthest child's end (computed by max over child ranges); its
    `selectionRange` is the name span at `TypeDecl.Pos` (`[Pos, Pos+len(name)]`).
    `selectionRange ⊆ range` holds because members follow the name. A type with
    no members falls back to the flat name span (range == selectionRange).
- Child symbols have `range == selectionRange` (the member name span); members
  have no further children.
- Helper: a small `maxPos(a, b)` / range-union to compute the parent end across
  (possibly multi-line) members.

### Data flow

`documentSymbol(uri)` → `documentSymbols(text)` → parse → per decl: leaf for
`LetDecl`, parent+children for `TypeDecl` → `[]DocumentSymbol` (with `Children`)
→ reply (existing handler, unchanged).

## Edge cases

- Parse failure → empty list (unchanged; the diagnostic reports the error).
- A destructuring top-level `let (a,b) = …` (Name == "") → skipped, as today.
- A type with zero members (not expressible for ADT/record in practice) → flat
  symbol, no children (range == selectionRange).
- `selectionRange` for a type is keyword-based (at `type`/`pub`), so the outline's
  clickable name region starts at the keyword — a documented v1 limitation.

## Testing

- **Parser:** a record field's `FieldType.Pos` matches its source column
  (`type T = { x: Int }` → `x` at the expected column). Existing parse/dump tests
  stay green.
- **`internal/lsp` (`documentSymbols` unit test):**
  - ADT `type Color = Red | Green | Blue` → one `Enum` symbol named `Color` with
    three `EnumMember` children (`Red`/`Green`/`Blue`) at the constructor columns.
  - Record `type Point = { x: Int, y: Int }` → one `Struct` symbol with two `Field`
    children (`x`, `y`).
  - `let f x = x` → a leaf `Function` symbol with no children.
  - Containment: each child's range is contained within its parent's range.
  - The existing #2 flat-symbol test (the counter example) still passes
    (functions/values are unchanged leaves).

## Out of scope

Function/local/parameter symbols as children (functions stay leaves); exact
type-name `selectionRange` (stays keyword-based); semantic tokens (slice 3c);
completion (#4).

## Affected code

- Changed: `internal/ast/ast.go` (`FieldType.Pos`), `internal/parse/parse.go`
  (set it), `internal/lsp/protocol.go` (`Children` + two kind consts),
  `internal/lsp/symbols.go` (hierarchical build).
- Docs: `editor/lsp.md` (symbols now hierarchical) + `CLAUDE.md` (the #2
  hierarchical-symbols follow-up is done).
