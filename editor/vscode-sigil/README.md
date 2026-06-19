# Sigil for VS Code

Language support for [Sigil](https://github.com/incantery/mako) — the
Go-native semantic UI compiler.

- **Instant syntax highlighting** via a TextMate grammar.
- **Compiler-accurate everything else** via `sigil lsp`: diagnostics on
  every keystroke (the same parse + lower pipeline as `sigil check`),
  semantic tokens, document outline, go-to-definition, hover.

## Requirements

The `sigil` CLI on your PATH (`make install` from the repo). If it
lives elsewhere, point `sigil.serverPath` at the binary. Highlighting
works even without the CLI; diagnostics and navigation need it.

## Building from source

```sh
make vscode-ext
code --install-extension editor/vscode-sigil/sigil-lang-0.1.0.vsix
```
