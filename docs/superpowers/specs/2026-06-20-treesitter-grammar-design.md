# Tree-sitter grammar + editor highlighting (editor support #1)

*Status: approved design, ready for planning. Date: 2026-06-20.*

## Context

The old kernel's `editor/` (tree-sitter grammar, nvim files, VS Code extension) and
`pkg/lang/lsp` were deleted with the superseded kernel. We are rebuilding editor
support for the current language (`internal/` + `std/`) as a sequence of
independently-shippable sub-projects:

1. **Tree-sitter grammar + highlighting** ← *this spec*
2. LSP foundation: diagnostics + document symbols
3. Type-aware: hover + go-to-definition + semantic tokens (needs new analysis in `internal/`)
4. Completion

This spec covers **only #1**. The old `editor/` and `pkg/lang/lsp` exist in git
history as reference, but the old tree-sitter grammar mirrored the *old* parser
surface (views/components/themes/stream-ops) — none of which exist in the new
ML-expression core — so the grammar is rewritten against `docs/grammar.md` and
`internal/parse`. Two pieces of old scaffolding transfer in spirit: the external
scanner's token model (INDENT/DEDENT/NEWLINE) and the `.scm` query conventions.

## Goal

Syntax highlighting + folding for `.sigil` files in **Neovim** (native
tree-sitter) and **VS Code** (TextMate grammar), kept honest by drift guards that
parse the real corpus and cross-check the keyword set.

## Decisions (settled during brainstorming)

- **Editors:** Neovim and VS Code.
- **VS Code highlighting:** TextMate grammar (`sigil.tmLanguage.json`), not
  tree-sitter — VS Code has no native tree-sitter highlighting and the extension
  route is non-standard. Neovim uses the tree-sitter grammar natively. This means
  two grammars; the drift guards mitigate divergence.
