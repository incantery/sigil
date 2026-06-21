# `sigil lsp` — LSP foundation (diagnostics + document symbols) design

Date: 2026-06-21
Status: approved, pre-implementation

## Goal

Editor support sub-project **#2**: a `sigil lsp` language server that gives
**live diagnostics** and **document symbols** for `.sigil` files, built as a thin
layer over the existing `internal/load` + `internal/types` — **no new compiler
machinery**. This is the foundation that #3 (hover, go-to-def, semantic tokens)
and #4 (completion) build on.

## What the compiler already gives us

- **Errors carry positions.** `lex.Error`, `parse.Error`, and `types.Error` are
  all `struct{ Line, Col int; Msg string }` (1-based). So every diagnostic maps
  cleanly to an LSP range.
- **AST decls carry positions.** `ast.LetDecl.Pos` and `ast.TypeDecl.Pos` exist,
  so top-level document symbols are a straight AST walk.
- **`load.Load` stops at the first error** — no recovery, no error accumulation.
  So a v1 over `load`/`types` as-is yields **one diagnostic per file at a time**.
  Multi-error reporting would require parser recovery + an error-collecting
  checker — the "new compiler machinery" this slice deliberately avoids.
- **`Variant` and `FieldType` carry NO `Pos`** — only the parent `TypeDecl` does.
  So hierarchical symbol children (constructors/fields with their own ranges)
  are out of scope until positions are added to those nodes (pairs naturally
  with #3).
- **Reference:** the old kernel hand-rolled this in git history
  (`pkg/lang/lsp/protocol.go` + `server.go` + `symbols.go`); superseded
  architecture, but a useful shape reference.

## Decisions (settled during brainstorming)

1. **Live diagnostics**, not on-save-only — squiggles as you type.
2. **Hand-rolled JSON-RPC over stdio**, no LSP library — keeps the repo's
   zero-extra-dependency, npm-free ethos; the method surface is small.
3. **Flat document symbols** (top-level decls only) — `Variant`/`FieldType` lack
   positions, so children are deferred.
4. **One `load` change only** — an optional file-content overlay so the checker
   sees unsaved buffers. `parse`/`types` are untouched.

## Architecture

A new package `internal/lsp` holds everything; the only change outside it is the
`load` overlay and a thin `sigil lsp` CLI command. Files, each one
responsibility:

- `internal/lsp/jsonrpc.go` — base protocol: `Content-Length` framing + JSON-RPC
  2.0 envelopes + a method dispatcher.
- `internal/lsp/protocol.go` — the LSP message structs we use (a small subset:
  `InitializeParams/Result`, `TextDocumentItem`, `Diagnostic`, `Range`,
  `Position`, `DocumentSymbol`, etc.).
- `internal/lsp/docs.go` — the open-document store (`URI → {text, version}`).
- `internal/lsp/diagnostics.go` — run analysis, map a compiler error to a
  `Diagnostic`.
- `internal/lsp/symbols.go` — walk the AST into `DocumentSymbol[]`.
- `internal/lsp/server.go` — wires the store, handlers, and lifecycle together.
- `internal/cli/lsp.go` — the `sigil lsp` command.
- `internal/load/load.go` — add `Options.Overlay`.

### 1. Command + transport

- `sigil lsp` (cobra, no flags — the editor launches it) wires `os.Stdin`/
  `os.Stdout` to a `Server` and runs until `exit`.
- **Hand-rolled JSON-RPC** (`jsonrpc.go`): read `Content-Length:`-framed messages
  from a `bufio.Reader`, decode JSON-RPC 2.0 envelopes (`jsonrpc`, `id`,
  `method`, `params`), dispatch by method name. Requests (have `id`) get a
  response; notifications (no `id`) do not. Unknown request method →
  `MethodNotFound` (-32601); unknown notification → ignored. Writes are
  serialized (one mutex) so concurrent diagnostics + responses don't interleave
  on stdout.

### 2. Lifecycle + capabilities

- `initialize` → capture the workspace root: `rootUri`, else
  `workspaceFolders[0].uri`, else the directory of the first opened file. Stored
  as `load`'s `Root` for all checks. Reply advertising only what we implement:
  `textDocumentSync: 1` (Full), `documentSymbolProvider: true`.
- `initialized` → no-op. `shutdown` → reply `null`, set a flag. `exit` →
  terminate (exit 0 if shutdown was received, else 1).

### 3. Document sync + the load overlay

- **Document store** (`docs.go`): a mutex-guarded `map[DocumentURI]Document`
  where `Document = { Text string; Version int }`. `didOpen` inserts;
  `didChange` (full-text sync — `contentChanges[].text` replaces the whole doc)
  updates; `didClose` removes and publishes empty diagnostics for that URI.
- **Load overlay** (the one compiler-side change): add
  `Options.Overlay map[string]string` (absolute file path → content) to
  `internal/load`. At `load`'s single file-read site, consult the overlay before
  `os.ReadFile`; a hit uses the in-memory text. The server builds the overlay
  from all open docs (path derived from each `file://` URI) on every analysis
  run, so the checker sees unsaved edits. No `parse`/`types` change.

### 4. Diagnostics

- Triggered on `didOpen`, `didChange`, `didSave`. For the changed doc: build the
  overlay from all open docs, call `load.Load(docPath, Options{Root, Overlay})`.
- On error, type-switch: `*lex.Error` / `*parse.Error` / `*types.Error` →
  `{Line, Col, Msg}`. Map to one `Diagnostic`, `severity: Error`, range from
  `(Line-1, Col-1)` to the end of that source line (length computed from the doc
  text, so the squiggle is visible); a position past the text clamps to a
  zero-width range. Publish `textDocument/publishDiagnostics { uri, diagnostics }`.
- On success, publish an **empty** diagnostics list for the URI (clears stale
  squiggles).
- **Errors without a position** (import resolution: missing module, cycle —
  built with `fmt.Errorf`) map to a diagnostic at `(0,0)`–end-of-line-0 with the
  error text, so they still surface.
- **Single diagnostic per file** — accepted v1 limitation (load stops at first
  error).
- Runs **synchronously** per message; sigil + stdlib are tiny so a full re-check
  per keystroke is milliseconds. Debounce/caching is a noted future
  optimization, not v1.

### 5. Document symbols

- `textDocument/documentSymbol` → walk `Module.Decls` (re-parse the doc text with
  `parse.Module`; symbols don't need type info or imports resolved) into a
  **flat** `[]DocumentSymbol`:
  - `LetDecl` with `Params` → `Function`; without → `Variable`.
  - `TypeDecl` record (`Record != nil`) → `Struct`; ADT → `Enum`.
  - Each symbol's `name` is the decl name; `range`/`selectionRange` cover the
    decl `Pos` to the end of the name.
- If the doc fails to parse, return an empty symbol list (the diagnostic already
  reports the parse error).
- Constructors/fields-as-children: documented follow-up, gated on adding `Pos`
  to `Variant`/`FieldType`.

## Testing

- **Unit:**
  - JSON-RPC framing round-trip (encode a message → decode it back; a partial/
    multi-message buffer splits correctly).
  - Diagnostics mapping: a fixture with a known type error → asserted
    range + message; a clean file → empty diagnostics; a parse error → asserted
    range; a missing-import → `(0,0)` diagnostic.
  - Overlay: buffer text shadows on-disk content (write a valid file, overlay a
    broken version, assert the error; and vice-versa).
  - Document symbols: `examples/counter/counter.sigil` → expected flat symbol set
    with correct kinds.
- **Integration:** drive the `Server` over an in-memory pipe (`io.Pipe`):
  `initialize` → `initialized` → `didOpen` (a file containing a type error) →
  assert a `publishDiagnostics` with the right range; then `documentSymbol` →
  assert the symbol list; then `shutdown`/`exit`. Exercises transport +
  lifecycle + handlers without a real editor.
- **Manual:** a short `editor/` note on wiring `sigil lsp` into Neovim
  (`vim.lsp.start`), verified by hand (no GUI for automated tests).

## Out of scope (v1 → #3/#4)

Hover, go-to-definition, semantic tokens, completion, multi-error recovery,
incremental document sync, cross-file re-diagnosis when a dependency changes,
workspace symbols, formatting.

## Affected code

- New: `internal/lsp/` (jsonrpc, protocol, docs, diagnostics, symbols, server +
  tests), `internal/cli/lsp.go`.
- Changed: `internal/load/load.go` (`Options.Overlay` + overlay-aware file read),
  `internal/cli/root.go` (register `lsp`).
- Docs: an `editor/` LSP/Neovim note; update `CLAUDE.md` ("What's next" #2 →
  in-progress/done, CLI subcommand list `…serve, dev, lsp`).
