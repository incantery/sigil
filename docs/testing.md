# Testing sigil with sigil

`sigil test [PATH]` discovers `*_test.sigil` files (default `PATH` is `.`),
compiles each against the standard library, and runs it. Slice A runs the
**non-browser tier** in goja; tests that touch the DOM are reported as browser
tests (Slice B will route those to Chrome).

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

Test files live under `tests/` (kept out of `std/`+`examples/` so the
tree-sitter drift guard, which does not yet know the `test`/`expect` keywords,
stays green). Run the suite with `make test-sigil` or `go run ./cmd/sigil test
tests --root .`. The Go suite also runs it via `internal/testrun` `TestDogfood`.

## How it works

`test`/`expect` lower to test-only JS hooks (`__test`/`__expect`) injected by a
dedicated test prelude — the same swap pattern the dev server uses — so the
kernel intrinsics are untouched and `build`/`serve`/`dev` never ship test code.
