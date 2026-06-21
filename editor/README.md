# Editor support for Sigil

## Neovim (tree-sitter)
1. `make nvim-install` — builds the parser and copies the parser, queries, and
   ftdetect into your nvim site directory.
2. Ensure tree-sitter highlighting is on (`vim.treesitter.start()` for filetype
   `sigil`, or your nvim-treesitter config). Open any `.sigil` file.

## VS Code (TextMate)
1. `make vscode-ext` — packages `editor/vscode-sigil/*.vsix`.
2. `code --install-extension editor/vscode-sigil/<file>.vsix`.

## Maintenance
- `make tree-sitter-test` — grammar corpus tests.
- `make tree-sitter-verify` — parse all std/ + examples/; fail on ERROR nodes.
- `go test ./internal/token/` — keyword cross-check (grammar vs language).

The tree-sitter grammar (nvim) and TextMate grammar (VS Code) are separate; keep
them aligned — the drift guards catch grammar/language divergence.
