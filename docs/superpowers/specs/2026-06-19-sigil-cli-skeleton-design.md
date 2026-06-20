# Sigil CLI — Phase 1: skeleton

**Date:** 2026-06-19
**Status:** Approved (design)

## Context

`sigil` and `sigil` are the same project: the repo was renamed from sigil to sigil,
and the language was refocused from a multi-target UI DSL (web, Swift, Kotlin, …)
to a proper programming language + toolset for **frontend web** development. The
kernel was redesigned into the lean `core/` + `std/` (≈24 intrinsics; stdlib in
Sigil). The old `pkg/ internal/ cmd/sigil` tree is the previous kernel and its
cobra-based CLI — bound to the dead `pkg/...` IR, but its command *surface*
(`run`, `fmt`, `test`/e2e, `vet`, …) is the product direction, to be carried
forward as the `sigil` CLI rebound onto `core/`.

There is **no `sigil` CLI today**. The only new-kernel binary is
`core/cmd/serve` (a rebuild-per-request dev server). The Makefile still
builds/installs `sigil` from `./cmd/sigil`.

This is a large, multi-phase effort. It is decomposed so each phase is its own
spec → plan → implement cycle. **This spec covers Phase 1 only: an installable
`sigil` binary with a cobra command skeleton and the cheap commands that map
directly onto the new kernel.** It deliberately defers porting the heavier
subcommands.

## Goals

- Ship an installable `sigil` binary (`make install` installs `sigil`, not `sigil`).
- Establish the cobra command surface that later subcommand ports hang off of.
- Provide the three commands that are thin wrappers over the new kernel:
  `serve`, `build`, `check` (plus `version`).
- Keep `go build ./...` and `go test ./core/...` green.

## Non-goals (deferred to later phases, each its own spec)

- Porting `run`, `fmt`, `test`/e2e (scenario runner), `vet`, `lsp`, `describe`,
  `explore`, `shot`, `stories`, `tokens`, `gen`.
- Removing the old `pkg/ internal/ cmd/sigil` kernel.
- A Bubble Tea TUI on the bare `sigil` invocation.
- viper/config wiring.
- Richer structured diagnostics from the kernel.

## New-kernel API this builds on

From `core/load`:

```go
func Load(entryFile string, opts Options) (*Program, error) // parse + resolve + typecheck
type Options struct { Root string; Prefix string }
func (p *Program) Bundle() (string, error)                  // linked JS bundle
```

`Load` stops at the first parse/resolve/type error and returns a plain `error`
(not a structured `*diag.Diagnostic`). `Bundle` produces the npm-free JS string.
`core/cmd/serve/main.go` already composes these (`build(entry, root)` →
`load.Load` → `prog.Bundle`) and wraps the JS in an HTML `shell` template.

## Architecture

Mirror the proven `cmd/sigil` (thin) → `internal/cli` (testable) split, rebound
onto `core/`:

- **`core/cmd/sigil/main.go`** — thin binary wrapper. Calls `cli.Execute()`;
  on error exits nonzero, printing to stderr unless the error is `cli.ErrSilent`
  (a command already printed its own diagnostic, e.g. `check --json`). Adapted
  from `cmd/sigil/main.go`.
- **`core/cli/`** — new package holding the cobra command tree and each
  subcommand. The new-kernel sibling of `internal/cli`. Later subcommand ports
  land here as additional `*.go` files.

Framework: **cobra** (already in `go.mod` at v1.10.2; viper v1.21.0 also present).
Phase 1 adds **no new dependencies**.

### Files

- `core/cli/root.go` — `rootCmd` (`Use: "sigil"`), `Execute()`, `ErrSilent`,
  `Version` var (overridden via `-ldflags -X .../core/cli.Version=…`),
  `AddCommand` for serve/build/check/version. `SilenceUsage` +
  `SilenceErrors` set (own error surfacing). No-subcommand behavior: print help
  (cobra default usage), no TUI.
