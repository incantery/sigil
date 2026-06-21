# Adopt an idiomatic Go repo layout (cmd/ + internal/)

*Status: approved design, ready for planning. Date: 2026-06-20.*

## Problem

After several refactor rounds the repo settled into a non-idiomatic shape: every
Go package lives under a catch-all `core/` directory (`core/lex`, `core/parse`,
`core/cli`, `core/cmd/sigil`, `core/examples`, …), with the Sigil standard
library at `std/`. A `core/` prefix on every import path is unusual for Go and
makes the tree read oddly.

The original request was to adopt `golang-standards/project-layout`. We are
**not** doing that literally — see the next section — but we are adopting its
idiomatic, uncontroversial subset.

## Decision: idiomatic subset, not the literal project-layout

`golang-standards/project-layout` is a community repo, not a Go-team standard,
and parts of it are contested:

- **`/pkg` is an anti-pattern.** It stutters every import path
  (`…/sigil/pkg/parse`) for no benefit and is discouraged by much of the Go
  community. The now-deleted old kernel used `pkg/`; reintroducing it would walk
  back toward the layout we just removed.
- The Go toolchain itself — the closest analog to this project — uses `cmd/` +
  `internal/`, not this layout.

So we adopt the parts of project-layout that *are* idiomatic Go — `cmd/`,
`internal/`, `examples/`, `docs/` — and skip `/pkg`.

**API-surface decision:** the compiler packages (`lex`, `token`, `ast`, `parse`,
`types`, `peval`, `emit`, `load`) and `cli` are **private** to the `sigil`
binary, so they move under `internal/`. This gives the strongest encapsulation
and lets package boundaries be refactored freely without a public API contract.
(If an embedding API is ever wanted, a curated package can be promoted out of
`internal/` later.)

**Sub-structure decision:** flat `internal/` (e.g. `internal/parse`), not grouped
(`internal/lang/parse`). Package names are already unambiguous; nesting would add
path length for no gain.

## Target layout

```
sigil/
├── cmd/
│   └── sigil/             # main.go — binary wrapper (from core/cmd/sigil)
├── internal/
│   ├── ast/  token/  lex/  parse/
│   ├── types/  peval/  emit/  load/
│   └── cli/
├── std/                   # Sigil stdlib (*.sigil) — unchanged, stays at root
├── examples/
│   └── counter/           # from core/examples/counter
├── docs/
│   └── kernel-redesign.md
├── go.mod  go.sum  Makefile  README.md  CLAUDE.md  .gitignore
```

**Why `std/` stays at root:** it is `.sigil` source resolved at runtime via the
loader's `Root` option, not Go packages. It is never addressed by Go import path,
so it does not belong under `internal/`; root is its honest home.

## Migration mechanics

All changes are mechanical (directory moves + import-path rewrite + a few
hardcoded path/string fixes). The existing test suite — including the
chromedp/goja end-to-end tests — is the safety net.

1. **Move directories** (use `git mv` to preserve history):
   - `core/cmd/sigil` → `cmd/sigil`
   - `core/{ast,token,lex,parse,types,peval,emit,load,cli}` → `internal/{…}`
   - `core/examples` → `examples`
   - remove the now-empty `core/`.

2. **Rewrite import paths** repo-wide (17 Go files import `core/*`; 9 distinct
   package paths): `github.com/incantery/sigil/core/X` →
   `github.com/incantery/sigil/internal/X` for X in
   {ast, token, lex, parse, types, peval, emit, load, cli}.

3. **Fix non-import references:**
   - `core/cli/cli_test.go` — `filepath.Join(repoRoot, "core", "examples", …)`
     → drop the `"core"` segment.
   - `core/load/example_test.go` — same `"core","examples"` → `"examples"` fix.
   - `core/cli/root.go` — doc comment mentioning `core/cmd/sigil` → `cmd/sigil`.
   - Verify the `repoRoot` constants in `load` and `cli` tests: both packages
     stay two levels below the root (`internal/load`, `internal/cli`), so
     `repoRoot = "../.."` remains correct — confirm, don't assume.

4. **Update build + docs prose:**
   - `Makefile`: `CMD_PATH := ./core/cmd/sigil` → `./cmd/sigil`;
     `PKG := …/core/cli` → `…/internal/cli` (the `-ldflags` version stamp).
   - `CLAUDE.md` and `README.md`: every `core/…` path reference (the "Where
     things live", build/run commands, and layout sections).

## Verification (done = all green)

- `go build ./...`
- `go vet ./...`
- `go test ./...` (the language suite, incl. headless-Chrome e2e — skips cleanly
  if Chrome is absent; goja tests run hermetically)
- `bin/sigil` still builds via `make build` and the version stamp resolves
  (`bin/sigil version`).
- `git status` clean; `git log --follow` shows continuity across the moved files.

## Out of scope

- No package-boundary or code changes beyond what the move requires.
- No public/embedding API extraction (revisit only if a consumer appears).
- `std/`, `docs/`, and the module path `github.com/incantery/sigil` are unchanged.