- **No LSP** anything in this sub-project (that is #2–#4).
- **Generated tree-sitter files are committed** (`parser.c`, `grammar.json`,
  `node-types.json`) so consumers install without the tree-sitter CLI — standard
  for grammar repos. Build outputs (`*.so`, `node_modules/`) are gitignored.
- **`editor/` lives at the repo root** — it is editor assets, not Go packages.

## Structure

```
editor/
├── README.md                         install instructions (nvim + VS Code)
├── tree-sitter-sigil/
│   ├── grammar.js                    grammar for the new ML-core syntax
│   ├── src/scanner.c                 external scanner: NEWLINE/INDENT/DEDENT
│   ├── src/parser.c, src/grammar.json, src/node-types.json   (generated, committed)
│   ├── queries/highlights.scm
│   ├── queries/folds.scm
│   ├── queries/injections.scm
│   ├── queries/locals.scm
│   ├── test/corpus/*.txt             tree-sitter grammar unit tests
│   ├── package.json
│   ├── tree-sitter.json
│   └── .gitignore                    *.so, node_modules/, build/
├── nvim/
│   └── ftdetect/sigil.lua            *.sigil → filetype=sigil
└── vscode-sigil/
    ├── package.json                  extension manifest (language contribution)
    ├── language-configuration.json   comments, bracket pairs, indent rules
    ├── syntaxes/sigil.tmLanguage.json  TextMate grammar (VS Code highlighting)
    ├── README.md
    └── .vscodeignore
```

## Grammar + external scanner

`grammar.js` is a **structural** grammar — it mirrors `docs/grammar.md` /
`internal/parse`'s surface for editor awareness (highlighting, folding, motions),
and is deliberately laxer than the real parser where strictness buys editors
nothing. It does not enforce semantic rules (the type checker does that).

Node coverage (from `docs/grammar.md`):
- Declarations: `let` / `let rec` / `pub let`, `import "path" (names) [as alias]`,
  `type` declarations with data constructors.
- Expressions: `fun` lambdas, application `f x` (left-assoc), binary operators by
  the precedence ladder in `docs/grammar.md` (`|>`, `||`, `&&`, comparisons, `++`,
  `+ -`, `* /`, etc.), `if/then/else`, `match … with` arms (`| pat -> expr`,
  optional `if` guards), `let … in`, field access `e.name`, parenthesized/list/
  record literals.
- Lexical: `INT`, `FLOAT`, `STRING` with `${expr}` interpolation, `IDENT`
  (lowercase-initial = value), `UIDENT` (uppercase-initial = type/constructor),
  `HOLE` (`__` intrinsics like `__cell`), `//` line comments, the keyword set
  `let rec pub import as type fun if then else match with of`.

The **external scanner** (`src/scanner.c`) replicates the new lexer's offside rule
(`internal/lex/lex.go`): an indentation stack synthesizes `INDENT` / `DEDENT` /
`NEWLINE`; layout is **suppressed inside brackets** `( ) [ ] { }`; blank lines and
`//`-comment-only lines are skipped before measuring the next line's indent. The
old `src/scanner.c` is the structural starting point (identical token model),
rewritten to the new rules.

## Highlighting: two mechanisms

- **Neovim:** the tree-sitter parser + `queries/highlights.scm`, using
  nvim-treesitter capture conventions (`@keyword`, `@type`, `@constructor`,
  `@function`, `@string`, `@number`, `@comment`, `@operator`,
  `@variable`, `@punctuation.*`). Capitalization drives `@type`/`@constructor`
  (UIDENT) vs `@variable`/`@function` (IDENT); `HOLE` → `@function.builtin`.
- **VS Code:** `sigil.tmLanguage.json` — a TextMate grammar matching the same
  token classes with regex rules (keywords, UIDENT vs IDENT, `__hole` builtins,
  strings + `${…}` interpolation, numbers, `//` comments, operators).
- `queries/folds.scm` folds indented blocks; `queries/injections.scm` injects
  `sigil` into `${…}` string-interpolation regions; `queries/locals.scm` provides
  a minimal scope model (function params, `let` bindings) for local highlighting.

## Drift guards + testing (the backbone)

1. **`tree-sitter test`** — corpus unit tests in `test/corpus/*.txt` covering each
   construct (decls, lambdas, match, application, operators, strings/interpolation,
   layout/indentation). Run via `make tree-sitter-test`.
2. **`make tree-sitter-verify`** — parse every `std/*.sigil` and
   `examples/**/*.sigil` with `tree-sitter parse`; **fail on any ERROR node**.
   This catches grammar drift from the real language. (Skips gracefully with a
   clear message if the tree-sitter CLI is unavailable.)
3. **Go keyword-coverage test** — a pure-Go test (no tree-sitter CLI) that reads
   the language's actual keyword set from `internal/token`/`internal/lex` and
   asserts the grammar's keyword list (parsed from `grammar.js` or a checked-in
   keyword manifest) is a superset. Adding a keyword to the language then fails CI
   until the grammar is updated. Lives where the language keyword set is defined
   (e.g. `internal/lex` or a small `editor`-focused test package) — the
   implementation plan picks the exact home and the extraction mechanism.
4. **Manual verification** — load a representative `.sigil` file (e.g.
   `examples/counter/counter.sigil`) in nvim and in VS Code (via the extension);
   confirm keywords, types/constructors, strings, interpolation, intrinsics, and
   comments highlight correctly, and that indented blocks fold.

## Build / packaging (Makefile targets to re-add)

- `tree-sitter` — regenerate the parser from `grammar.js` (pin the CLI version for
  reproducible `parser.c`).
- `tree-sitter-test` — run corpus tests.
- `tree-sitter-verify` — the drift guard above.
- `nvim-install` — install the compiled parser + queries + `ftdetect/sigil.lua`
  into the user's nvim site directory.
- `vscode-ext` — package the VS Code extension into a `.vsix`.

`README.md` documents both install paths (nvim: parser build + queries +
lspconfig-free highlighting; VS Code: install the `.vsix`).

## Out of scope

- Anything LSP — diagnostics, hover, go-to-def, completion, semantic tokens
  (sub-projects #2–#4).
- A tree-sitter integration for VS Code (TextMate is the chosen path).
- Formatting, DAP, snippets beyond basic language-configuration.
- Publishing to the VS Code Marketplace / nvim plugin registries (local install
  only for now).

## Verification (done = all green)

- `make tree-sitter-test` passes (corpus).
- `make tree-sitter-verify` parses all `std/` + `examples/` `.sigil` with zero
  ERROR nodes.
- `go test ./...` stays green, including the new keyword-coverage test.
- Manual: highlighting confirmed in nvim and VS Code on `examples/counter`.
