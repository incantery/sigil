# Sigil test framework — Slice A (goja tier) — design

Status: approved (brainstorm) — 2026-06-22
Author: brainstormed with Claude

## Goal

Let us **test Sigil with Sigil**. Tests are authored in `.sigil`, and a new
`sigil test` CLI subcommand compiles and runs them. Slice A delivers the full
**non-browser tier** end-to-end: `test`/`expect` syntax, a `std/test` matcher
library, a test-only runtime, the goja runner, and `sigil test`. Tests that
need a real DOM are deferred to **Slice B** (static DOM-reachability classifier
+ chromedp driver); Slice A only has to *fail gracefully* when it meets one.

This is the first of two sequential sub-projects (each its own spec→plan→build):

- **Slice A (this spec):** syntax + matchers + collector + goja runner + CLI.
- **Slice B (next):** static DOM-reachability pass + chromedp driver routing
  DOM tests to Chrome, plus a result bridge.

## Decisions locked during brainstorming

1. **Test shape:** language-level `test "name" { ... }` syntax (not an exported
   list, not a naming convention).
2. **Expect semantics:** matcher functions — `expect (eq actual expected)` —
   carrying actual/expected into the failure message (not bool-only, not a
   special-form `==` capture).
3. **Browser split (Slice B):** static call-graph detection (no author marker,
   no separate-file convention). Out of scope for Slice A beyond graceful
   failure.
4. **Sequencing:** two slices, goja first (this spec).
5. **Test location:** separate `*_test.sigil` files that `import` the module
   under test. Tests see only `pub` exports. Build never encounters them.

## Authoring model

A test file imports the module under test (only `pub` exports visible) and
`std/test`:

```sigil
import "std/list" (reverse, len)
import "std/test" (eq, isTrue, gt)

test "reverse swaps ends" {
  expect (eq (reverse [1, 2, 3]) [3, 2, 1])
}

test "len counts" {
  let xs = [10, 20]
  expect (eq (len xs) 2)
  expect (gt (len xs) 0)
}
```

- **`test "name" { ... }`** — a new top-level keyword declaration. The body is
  an **effect context** (like an `effect { }` block), so tests may drive
  reactivity (`__cell`/`__set`/`__effect`, via `std/reactive`) and then assert.
  The body is a statement sequence: `let` bindings and `expect` statements.
- **`expect <matcher>`** — a keyword statement valid only inside a `test` block.
  Its argument must have type `Match`. It records the result and **never
  throws**, so every `expect` in a test runs and the test passes iff all of its
  expects passed.
- **`std/test`** — pure-Sigil matchers, each returning `Match`:
  `eq`, `neq`, `isTrue`, `isFalse`, `gt`, `lt`, `gte`, `lte`, `contains`
  (list/string). Matchers build their `got`/`expected` strings via existing
  `"${x}"` interpolation and polymorphic `==` (which already lowers to `__eq`).
  Custom matchers are ordinary functions returning `Match` — no special status.

## Types & lowering

- **`Match` is a built-in record type** known to the type checker, mirroring how
  `Option`/`Result`/`Cell`/`Node` are built in:

  ```
  Match = { pass: Bool, label: String, got: String, expected: String }
  ```

  `expect e` type-checks by requiring `e : Match`. `std/test` matchers construct
  `Match` record values. (Alternative considered: resolve `Match` from the
  `std/test` import. Rejected to avoid coupling the checker's `expect` rule to a
  specific stdlib import; built-in is consistent with the existing built-in
  ADTs.)

- **`test`/`expect` lower to test-only runtime hooks** — `__test(name, thunk)`
  and `__expect(match)` — injected by a dedicated **test prelude**, exactly the
  pattern dev-mode uses to inject `__sigilDev` (see `devSwaps`/`devPrelude` in
  `internal/emit`). These are **not** new kernel intrinsics: the count of ~24
  stays put, and they exist only in test builds.

- Under the **normal prelude**, the emitter **drops `test` declarations**
  entirely, so `build`/`serve`/`dev` never ship them. (In practice a separate
  `*_test.sigil` file is never a build entry, so this is a defensive guard.)

## Runner & CLI

- **`internal/cli/test.go`** → `newTestCmd()`, registered in `root.go`
  alongside the other subcommands (cobra, same pattern as `check`/`build`).
- **`internal/testrun`** (new package): for each discovered `*_test.sigil` file,
  load it as an entry module (`load.Load(path, Options{Root})`, resolving its
  imports against the stdlib root), bundle it with the **test prelude** plus a
  driver that runs the registered tests and exposes a structured results array,
  execute the bundle in **goja**, and read results back via `v.Export()`
  (reusing the established goja patterns in `emit_test.go` / `load_test.go`).
- **Result shape** read back from JS:
  per test → `{ name, expects: [{ pass, label, got, expected }] }`.
- **Discovery:** `sigil test [path]` (default: project root) walks for files
  matching `*_test.sigil`. `path` may be a single file or a directory.
- **Output (default, human-readable):** group by file; print `✓`/`✗` per test;
  for each failure show the test name, matcher `label`, `expected`, and `got`;
  footer with totals (files, tests, passed, failed). Exit `0` if all pass,
  `1` otherwise.
- **Graceful browser-test failure:** if a goja run throws a `ReferenceError` for
  `document`/`window`/etc., the runner reports the test as errored with a clear
  message ("looks like a browser test — Slice B will route these to Chrome")
  rather than a raw stack trace, and still exits non-zero.

## Testing the tester

- Go-level tests in `internal/testrun` that run sample `*_test.sigil` fixtures
  and assert the runner reports the correct pass/fail counts and failure
  messages (label/expected/got), plus the browser-guard path (a DOM-touching
  test errors gracefully, runner exits non-zero).
- First dogfood: add real `*_test.sigil` files for a couple of stdlib modules
  (e.g. `std/list`, `std/string`) and wire `go test ./...` or a `make` target to
  run `sigil test` over them so the Sigil suite is exercised in CI.

## Explicitly out of scope (Slice B and later)

- Static DOM-reachability classifier; chromedp driver; browser result bridge.
- Co-located / non-`pub` internal testing.
- `--json` output, watch mode, parallel execution, test filtering/`-run`.
- Setup/teardown fixtures, async/`__fetch` assertions, snapshot testing.

## Open implementation questions (for the plan, not blocking)

- Exact AST node for the `expect` statement (dedicated node vs. reuse of an
  application form) and where the "effect context" flag is threaded through the
  type checker for `test` bodies.
- Whether the test prelude is a third prelude variant or a small swap-set over
  the base prelude (favor the swap-set, consistent with `devSwaps`).
- The precise built-in registration site for the `Match` type in
  `internal/types`.
