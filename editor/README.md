# Sigil editor support

Two layers, by design:

1. **tree-sitter grammar** (`tree-sitter-sigil/`) — instant lexical +
   structural highlighting, folding, locals. Mirrors the Go parser's
   surface; `make tree-sitter-verify` parses every example app and fails
   on drift, and `pkg/lang/lsp/grammar_sync_test.go` cross-checks the
   keyword and builtin-component lists against the compiler.
2. **`sigil lsp`** — the language server built into the CLI. The same
   parse + lower pipeline as `sigil check` provides diagnostics on every
   keystroke, plus compiler-accurate semantic tokens (builtin kind vs
   user component, state cells, params, tones), document symbols,
   go-to-definition, and hover. No separate install: anywhere the
   `sigil` binary is, the language server is.

`sigil fmt` exists but is deliberately **not** wired into the LSP as
formatting: the v0 formatter drops comments, and format-on-save must
never destroy them. Wire it up once the formatter round-trips comments.

## VS Code

```sh
make vscode-ext          # packages editor/vscode-sigil/*.vsix
code --install-extension editor/vscode-sigil/sigil-lang-0.1.0.vsix
```

The extension ships a TextMate grammar for instant coloring and starts
`sigil lsp` for everything else. If the binary isn't on VS Code's PATH,
set `sigil.serverPath` (e.g. `~/go/bin/sigil`).

## Neovim (0.10+)

```sh
make nvim-install        # parser .so + queries + ftdetect into ~/.local/share/nvim/site
```

Then enable highlighting and the LSP in your config:

```lua
-- Highlighting (tree-sitter)
vim.api.nvim_create_autocmd("FileType", {
  pattern = "sigil",
  callback = function()
    vim.treesitter.start()
  end,
})

-- Language server (0.11+ style)
vim.lsp.config("sigil", {
  cmd = { "sigil", "lsp" },
  filetypes = { "sigil" },
  root_markers = { "sigil.mod", ".git" },
})
vim.lsp.enable("sigil")
```

On 0.10 with nvim-lspconfig, register the same `cmd`/`filetypes` via
`lspconfig.configs` instead.

## Helix

Add to `~/.config/helix/languages.toml`:

```toml
[language-server.mako]
command = "sigil"
args = ["lsp"]

[[language]]
name = "sigil"
scope = "source.mako"
file-types = ["sigil"]
comment-token = "//"
indent = { tab-width = 2, unit = "  " }
language-servers = ["sigil"]

[[grammar]]
name = "sigil"
source = { path = "/path/to/sigil/editor/tree-sitter-sigil" }
```

Then `hx --grammar build` and copy `tree-sitter-sigil/queries/*.scm` to
`~/.config/helix/runtime/queries/sigil/`.

## Keeping it in sync

When the language grows:

- `make tree-sitter` regenerates the parser from `grammar.js` (tree-sitter
  CLI, or npx fallback — no global install needed) **and rebuilds
  `tree-sitter-sigil/sigil.so`**. The rebuild matters: Neovim loads that
  `.so` directly while reading the query files live, so a grammar change
  that didn't rebuild it leaves nvim on a stale parser and the highlight
  queries error on the new nodes (e.g. `flow_decl`). After regenerating,
  **restart nvim** — it caches the loaded parser for the session.
- `make tree-sitter-test` runs the corpus + validates the queries.
- `make tree-sitter-verify` parses every `examples/**/*.mako`.
- `go test ./pkg/lang/lsp/` includes the drift guards: decl keywords in
  grammar + highlights, and the builtin-kind list in `highlights.scm`
  against `lower.BuiltinKinds()`.

The LSP's semantic tokens come straight from the compiler's AST, so
they never need manual syncing.
