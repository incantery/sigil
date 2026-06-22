# Testing sigil with sigil

`sigil test [PATH]` discovers `*_test.sigil` files (default `PATH` is `.`),
compiles each against the standard library, and runs it. Tests that import
`std/browser` are automatically routed to headless Chrome; all others run in
goja. Pass `--skip-dir browser` to exclude browser tests (e.g. when no server
is running): `sigil test tests --root . --skip-dir browser`.

## Writing a test

Test files import the module under test (only `pub` exports are visible) and
`std/test`:

```sigil
import "std/list" (reverse)
import "std/test" (eq, gt)

test "reverse swaps ends" {
  expect (eq (reverse [1, 2, 3]) [3, 2, 1])
}
```

- `test "name" { ... }` — the body is an effect context (you may drive
  `__cell`/`__set`/`__effect` and then assert). Statements are separated by `;`
  with an optional trailing `;` (layout is suspended inside the braces, like
  `effect { }`).
- `expect <matcher>` — records a `Match`. Matchers come from `std/test`:
  `eq`, `neq`, `isTrue`, `isFalse`, `gt`, `lt`. A custom matcher is any function
  returning `{ pass: Bool, label: String, got: String, expected: String }`.
- `let n = expr;` — binds a value for the rest of the test.

Non-browser test files live under `tests/` (kept out of `std/`+`examples/` so
the tree-sitter drift guard stays green). Browser test files live under
`tests/browser/`. Run the non-browser suite with `make test-sigil` or
`go run ./cmd/sigil test tests --root . --skip-dir browser`. The Go suite also
runs it via `internal/testrun` `TestDogfood`.

## How it works

`test`/`expect` lower to test-only JS hooks (`__test`/`__expect`) injected by a
dedicated test prelude — the same swap pattern the dev server uses — so the
kernel intrinsics are untouched and `build`/`serve`/`dev` never ship test code.

## Browser tests (SP1)

Tests that import `std/browser` are **automatically classified** as browser tests
and routed to headless Chrome instead of goja. No annotation needed — the runner
inspects the dependency closure for any `std/browser` intrinsic use.

```sigil
import "std/browser" (navigate, waitVisible, domText)
import "std/test" (eq)

test "counter renders heading" {
  navigate "http://localhost:8099";
  waitVisible "#count";
  expect (eq (domText "#count") "0")
}
```

`std/browser` exports five primitives:

| Function | Signature | Description |
|---|---|---|
| `navigate` | `String -> Unit` | Navigate Chrome to a URL; waits for the in-page agent |
| `click` | `String -> Unit` | Click the first element matching a CSS selector |
| `fill` | `String -> String -> Unit` | Set an input's value and fire input/change |
| `waitVisible` | `String -> Unit` | Block until an element is visible (5s timeout) |
| `domText` | `String -> String` | Read the textContent of the first matching element |

**Chrome-absent skip:** when Chrome is not available the entire browser-test file
is skipped with a clear message (`⤼ skipped (no Chrome): browser test`). The
goja-tier tests in the same run are unaffected. CI without Chrome stays green.

**Failure artifacts:** on any failing browser test the runner writes:

```
.sigil-test/last/<filename>/screenshot.png   # viewport at failure
.sigil-test/last/<filename>/console.log      # browser console output
.sigil-test/last/<filename>/errors.log       # unhandled page exceptions
```

**Running browser tests:**

```sh
# Against a served app (manual):
go run ./cmd/sigil serve examples/counter/counter.sigil &   # :8099
go run ./cmd/sigil test tests/browser --root .              # or: make test-browser

# As part of go test (dogfood fixture, self-contained):
go test ./internal/testrun/ -run TestDogfoodBrowser -v
```

Browser tests live under `tests/browser/`. `make test-sigil` passes
`--skip-dir browser` so only goja tests run; `make test-browser` scans
`tests/browser/` and requires a served app (see comment in
`tests/browser/counter_test.sigil`). `make tree-sitter-verify` is unaffected
(it only scans `std/` and `examples/`).
