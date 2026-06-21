# `sigil lsp` type-aware slice 3c — semantic tokens (design)

Date: 2026-06-21
Status: approved, pre-implementation

## Goal

Editor support **#3 (type-aware), slice 3c**: `textDocument/semanticTokens/full`
— type-aware syntax coloring that classifies every non-comment token by role,
the unique value being disambiguation a plain grammar can't do: `type` vs
`constructor` (both uppercase), and `function` vs `parameter` vs `variable`
(all lowercase). This completes the type-aware #3 trio (3a hover, 3b go-to-def
shipped); only #4 completion remains afterward.

## What we reuse and the gaps

- **Reuse:** the `internal/lsp` document store, dispatch, and pipe-test harness;
  the existing `internal/lex` token stream (positions + kinds); the binder
  positions added across 3a/3b/symbols (`Variant`, `VarParam`, `VarPat`,
  `FieldType`, `Field`, `RecordParamField`, `PatField`).
- **No type information needed** — roles are determined structurally (by syntactic
  context), so this runs off a plain `parse.Module(text)` (no `load`, no imports,
  no checker).
- **Gaps (parser additions):** declaration names and constructor patterns lack
  positions. Add `NamePos` to `LetDecl` and `TypeDecl` (the name, not the
  keyword) and `Pos` to `CtorPat` (constructor patterns). Same additive,
  dump-safe pattern as prior slices.
- **Comments are dropped by the lexer** (`skipLineComment`) — they cannot be
  colored by semantic tokens; the TextMate/tree-sitter grammars (#1) handle them.

## Decisions (settled during brainstorming)

1. **Full coverage** (option B): classify every non-comment token — identifiers by
   role plus the lexical categories (`keyword`/`operator`/`number`/`string`).
2. **Heuristic use classification** (option A): a lowercase *use* colors
   `function` if its name is in the file's set of function names (any `let` with
   params), else `variable`. Parameter *uses* color `variable` (not `parameter`);
   declaration sites are always precise. No scope stack; shadowing edge cases
   mis-color (rare, cosmetic).

## Architecture

### 1. Parser — anchor positions

Add (each as the relevant node's name/start position, set from the identifier
token; additive and dump-safe):
- `LetDecl.NamePos ast.Pos` — the bound name (the `IDENT` after `let`).
- `TypeDecl.NamePos ast.Pos` — the type name (the `UIDENT` after `type`).
- `CtorPat.Pos ast.Pos` — the constructor name in a pattern.

`LetDecl`/`TypeDecl` already carry `Pos` at the keyword; `NamePos` is additional.
(`NamePos` also retroactively sharpens go-to-def/document-symbol precision, though
wiring those is out of scope here.)

### 2. `internal/analysis` — role classifier

`type Role int` with values `RoleType`, `RoleEnumMember`, `RoleFunction`,
`RoleParameter`, `RoleVariable`, `RoleProperty`.

`func SemanticRoles(m *ast.Module) map[ast.Pos]Role` — a context-aware AST walk
producing a role for each identifier occurrence it classifies. It first collects
`functionNames` (the set of names of every `let`/`LetDecl`/`Let` with params),
then walks declarations, type expressions, expressions, and patterns:

- **type names:** `TypeDecl.NamePos`; type-expression constructors (named types in
  annotations / variant args) → `RoleType`.
- **constructors:** `Variant` decls, `Ctor` expressions, `CtorPat` patterns →
  `RoleEnumMember`.
- **record fields:** `FieldType` decls; `.field` access (`ast.Field.Pos`) →
  `RoleProperty`.
- **declarations:** `LetDecl.NamePos` → `RoleFunction` (has params) else
  `RoleVariable`.
- **parameters:** `VarParam` → `RoleParameter`; pattern binders (`VarPat`,
  record-pattern puns) → `RoleVariable`.
- **lowercase uses:** `ast.Var` → `RoleFunction` if its name ∈ `functionNames`,
  else `RoleVariable`.

