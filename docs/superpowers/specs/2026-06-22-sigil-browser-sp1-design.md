# Sigil Browser — SP1 "First light" (driving spine + first assertion) — design

Status: approved (brainstorm) — 2026-06-22
Author: brainstormed with Claude

## Context: the larger vision

"Sigil Browser" is an **agent-grade browser driver/test/benchmark tool**. The goal
is for Sigil browser testing to be worth using *in its own right* — to drive *any*
web app (not just Sigil-rendered ones), make real DOM assertions, capture an
AI-optimized run artifact (HAR, screenshots, console, errors, perf), and be the
single best way for an AI coding agent to drive a browser. Tests must also be
**fast** — slow e2e is the worst part of browser testing.

This is a multi-spec program. The agreed decomposition (each its own
spec→plan→build cycle):

- **SP1 (this spec) — Spine + first assertion:** the locked architecture made real
  end-to-end on the smallest believable surface.
- **SP2 — AI run bundle:** network→HAR, per-step screenshots, console/error
  streams, DOM snapshots, synchronized per-step timeline, manifest.
- **SP3 — Benchmarking:** CDP tracing/Performance + in-page Web Vitals + network
  timing; perf matchers and a benchmark report.
- **SP4 — Interaction & stability breadth:** richer waits (network-idle, custom
  predicates), more actions (hover/scroll/select/keyboard/drag), trusted-input
  variants, retries/timeouts, iframes, parallel contexts.
- **SP5 — Harness & distribution:** base-URL/targets, serve-your-Sigil-app
  integration, fixtures, parallelism, single-binary standalone story, and a **live
  agent-drive mode** (an agent issues commands live instead of a compiled test
  file — reuses the SP1 spine).

This builds on the completed **Slice A** (goja unit-test tier: `test`/`expect`
syntax, `std/test` matchers, `internal/testrun` runner, `sigil test` CLI).

## The locked architecture

A browser test's logic runs **Go-side in goja** (the coordinator). The driver
holds two channels to one headless Chrome:

- **CDP (Go ↔ browser):** launch/own Chrome, `navigate`, inject the agent on every
  document, capture console/page-errors, screenshots, (later) HAR/tracing.
- **Bidirectional websocket (in-page agent ↔ Go):** the DOM hot path. Go sends
  compound *intents* ("click, then resolve when visible, return text"); the agent
  executes locally with **observer-based** waiting (no polling) and replies once.
  Bidirectional so the agent can stream events and Go can time captures precisely.

The in-page agent is **hand-written JS** (not Sigil — running Sigil in the browser
would require a large browser-boundary-intrinsic expansion; deferred indefinitely).

Pure-logic tests still run in **plain goja** (Slice A, untouched). A **classifier**
routes tests that touch the browser DSL to the browser runner.

### Key simplification: the Sigil layer stays synchronous

Because the test runs in goja Go-side, the browser primitives are **Go functions
that block** on the ws round-trip and return synchronously to goja. So `domText
"#x"` is a plain synchronous `String` to the test; the waiting happens inside Go (a
goroutine parked on a reply channel). Therefore:

- **No async/await anywhere in the Sigil layer.**
- Slice A's `__runTests`/`__expect` collector is reused **verbatim** — a browser
  test is "the same test bundle in goja, with browser intrinsics bound to
  live-session Go functions."

## SP1 scope

A browser test like:

```sigil
import "std/browser" (navigate, click, fill, waitVisible, domText)
import "std/test" (eq)

test "counter increments" {
  navigate "http://localhost:8099";
  click "#inc";
  waitVisible "#count";
  expect (eq (domText "#count") "1")
}
```

is routed to a reused headless Chrome, driven, asserted via `std/test` matchers,
and (on failure) leaves a screenshot + console + page-errors artifact. This is the
smallest slice that exercises the whole spine: CDP launch+inject, the bidirectional
ws, the in-page agent, the DSL, the classifier, blocking Go primitives, and the
capture path.

## Two channels — responsibility split

