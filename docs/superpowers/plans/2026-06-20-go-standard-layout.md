# Idiomatic Go Layout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the `core/` catch-all into an idiomatic Go layout — `cmd/sigil` + flat `internal/*` — with `std/`, `examples/`, and `docs/` at the root.

**Architecture:** Pure relocation. `git mv` the directories, rewrite every `github.com/incantery/sigil/core/X` import to `…/internal/X`, fix a handful of hardcoded path strings, then repoint the Makefile and docs. No code or behavior changes. The existing test suite (including the chromedp/goja end-to-end tests) is the regression oracle — it must stay green at every commit.

**Tech Stack:** Go 1.26, cobra CLI, chromedp + goja for tests, make.

## Global Constraints

- Module path stays `github.com/incantery/sigil` (unchanged).
- Compiler packages are **private**: they live under `internal/` (no `/pkg`, no public API).
- `internal/` is **flat**: `internal/parse`, not `internal/lang/parse`.
- `std/` stays at the repo root (it is `.sigil` source resolved at runtime, not Go packages).
- Platform: macOS (`darwin`) — `sed -i` requires the BSD form `sed -i ''`.
- "Green" means all three pass: `go build ./...`, `go vet ./...`, `go test ./...`.
- The 9 packages being moved: `ast token lex parse types peval emit load cli`.

---

### Task 1: Move Go packages into cmd/ + internal/ and rewrite imports

Moves all Go code out of `core/` and fixes every import path in one atomic
change (a partial move would not compile). `core/examples` is intentionally
left in place this task so the test path strings still resolve — it moves in
Task 2.

**Files:**
- Move: `core/cmd/sigil/` → `cmd/sigil/`
- Move: `core/{ast,token,lex,parse,types,peval,emit,load,cli}/` → `internal/{…}/`
- Modify: all 17 `.go` files importing `github.com/incantery/sigil/core/*` (import rewrite)
- Modify: `internal/cli/root.go` (doc comment `core/cmd/sigil` → `cmd/sigil`)

**Interfaces:**
- Consumes: nothing (first task).
- Produces: packages now importable as `github.com/incantery/sigil/internal/{ast,token,lex,parse,types,peval,emit,load,cli}`; the binary main package at `github.com/incantery/sigil/cmd/sigil`.

- [ ] **Step 1: Record the pre-move baseline (suite is green before we start)**

Run:
```bash
go build ./... && go test ./... 2>&1 | tail -5
```
Expected: all `ok` / no-test-files lines, no failures.

- [ ] **Step 2: Move the binary and the nine packages with git mv**

```bash
mkdir -p cmd internal
git mv core/cmd/sigil cmd/sigil
for p in ast token lex parse types peval emit load cli; do
  git mv core/$p internal/$p
done
```