- `core/cli/serve.go` — `serveCmd`. Flags `--root` (default `.`), `--port`
  (default `8099`). Ports the body of `core/cmd/serve/main.go`: build once at
  startup to fail fast, then serve, rebuilding per request; the HTML `shell`
  template lives here (or in a shared helper — see build.go).
- `core/cli/build.go` — `buildCmd`. Flags `--root` (default `.`), `-o/--out`
  (default stdout), `--html` (wrap the JS in the page shell vs. emit raw JS).
  Backed by `load.Load` + `Bundle()`. The `shell` template is shared between
  build and serve (single definition in this package).
- `core/cli/check.go` — `checkCmd`. Flags `--root` (default `.`), `--json`.
  Runs `load.Load` only (no `Bundle`). Success: prints `ok  <file>` (or JSON
  `{ok:true, file}`); failure: prints the error to stderr (or JSON
  `{ok:false, file, error}` to stdout) and returns `ErrSilent` for a nonzero
  exit.
- `core/cli/version.go` — `versionCmd`. Prints
  `sigil <Version> (<os>/<arch>, go <ver>)`.
- `core/cmd/sigil/main.go` — wrapper described above.

### Command summary

| Command | Behavior | Backed by |
|---|---|---|
| `sigil serve [--root DIR] [--port N] ENTRY.sigil` | Rebuild-per-request dev server (port of `core/cmd/serve`) | `load.Load` + `Bundle()` |
| `sigil build [--root DIR] [-o FILE] [--html] ENTRY.sigil` | Emit JS bundle to stdout/`-o`; `--html` wraps in the page shell | `Bundle()` + shell template |
| `sigil check [--root DIR] [--json] ENTRY.sigil` | Typecheck only; nonzero exit on failure; `--json` for editor/AI loops | `load.Load` |
| `sigil version` | `sigil <ver> (os/arch, go …)` | — |
| *(no subcommand)* | Print help | — |

All file-taking commands use `cobra.ExactArgs(1)` for the entry `.sigil` path.

### check JSON shape

Because `core` errors are plain `error` (not `*diag.Diagnostic`), the JSON is
simpler than the old `check --json`:

- success: `{"ok": true, "file": "<path>"}`
- failure: `{"ok": false, "file": "<path>", "error": "<message>"}`

## Makefile changes

Repoint the **core toolchain targets** to sigil; leave the sigil-era
example/demo targets (studio, docs, chat, gauntlet, tree-sitter, nvim, vscode)
untouched — they still compile against the old kernel and belong to the later
"remove old kernel" phase.

```makefile
BIN_NAME := sigil
CMD_PATH := ./core/cmd/sigil
PKG      := github.com/incantery/sigil/core/cli   # for -ldflags -X PKG.Version
```

Targets updated: `build`, `install`, `run` (and the `version`/`LDFLAGS` path that
references `PKG.Version`). Repo-wide targets `test`, `vet`, `fmt`, `tidy`,
`clean` stay as-is.

## Testing

`core/cli` package tests (no browser required):

- `check` against `core/examples/counter/counter.sigil` succeeds (exit ok,
  `ok` output).
- `build` against the counter example returns a non-empty bundle; `--html`
  output contains the bundle inside the page shell and an `#app` mount point.
- `check` against a deliberately broken source returns a non-nil error /
  `ErrSilent`, and with `--json` emits `ok:false`.

These exercise commands by invoking their `RunE`/helper functions directly (or
via `rootCmd.SetArgs` + `Execute`), capturing stdout/stderr — not by shelling
out to a built binary.

Acceptance: `go build ./...` green; `go test ./core/...` green;
`make install` installs a working `sigil` whose `serve`, `build`, `check`,
`version` behave as specified.

## Risks / open questions

- `--root` default of `.` means `sigil` must be run from a directory where `std/`
  resolves. Acceptable for Phase 1 (matches `serve` today); a smarter root
  discovery is a later concern.
- The HTML `shell` template is duplicated from `core/cmd/serve` into `core/cli`.
  Once `sigil serve` exists, `core/cmd/serve` becomes redundant; whether to delete
  it in this phase or a later one is left open (default: leave it, remove when
  the old kernel goes).
