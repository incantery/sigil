-- Filetype detection for Sigil. Installed by `make nvim-install`
-- alongside the tree-sitter parser (parser/sigil.so) and its queries
-- (queries/sigil/*.scm).
vim.filetype.add({
  extension = {
    sigil = "sigil",
  },
})

-- Map the filetype to the tree-sitter language so `:set ft=sigil`
-- buffers get highlighting from the bundled parser.
vim.treesitter.language.register("sigil", "sigil")