The map is keyed by `ast.Pos` (1-based line/col), which matches the lexer's token
positions exactly (parser consumes the lexer's tokens), so the encoder can look
up an identifier token's role by its position.

### 3. Token encoder

`func SemanticTokens(text string) []uint` (in `internal/analysis`):
1. `parse.Module(text)` → on parse error return an empty slice (no tokens; the
   diagnostic already reports the error). Build the role map via `SemanticRoles`.
2. `lex.Lex(text)` → the positioned token stream.
3. For each token, compute its semantic token type (an index into the legend):
   - identifier (`IDENT`/`UIDENT`/`HOLE`): role-map lookup by position; on a miss,
     fallback — `UIDENT` → `type`, lowercase → `function`/`variable` via
     `functionNames`.
   - keyword kinds (`LET`..`EFFECT`) → `keyword`.
   - operator kinds (`PIPEFWD`..`BANG`, plus `EQ`/`ARROW`/`PIPE`) → `operator`.
   - `INT`/`FLOAT` → `number`; `STRING` → `string`.
   - layout (`NEWLINE`/`INDENT`/`DEDENT`), punctuation (`LPAREN`..`SEMI` except the
     operators above), `EOF`, `UNDERSCORE` → skipped (no token emitted).
4. Tokens are already in source order; **delta-encode** to `[]uint`, 5 ints per
   emitted token: `deltaLine` (line − prevLine), `deltaStartChar` (col − prevCol
   on the same line, else absolute col), `length`, `tokenType` (legend index),
   `tokenModifiers` (always 0). Positions converted 1-based → 0-based here.

The legend (token-type order; indices used above):
`["type","enumMember","function","parameter","variable","property","keyword","operator","number","string"]`.

### 4. `internal/lsp` — protocol + handler

- `ServerCapabilities.SemanticTokensProvider` advertising the legend
  (`tokenTypes` = the list above, `tokenModifiers` = `[]`) and `full: true`.
- `textDocument/semanticTokens/full` handler: get the doc text from the store,
  call `analysis.SemanticTokens(text)`, reply `{ data: []uint }` (an empty `data`
  on parse failure). No `load`, no overlay-vs-disk subtlety — it parses the
  in-memory buffer text directly.

### Data flow

`semanticTokens/full(uri)` → doc text → `analysis.SemanticTokens(text)`
[parse → `SemanticRoles` map → lex → per-token type → delta-encode] →
`{ data: []uint }`.

## Edge cases

- Parse failure → empty `data` (the diagnostic reports the error).
- A token whose identifier the walk didn't classify → the lexical fallback
  (uppercase → `type`, lowercase → `function`/`variable`), never unclassified.
- String interpolation: a `STRING` token is colored `string` as a whole; the
  interpolated `${…}` holes are not separately re-classified in v1.
- Empty document / comment-only lines → empty `data` (no non-comment tokens).
- `__intrinsic` (`HOLE`) identifiers → treated as lowercase uses (fallback
  `variable`, or `function` if the name is in `functionNames`).

## Testing

- **Parser:** `LetDecl.NamePos`, `TypeDecl.NamePos`, `CtorPat.Pos` carry the
  correct 1-based column. Existing parse/dump tests stay green.
- **`internal/analysis` roles:** a fixture exercising each role — `type Color =
  Red` (`Color`→type, `Red`→enumMember), `let f x = x` (`f`→function,
  `x`-param→parameter, `x`-use→variable), a `.field` access (→property), a `match`
  with `| Some y ->` (`Some`→enumMember via `CtorPat`, `y`→variable), a use of a
  top-level function (→function) — asserted by position.
- **`internal/analysis` encoder:** a small known source → the exact `[]uint`
  (verifying delta-line/char arithmetic, lengths, legend indices, and the
  keyword/operator/number/string mapping); a multi-line case verifying
  `deltaLine` and the absolute-vs-relative `deltaStartChar` reset across lines.
- **`internal/lsp` integration:** drive the server over the pipe harness —
  `initialize` advertises `semanticTokensProvider` (legend present); `didOpen`
  then `textDocument/semanticTokens/full` → a non-empty `data` array; a parse-error
  document → empty `data` (no hang/crash).

## Out of scope (→ later / #4)

Comments; record-*literal* field names and block-`let` names (use the lexical
fallback); semantic-token **modifiers** (e.g. `declaration`/`readonly`);
**range** and **delta** semantic-token requests (only `full`); parameter *uses*
colored as `parameter` (they color `variable`); interpolation-hole
re-classification; completion (#4).

## Affected code

- Changed: `internal/ast/ast.go` (`NamePos` on `LetDecl`/`TypeDecl`, `Pos` on
  `CtorPat`), `internal/parse/parse.go` (set the three), `internal/lsp/protocol.go`
  (capability + params/result structs), `internal/lsp/server.go` (capability value
  + dispatch + handler).
- New: `internal/analysis/semantic.go` (`Role`, `SemanticRoles`, `SemanticTokens`)
  + `internal/analysis/semantic_test.go`.
- Docs: `editor/lsp.md` (semantic tokens added) + `CLAUDE.md` (3c done → type-aware
  trio complete; only #4 completion remains).
