# `sigil lsp` #4 — completion (design)

Date: 2026-06-22
Status: approved, pre-implementation

## Goal

Editor support **#4**: `textDocument/completion` — prefix-filtered identifier
completion. Typing a prefix offers the in-scope locals, top-level declarations,
selectively-imported names, and keywords, each tagged with a completion-item
kind. This is the last item on the editor roadmap; after it, the toolchain has
highlighting (#1), LSP foundation (#2), the type-aware trio (#3: hover,
go-to-def, semantic tokens), and completion.

## What we reuse and the key constraint

- **Reuse:** the `internal/lsp` doc store + dispatch + pipe-test harness; the 3b
  binder helpers (`paramBinders`/`patBinders`) for locals; the keyword set from
  `internal/token`; the role idea (3c) for kinds.
- **Key constraint — completion runs mid-edit.** Unlike hover/go-to-def/semantic
  tokens (which the user invokes at a settled point), completion fires while
  typing, so the buffer may not parse. v1 is **parse-based**: when the buffer
  parses, give full candidates; when it doesn't, give keywords only. This is
  useful in the common flow because a partial identifier (`ca|`) is itself a
  valid expression, so the file parses and candidates appear; the dead spots
  (right after `=`/`(`) are where there's nothing to complete yet.
- **Parse-only — no `load`, no type-check.** Imported names are read directly
  from the import statements' `Names` in the AST (selective imports list them),
  so completion never resolves deps or type-checks — it survives type errors,
  needing only a successful parse.

## Decisions (settled during brainstorming)

1. **Scope A** (parse-based identifier completion); member/after-`.` completion
   (needs type info) and bare-import all-names expansion (needs dep loading) are
   out of scope.
2. **Function-scoped locals**, not position-precise: the binders of the top-level
   declaration containing the cursor. Over-offering a sibling-branch local is
   harmless (the editor prefix-filters).
3. **Client-side filtering**: return the full deduped candidate set with
   `isIncomplete: false`; the editor filters by the typed prefix.

## Architecture

### 1. `internal/analysis` — candidate assembly (`completion.go`)

- `type CompletionKind int`: `CompFunction`, `CompVariable`, `CompType`,
  `CompConstructor`, `CompKeyword`.
- `type Candidate struct { Label string; Kind CompletionKind }`.
- `func Completions(text string, line, col int) []Candidate`:
  1. `parse.Module(text)`; on error → return just the keyword candidates
     (best-effort so completion isn't dead mid-edit).
  2. Otherwise gather (then dedup by label, first occurrence wins):
     - **Keywords** — each sigil keyword → `CompKeyword`.
     - **Top-level decls** — `*ast.LetDecl` with a name → `CompFunction` (has
       params) / `CompVariable`; `*ast.TypeDecl` name → `CompType`; each
       `*ast.Variant` → `CompConstructor`.
     - **Imported names** — for each `*ast.Import`, every name in `Names`:
       uppercase-initial → `CompType`, else `CompFunction`. (Bare imports —
       `Names` empty — contribute nothing in v1.)
     - **Locals** — find the top-level `*ast.LetDecl` with the greatest `Pos`
       that is `<= (line, col)` (the decl whose body the cursor is in); collect
       its parameter binders + every `let`/pattern binder in its body via a walk
       reusing `paramBinders`/`patBinders`. A bound name with params →
       `CompFunction`, else `CompVariable`. If no decl precedes the cursor, no
       locals.
  3. Return the deduped slice (order: locals, top-level, imports, keywords — the
     editor re-sorts/filters).

Keyword source: `token.Keywords()` (already exported — the sorted reserved-word
list the tree-sitter cross-check uses). No `internal/token` change needed.

### 2. `internal/lsp` — handler + protocol

- `ServerCapabilities.CompletionProvider *CompletionOptions` (`omitempty`); v1
  advertises an empty `CompletionOptions{}` (no `triggerCharacters`).
- `CompletionParams { TextDocument, Position }`; `CompletionItem { Label string;
  Kind int }`; `CompletionList { IsIncomplete bool; Items []CompletionItem }`.
- `textDocument/completion` handler: buffer text from the store →
  `analysis.Completions(text, pos.Line+1, pos.Character+1)` → map each
  `CompletionKind` to an LSP `CompletionItemKind` int (`CompFunction`→3 Function,
  `CompVariable`→6 Variable, `CompType`→7 Class, `CompConstructor`→20 EnumMember,
  `CompKeyword`→14 Keyword) → reply `CompletionList{IsIncomplete: false, Items}`.
  No `load`, no overlay — parse-only off the buffer text.

### Data flow

`completion(uri, pos)` → buffer text → `analysis.Completions(text, line, col)`
[parse → keywords ∪ top-level ∪ imports ∪ enclosing-decl locals, deduped] →
map kinds → `CompletionList`.

## Edge cases

- Parse failure → keywords only (never empty/error).
- Cursor at top level (no enclosing decl) → no locals (top-level + imports +
  keywords still offered).
- Duplicate labels (a local shadowing a top-level name, or a keyword colliding
  with a name) → deduped, first occurrence kept (locals first, so a local wins).
- Bare import (`import "std/ui"` with no name list) → contributes no names in v1.
- Empty document → keywords only.

## Testing

- **`internal/analysis`:**
  - A fixture: `import "std/ui" (card, button)`, a top-level `pub let app = …`, a
    `type Color = Red`, and `let inc n = let m = n in m` — `Completions` at a
    position inside `inc`'s body includes: `n`/`m` (CompVariable locals), `inc`
    (CompFunction), `app` (CompVariable), `Color` (CompType), `Red`
    (CompConstructor), `card`/`button` (CompFunction imports), and a keyword like
    `match` (CompKeyword); labels are deduped.
  - A parse-error buffer (`let x = (`) → only keyword candidates.
  - Locals are scoped: a position inside one top-level function does not surface
    another function's parameter.
- **`internal/lsp` integration:** drive the server over the pipe harness —
  `didOpen` a small program, `textDocument/completion` at a body position → the
  reply `Items` include the in-scope local, a top-level function, an imported
  name (`card`), and a keyword (`match`), with `isIncomplete:false`.

## Out of scope (→ follow-ups)

Member/after-`.` completion (record fields via `TypeInfo`); bare-import all-names
expansion (needs dep loading + Exports); position-precise scoping; completion
`detail` (type signatures) / documentation / `sortText`; trigger characters;
snippet/auto-import insert-text.

## Affected code

- New: `internal/analysis/completion.go` (`CompletionKind`, `Candidate`,
  `Completions`) + `internal/analysis/completion_test.go`.
- Changed: `internal/lsp/protocol.go` (capability + completion params/items/list),
  `internal/lsp/server.go` (capability value + dispatch + handler). (`internal/token`
  already exports `Keywords()` — no change there.)
- Docs: `editor/lsp.md` (completion added) + `CLAUDE.md` (#4 done → the editor
  roadmap is complete).