- **CDP ops (Go-side, via chromedp):** launch Chrome (reuse the existing harness
  pattern), `__navigate` (chromedp.Navigate + wait for the re-injected agent to
  redial), agent injection (`Page.addScriptToEvaluateOnNewDocument`), console +
  page-error capture (Runtime domain), screenshot on failure (Page.captureScreenshot).
- **WS intents (agent-side):** `__domText`, `__waitVisible`, `__click`, `__fill`.
  The agent does observer-based waiting (MutationObserver / condition re-check;
  resolve the instant the condition holds) and replies `{id, ok, value|error}`.

## Components & package layout

- **`internal/browser`** (new): the driver.
  - Session lifecycle: one browser reused for the whole run; a fresh page per
    browser-test file (navigate resets state). Multi-context/parallelism is SP4.
  - ws server (localhost, ephemeral port) the agent dials; intent send/await-reply
    correlated by integer id; a reply-channel map.
  - Agent injection via CDP; navigate waits until the new agent has reconnected
    (the agent sends a "hello" on connect; the navigate primitive blocks on it).
  - Capture: buffer console/exceptions per test (CDP Runtime events); screenshot on
    failure.
  - The agent JS ships as `//go:embed agent.js`.
  - A `Session` API the runner uses (illustrative): `Navigate(url) error`,
    `DomText(sel) (string, error)`, `WaitVisible(sel) error`, `Click(sel) error`,
    `Fill(sel, text) error`, `ScreenshotPNG() ([]byte, error)`, `Console() []string`,
    `Errors() []string`, `Close() error`, plus a constructor that launches Chrome
    and returns an error if Chrome is absent.
- **`internal/browser/agent.js`** (new, hand-rolled, ~100 lines): dial the ws,
  handle the 4 DOM intents, observer-based `waitVisible`, reply by id, send "hello"
  on (re)connect.
- **`std/browser.sigil`** (new): the DSL —
  - `navigate : String -> Unit`
  - `click : String -> Unit`
  - `fill : String -> String -> Unit`  (`fill sel text`; not `type` — keyword)
  - `waitVisible : String -> Unit`
  - `domText : String -> String`
  - thin wrappers over boundary intrinsics `__navigate/__click/__fill/__waitVisible/__domText`.
- **Type/intrinsic registration:** the five browser intrinsics are typed in
  `internal/types` like the existing boundary intrinsics, but **bound only by the
  browser runner** (injected into the goja VM via `vm.Set`), so they are **not** in
  the JS prelude. They are test-time-only — a normal `build` would leave them
  unbound, which is fine (a browser test is not a buildable app). Actions
  (`__navigate/__click/__fill/__waitVisible`) are **effect ops** (gated to effect
  context, like `__set`); `__domText` is a **read** (ungated, like `__path`). The
  test body is an effect context (Slice A), so all five are legal there, including
  `domText` nested inside an `expect` argument.

## The intent protocol (SP1)

- Intent (Go→agent): `{ "id": <int>, "op": "domText"|"waitVisible"|"click"|"fill", "sel": "<css>", "text": "<for fill>" }`
- Reply (agent→Go): `{ "id": <int>, "ok": true, "value": "<string|empty>" }` or
  `{ "id": <int>, "ok": false, "error": "<message>" }`
- Hello (agent→Go on connect): `{ "hello": true }` (so navigate can await
  reconnection).

`navigate` is **not** a ws intent — it is a CDP op. Console/errors are captured via
CDP Runtime events (Go-side), **not** the ws, to keep the SP1 agent minimal.

`waitVisible` has a per-intent timeout (e.g. 5s default for SP1); on timeout the
agent replies `{ok:false, error:"timeout waiting for <sel>"}` → surfaces as the
test's `Error`.

## Classifier (SP1 form)

After `load.Load` of a test file, scan **every module in the program's dependency
closure** for a reference to any browser intrinsic
(`__navigate/__click/__fill/__waitVisible/__domText`). If any module references
one, route the **whole file** to the browser runner; else plain goja. Cheap, robust
(catches indirect use through helper modules), **per-file** (accepted trade-off).