- [ ] **Step 3: Rewrite every core/* import path to internal/***

```bash
grep -rl 'incantery/sigil/core/' --include='*.go' . \
  | xargs sed -i '' 's#incantery/sigil/core/#incantery/sigil/internal/#g'
```

- [ ] **Step 4: Fix the stale doc comment in root.go**

In `internal/cli/root.go`, change the comment referencing `core/cmd/sigil` to
`cmd/sigil`:
```
// files; cmd/sigil is a thin binary wrapper around Execute.
```

- [ ] **Step 5: Confirm no core/* import paths remain**

Run:
```bash
grep -rn 'incantery/sigil/core/' --include='*.go' . || echo "CLEAN"
```
Expected: `CLEAN`.

- [ ] **Step 6: Run the full suite (examples still at core/examples — must be green)**

Run:
```bash
go build ./... && go vet ./... && go test ./... 2>&1 | tail -8
```
Expected: all packages `ok`; `internal/load` and `internal/cli` tests pass
(their `repoRoot = "../.."` still resolves — they remain two levels below the
root — and `core/examples` still exists this task).

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: move core/ Go packages to cmd/ + internal/

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Move examples to the root and drop the empty core/

Relocates `core/examples` → `examples` and updates the two test files that
hardcode the `core/examples` path. After this, `core/` is empty and removed.

**Files:**
- Move: `core/examples/` → `examples/`
- Modify: `internal/load/example_test.go` (path string)
- Modify: `internal/cli/cli_test.go` (path string)

**Interfaces:**
- Consumes: the `internal/*` packages from Task 1.
- Produces: example apps at `examples/<name>/<name>.sigil`; no remaining `core/` directory.

- [ ] **Step 1: Move the examples directory**

```bash
git mv core/examples examples
```

- [ ] **Step 2: Fix the hardcoded example path in load's test**

In `internal/load/example_test.go`, drop the `"core"` path segment:
```go
	entry := filepath.Join(repoRoot, "examples", "counter", "counter.sigil")
```

- [ ] **Step 3: Fix the hardcoded example path in cli's test**

In `internal/cli/cli_test.go`, drop the `"core"` path segment:
```go
	return filepath.Join(repoRoot, "examples", "counter", "counter.sigil")
```

- [ ] **Step 4: Remove the now-empty core/ directory**

Run:
```bash
rmdir core 2>/dev/null; test ! -d core && echo "core/ gone" || { echo "core/ not empty:"; find core; }
```
Expected: `core/ gone`.

- [ ] **Step 5: Run the full suite**

Run:
```bash
go build ./... && go vet ./... && go test ./... 2>&1 | tail -8
```
Expected: all `ok`; `TestCounterExample` (in `internal/load`) and the cli test
that compiles the counter both pass against the new `examples/` path.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: move examples/ to repo root, remove empty core/

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Repoint the Makefile

Updates the two build variables that still reference `core/`. The version stamp
flows through `-ldflags`, so this also re-verifies the binary builds and stamps.

**Files:**
- Modify: `Makefile` (`CMD_PATH`, `PKG`)

**Interfaces:**
- Consumes: the new `cmd/sigil` and `internal/cli` locations from Task 1.
- Produces: `make build` → `bin/sigil` with a working `version` command.

- [ ] **Step 1: Update CMD_PATH and PKG**

In `Makefile`:
```make
CMD_PATH := ./cmd/sigil
PKG      := github.com/incantery/sigil/internal/cli
```

- [ ] **Step 2: Build the binary and check the version stamp resolves**

Run:
```bash
make build && bin/sigil version
```
Expected: `→ bin/sigil (<version>)` then a version line (not `0.0.1-dev`
unless untagged) — confirming the `-ldflags -X …/internal/cli.Version` path is
correct.

- [ ] **Step 3: Smoke-test a subcommand against the moved example**

Run:
```bash
bin/sigil check examples/counter/counter.sigil
```
Expected: `ok  examples/counter/counter.sigil`.

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "build: repoint Makefile to cmd/sigil + internal/cli

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Update CLAUDE.md and README.md prose

Fixes every `core/…` path reference in the two docs so the working notes match
the new tree.

**Files:**
- Modify: `CLAUDE.md` ("Where things live", build/run commands)
- Modify: `README.md` (run command, Layout section)

**Interfaces:**
- Consumes: the final tree from Tasks 1–3.
- Produces: docs with no stale `core/` references.

- [ ] **Step 1: Find every core/ reference in the docs**

Run:
```bash
grep -nE 'core/' CLAUDE.md README.md
```
Expected: a short list (the "Where things live" bullet, the `go run` /
`go test` commands, the README run command and Layout bullet). Note each one.

- [ ] **Step 2: Update CLAUDE.md**

In CLAUDE.md, rewrite the package locations and commands:
- "Where things live" → packages live in `internal/` (`internal/lex`, `internal/parse`, …), the CLI is `internal/cli` wrapped by `cmd/sigil`, examples at `examples/`.
- Build/run block:
```sh
go build ./...
go test ./...
go run ./cmd/sigil serve examples/counter/counter.sigil   # serves on :8099
make build                           # → bin/sigil
```
- Any other `core/…` path (e.g. `core/load`, `core/peval`, `core/examples/counter`) → the `internal/…` / `examples/…` equivalent.

- [ ] **Step 3: Update README.md**

In README.md:
- Run command → `go run ./cmd/sigil serve examples/counter/counter.sigil`.
- Layout section → `internal/` holds the compiler + CLI; `examples/` at root.

- [ ] **Step 4: Confirm no stale core/ references remain**

Run:
```bash
grep -nE 'core/' CLAUDE.md README.md && echo "STILL PRESENT — fix" || echo "CLEAN"
```
Expected: `CLEAN`.

- [ ] **Step 5: Final whole-tree verification**

Run:
```bash
go build ./... && go vet ./... && go test ./... 2>&1 | tail -8 && git status --short
```
Expected: all `ok`; working tree clean after commit (next step).

- [ ] **Step 6: Commit**

```bash
git add CLAUDE.md README.md
git commit -m "docs: update CLAUDE.md + README.md for the cmd/internal layout

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- Target layout (cmd/sigil, internal/* flat, examples/, std/ at root) → Tasks 1 & 2. ✓
- Move dirs with history (`git mv`) → Tasks 1 & 2 use `git mv`. ✓
- Rewrite 17 importers / 9 paths → Task 1 Step 3 + verify Step 5. ✓
- Fix non-import references (2 test path strings + root.go comment) → Task 1 Step 4, Task 2 Steps 2–3. ✓
- `repoRoot = "../.."` confirmed valid → Task 1 Step 6 note, Task 2 Step 5. ✓
- Makefile CMD_PATH/PKG → Task 3. ✓
- CLAUDE.md + README.md prose → Task 4. ✓
- Verification (build/vet/test green, bin/sigil version, git clean) → Task 3 Steps 2–3, Task 4 Step 5. ✓
- `std/` unchanged → never touched in any task. ✓

**Placeholder scan:** No TBD/TODO; every code/command step shows exact content. ✓

**Type consistency:** No new types or signatures introduced (pure move); the 9 package names are listed once in Global Constraints and reused verbatim. ✓
