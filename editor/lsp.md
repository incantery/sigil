# `sigil lsp` — language server

`sigil lsp` is a stdio Language Server Protocol server giving **live
diagnostics** and **document symbols** for `.sigil` files. It is a thin layer
over the compiler (`internal/load` + `internal/types`); the JSON-RPC base
protocol is hand-rolled (no external LSP dependency).

## What it provides (v1)

- **Diagnostics** — type/parse/lex errors as you type (the editor's unsaved
  buffer is type-checked via an in-memory overlay). One diagnostic per file:
  the compiler stops at the first error, so fix it and the next appears.
- **Document symbols** — top-level `let`/`type` declarations for the outline /
  symbol picker, with `type` declarations **nested**: an ADT shows its
  constructors and a record shows its fields as child symbols.
- **Hover** — the inferred type of the expression under the cursor
  (`name : type`; a top-level binding shows its generalized scheme, e.g.
  `map : (a -> b) -> List a -> List b`). Powered by a per-node type record
  captured from the checker.
- **Go to definition** — jump from a use of a name to its binder: a parameter,
  a local `let`, a pattern binder, a same-file top-level definition, or an
  imported name (jumps into the dependency's source file, e.g. `std/ui`).

## Neovim

```lua
vim.api.nvim_create_autocmd("FileType", {
  pattern = "sigil",
  callback = function(args)
    vim.lsp.start({
      name = "sigil",
      cmd = { "sigil", "lsp" },
      root_dir = vim.fs.root(args.buf, { "std", ".git" }),
    })
  end,
})
```

`root_dir` must be the directory that contains `std/`, so imports like
`import "std/ui"` resolve. (`sigil` must be on `PATH` — `make build` then add
`bin/` to PATH, or use an absolute `cmd`.)

## Not yet (→ #3/#4)

Semantic tokens, completion, multi-error reporting,
incremental sync. Also note: an error inside an *imported* file is currently reported against the
open file at the imported error's line/col (precise cross-file attribution lands
with #3). In practice the open file is usually the one with the error, so this
rarely bites. See `docs/superpowers/specs/2026-06-21-sigil-lsp-foundation-design.md`.