**Escalation net:** a goja file that errors with an unbound-browser-intrinsic
signature (e.g. `__navigate is not defined`) is re-run in the browser. Extend
`looksBrowser` (or add a sibling) to recognize the browser-intrinsic-unbound
signature.

Per-*test* precision (running some tests of a mixed file in goja and others in the
browser) is deferred.

## Runner integration

`internal/testrun.Run` gains a routing step: classify each discovered file →
goja path (existing `runFile`) or browser path (new `runFileBrowser`).

`runFileBrowser(file, root, sess)`:
1. `load.Load` + `prog.BundleTest()` (same bundle as goja).
2. Create a goja VM; `vm.Set` the five browser intrinsics to Go closures that call
   the live `Session` (blocking on the ws/CDP round-trip, returning the value or
   throwing a goja error that `__runTests`'s try/catch turns into `TestResult.Error`).
3. `vm.RunString(js + "\n;JSON.stringify(__runTests())")` → results (same readback
   as goja).
4. For each failing/errored test, capture screenshot + the buffered console/errors
   to a per-run artifact dir and note the path.

Results flow into the same `[]TestResult` and the existing `Run` report (✓/✗ per
test). Reuse one `Session` across all browser files; reset with a fresh page per
file.

## Capture (minimal — SP2 makes it rich)

Per run, an artifact root (e.g. `.sigil-test/<timestamp>/`). On a failing/errored
browser test, write `<file>/<test>/screenshot.png`, `console.log`, `errors.log` and
print the path in the report. SP2 adds HAR, per-step screenshots, DOM snapshots, the
synchronized timeline, and a manifest. (The artifact root path is passed in / made
deterministic for tests rather than time-based where tests need to assert on it.)

## Chrome-absent behavior

If Chrome won't launch, **skip** browser-classified files with a clear
`N browser tests skipped (no Chrome)` notice; exit 0 if all non-skipped tests
passed (matches Go `t.Skipf` philosophy; goja-only CI stays green). A
`--require-browser` strict flag is a later add. The report shows the skipped count.

## Testing strategy (how SP1 itself is tested)

- **`internal/browser`** Go tests via chromedp against an `httptest` fixture HTML
  page: assert ws handshake + agent injection, `domText`, `click` (then `domText`
  reflects the change), `waitVisible` (resolves when an element appears),
  `navigate` (to a second fixture page, agent redials), and a failure screenshot is
  produced. **Skip if Chrome absent** (existing `t.Skipf` pattern).
- **Classifier** unit tests: a file importing `std/browser` (directly or via a
  helper module) routes to browser; a pure file routes to goja.
- **Blocking-primitive** test: a goja VM with a stub Session (no real browser)
  confirms `__domText` returns synchronously and a thrown primitive becomes a
  `TestResult.Error`.
- **Dogfood** `tests/browser/<name>_test.sigil`: serve a known app (e.g. the
  counter example via `httptest`/the existing serve path) and assert `domText` /
  `click` → `domText`. Wired so it **skips** (not fails) when Chrome is absent, so
  `go test ./...` and `make test-sigil` stay green without Chrome.

## Explicitly out of scope (later SPs)

- Rich AI run bundle / HAR / per-step screenshots / DOM snapshots / manifest (SP2).
- Benchmarking, Web Vitals, perf matchers (SP3).
- More actions (hover/scroll/select/keyboard/drag), network-idle/custom waits,
  trusted CDP input, retries, iframes, parallel contexts (SP4).
- base-URL/targets config, serve-app integration, fixtures, live agent-drive mode,
  single-binary distribution (SP5).
- Per-*test* (vs per-file) browser routing.
- Rewriting the agent in Sigil (would require browser-boundary intrinsics).

## Open implementation questions (for the plan, not blocking)

- Exact `Session` constructor/shutdown shape and how the ws server lifetime binds
  to the chromedp context.
- How `navigate` reliably waits for agent reconnection (hello handshake + a
  ready channel keyed to the current document).
- Where the browser-intrinsic name set lives so the classifier and the runner agree
  on it (a single shared list).
- Artifact-root path injection for deterministic capture assertions in tests.
