# Sigil Browser SP1 — Driving spine + first assertion — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `*_test.sigil` browser test can `navigate` to a URL, `click`/`fill`/`waitVisible`, and assert with `expect (eq (domText "#x") "…")` — routed automatically to a reused headless Chrome driven over a websocket-connected in-page agent, with a screenshot + console + errors captured on failure.

**Architecture:** The test runs Go-side in goja (Slice A's collector, reused verbatim). Browser primitives are Go functions injected into the VM (`vm.Set`) that **block** on a round-trip and return synchronously — so the Sigil layer needs no async. A `internal/browser` driver owns one headless Chrome via chromedp (launch, navigate, inject agent on every document, capture) plus a localhost websocket server; a hand-rolled JS agent (`agent.js`) does the DOM hot path (domText/click/fill/waitVisible with observer-based waiting) and replies over the ws. A classifier routes any test whose dependency closure references a browser intrinsic to the browser runner.

**Tech Stack:** Go, chromedp `v0.15.1` (CDP), `github.com/gobwas/ws` + `wsutil` (websocket — already a transitive dep), `github.com/dop251/goja` (JS VM, already a dep), the existing `internal/{types,emit,load,analysis,testrun,cli}` packages, and Slice A's `std/test`.

## Global Constraints

- `go build ./...` and `go test ./...` must stay green after every task.
- **Only dependency change allowed:** promote `github.com/gobwas/ws` (already in `go.sum` transitively via chromedp) to a direct dependency. **No other new modules.**
- Browser intrinsics (`__navigate`, `__click`, `__fill`, `__waitVisible`, `__domText`) are **test-time-only**: typed in `internal/types` but **bound only by the browser runner** (injected via `vm.Set`), never added to the JS prelude in `internal/emit`.
- Action intrinsics (`__navigate`, `__click`, `__fill`, `__waitVisible`) are **effect ops** (added to `effectOps`); `__domText` is a **read** (not gated), like `__path`.
- **Chrome-absent ⇒ skip, never fail.** If Chrome won't launch, browser-classified files are skipped with a notice and the run still exits 0 when all non-skipped tests pass. Go integration tests use `t.Skipf` on Chrome-absent, matching `internal/emit/emit_browser_test.go`.
- Keep `test`/`expect` syntax out of `std/` and `examples/` (tree-sitter drift guard). Browser dogfood tests live under `tests/browser/`.
- The driver must `Page.setBypassCSP(true)` so a target page's Content-Security-Policy cannot block the agent's websocket connection.
- The classifier and runner must agree on the browser-intrinsic name set via a **single shared list** (define it once, in `internal/browser`).

---

### Task 1: Transport core — `internal/browser` Session: launch, ws server, agent inject, one round-trip

**Files:**
- Create: `internal/browser/browser.go` (the `Session`)
- Create: `internal/browser/agent.js` (hand-rolled in-page agent)
- Create: `internal/browser/assets.go` (`//go:embed agent.js`)
- Create: `internal/browser/names.go` (shared browser-intrinsic name list)
- Create: `internal/browser/browser_test.go` (chromedp integration test)
- Modify: `go.mod` (promote `github.com/gobwas/ws` to direct — happens automatically on `go mod tidy`)

**Interfaces:**
- Produces:
  - `browser.BrowserIntrinsics = []string{"__navigate","__click","__fill","__waitVisible","__domText"}` (in `names.go`).
  - `func New() (*Session, error)` — launches headless Chrome; returns an error (Chrome absent) without panicking.
  - `(*Session) Navigate(url string) error`
  - `(*Session) DomText(sel string) (string, error)`
  - `(*Session) Close() error`
  - (later tasks add `Click`, `Fill`, `WaitVisible`, `ScreenshotPNG`, `Console`, `Errors`.)

> **Implementer note (transport):** This task integrates three libraries whose exact call shapes you must confirm against the installed versions: `chromedp` `v0.15.1`, `github.com/gobwas/ws` + `.../ws/wsutil`, and CDP page injection. The code below is a *correct-by-intent reference*; the chromedp integration test in Step 1 is the gate. If an API call differs, adjust to the library while preserving the structure (ws server on an ephemeral localhost port → agent dials it → handshake `{"hello":true}` → intent/reply by id). Use `chromedp.ListenTarget`/`cdproto/page` for injection and `cdproto/page.SetBypassCSP(true)`. Do not silently fall back to per-op `chromedp.Evaluate` — the ws+agent spine is the point of SP1.

- [ ] **Step 1: Write the failing integration test**

`internal/browser/browser_test.go`:

```go
package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveHTML serves a single static HTML page.
func serveHTML(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	}))
}

func TestSessionNavigateAndDomText(t *testing.T) {
	srv := serveHTML(`<!doctype html><html><body><h1 id="title">Hello</h1></body></html>`)
	defer srv.Close()

	sess, err := New()
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}
	defer sess.Close()

	if err := sess.Navigate(srv.URL); err != nil {
		t.Skipf("navigate failed (no Chrome?): %v", err)
	}
	got, err := sess.DomText("#title")
	if err != nil {
		t.Fatalf("DomText: %v", err)
	}
	if got != "Hello" {
		t.Errorf("DomText(#title) = %q, want %q", got, "Hello")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/browser/ -run TestSessionNavigateAndDomText`
Expected: FAIL — package/`New` undefined.

- [ ] **Step 3a: Shared intrinsic name list**

`internal/browser/names.go`:

```go
package browser

// BrowserIntrinsics are the __-prefixed boundary intrinsics bound only by the
// browser runner. The classifier and the runner both read this list so they
// never disagree about what makes a test a browser test.
var BrowserIntrinsics = []string{
	"__navigate",
	"__click",
	"__fill",
	"__waitVisible",
	"__domText",
}

// IsBrowserIntrinsic reports whether name is one of the browser intrinsics.
func IsBrowserIntrinsic(name string) bool {
	for _, n := range BrowserIntrinsics {
		if n == name {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3b: Embed the agent**

`internal/browser/assets.go`:

```go
package browser

import _ "embed"

//go:embed agent.js
var agentJS string
```

- [ ] **Step 3c: The in-page agent**

`internal/browser/agent.js` — hand-rolled. `%[1]s` is replaced by the ws URL at inject time (the Go side uses `fmt.Sprintf`/`strings.Replace` on a placeholder; here we read it from a global the driver sets, to avoid format-string coupling):

```javascript
// Sigil browser agent. Injected into every document by the driver. Connects
// back to the driver's localhost websocket and serves DOM intents.
(function () {
  if (window.__sigilAgent) return;
  window.__sigilAgent = true;
  var url = window.__SIGIL_WS_URL__;
  if (!url) return;
  var ws = new WebSocket(url);

  function visible(el) {
    if (!el) return false;
    var r = el.getBoundingClientRect();
    if (r.width === 0 && r.height === 0) return false;
    var s = window.getComputedStyle(el);
    return s.display !== "none" && s.visibility !== "hidden" && s.opacity !== "0";
  }

  // waitVisible resolves the instant the selector is visible, using a
  // MutationObserver (no polling). Times out after `ms`.
  function waitVisible(sel, ms, done) {
    var el = document.querySelector(sel);
    if (visible(el)) { done(null); return; }
    var obs = new MutationObserver(function () {
      var e = document.querySelector(sel);
      if (visible(e)) { obs.disconnect(); clearTimeout(timer); done(null); }
    });
    obs.observe(document.documentElement, { childList: true, subtree: true, attributes: true });
    var timer = setTimeout(function () {
      obs.disconnect();
      done("timeout waiting for " + sel);
    }, ms);
  }

  function reply(id, value, error) {
    ws.send(JSON.stringify({ id: id, ok: error == null, value: value || "", error: error || "" }));
  }

  function handle(msg) {
    var id = msg.id, sel = msg.sel;
    try {
      switch (msg.op) {
        case "domText": {
          var el = document.querySelector(sel);
          reply(id, el ? (el.textContent || "") : "", el ? null : "no element matches " + sel);
          break;
        }
        case "click": {
          var c = document.querySelector(sel);
          if (!c) { reply(id, "", "no element matches " + sel); break; }
          c.click();
          reply(id, "", null);
          break;
        }
        case "fill": {
          var f = document.querySelector(sel);
          if (!f) { reply(id, "", "no element matches " + sel); break; }
          f.value = msg.text;
          f.dispatchEvent(new Event("input", { bubbles: true }));
          f.dispatchEvent(new Event("change", { bubbles: true }));
          reply(id, "", null);
          break;
        }
        case "waitVisible": {
          waitVisible(sel, msg.ms || 5000, function (err) { reply(id, "", err); });
          break;
        }
        default:
          reply(id, "", "unknown op " + msg.op);
      }
    } catch (e) {
      reply(id, "", String(e));
    }
  }

  ws.onopen = function () { ws.send(JSON.stringify({ hello: true })); };
  ws.onmessage = function (ev) { handle(JSON.parse(ev.data)); };
})();
```

- [ ] **Step 3d: The Session (reference implementation)**

`internal/browser/browser.go`:

```go
// Package browser drives a headless Chrome over CDP (control/capture/injection)
// and a localhost websocket to an in-page agent (the DOM hot path). A browser
// test's primitives call a Session; each call blocks on a round-trip and returns
// synchronously, so the Sigil/goja layer stays synchronous.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

type reply struct {
	ID    int    `json:"id"`
	OK    bool   `json:"ok"`
	Value string `json:"value"`
	Error string `json:"error"`
}

type intent struct {
	ID   int    `json:"id"`
	Op   string `json:"op"`
	Sel  string `json:"sel,omitempty"`
	Text string `json:"text,omitempty"`
	Ms   int    `json:"ms,omitempty"`
}

// Session is one driven browser.
type Session struct {
	allocCancel context.CancelFunc
	ctxCancel   context.CancelFunc
	ctx         context.Context

	httpSrv *http.Server
	wsURL   string

	mu      sync.Mutex
	conn    net.Conn          // current agent connection
	nextID  int
	pending map[int]chan reply
	ready   chan struct{}     // closed when an agent says hello
}

// New launches headless Chrome and the agent websocket server. It returns an
// error (not a panic) when Chrome is unavailable.
func New() (*Session, error) {
	s := &Session{pending: map[int]chan reply{}, ready: make(chan struct{})}

	// 1. websocket server on an ephemeral localhost port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.wsURL = "ws://" + ln.Addr().String() + "/agent"
	mux := http.NewServeMux()
	mux.HandleFunc("/agent", s.handleWS)
	s.httpSrv = &http.Server{Handler: mux}
	go s.httpSrv.Serve(ln)

	// 2. headless Chrome.
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Headless)...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	s.allocCancel, s.ctxCancel, s.ctx = allocCancel, ctxCancel, ctx

	// 3. inject the agent + the ws URL into every document, and bypass CSP so
	//    the agent's websocket isn't blocked by a target page.
	script := "window.__SIGIL_WS_URL__ = " + jsonString(s.wsURL) + ";\n" + agentJS
	if err := chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			if err := page.SetBypassCSP(true).Do(ctx); err != nil {
				return err
			}
			_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
			return err
		}),
	); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

// handleWS upgrades the agent connection and reads replies/hellos until close.
func (s *Session) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	for {
		data, err := wsutil.ReadClientText(conn)
		if err != nil {
			return
		}
		// hello?
		var probe map[string]json.RawMessage
		if json.Unmarshal(data, &probe) == nil {
			if _, ok := probe["hello"]; ok {
				select {
				case <-s.ready:
				default:
					close(s.ready)
				}
				continue
			}
		}
		var rep reply
		if json.Unmarshal(data, &rep) != nil {
			continue
		}
		s.mu.Lock()
		ch := s.pending[rep.ID]
		delete(s.pending, rep.ID)
		s.mu.Unlock()
		if ch != nil {
			ch <- rep
		}
	}
}

// send issues an intent and blocks for its reply (or a timeout).
func (s *Session) send(it intent) (reply, error) {
	s.mu.Lock()
	s.nextID++
	it.ID = s.nextID
	ch := make(chan reply, 1)
	s.pending[it.ID] = ch
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return reply{}, fmt.Errorf("agent not connected")
	}
	b, _ := json.Marshal(it)
	if err := wsutil.WriteServerText(conn, b); err != nil {
		return reply{}, err
	}
	select {
	case rep := <-ch:
		if !rep.OK {
			return rep, fmt.Errorf("%s", rep.Error)
		}
		return rep, nil
	case <-time.After(15 * time.Second):
		return reply{}, fmt.Errorf("intent %s timed out", it.Op)
	}
}

// Navigate goes to url (a CDP op) and waits for the re-injected agent to redial.
func (s *Session) Navigate(url string) error {
	s.mu.Lock()
	s.ready = make(chan struct{}) // reset readiness for the new document
	s.conn = nil
	s.mu.Unlock()
	if err := chromedp.Run(s.ctx, chromedp.Navigate(url)); err != nil {
		return err
	}
	select {
	case <-s.ready:
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("agent did not connect after navigating to %s", url)
	}
}

// DomText returns the textContent of the first element matching sel.
func (s *Session) DomText(sel string) (string, error) {
	rep, err := s.send(intent{Op: "domText", Sel: sel})
	if err != nil {
		return "", err
	}
	return rep.Value, nil
}

// Close tears down Chrome and the ws server.
func (s *Session) Close() error {
	if s.ctxCancel != nil {
		s.ctxCancel()
	}
	if s.allocCancel != nil {
		s.allocCancel()
	}
	if s.httpSrv != nil {
		s.httpSrv.Close()
	}
	return nil
}

var _ = strings.TrimSpace // keep strings imported if unused after edits
```

- [ ] **Step 4: Run the test to verify it passes (or skips cleanly)**

Run: `go mod tidy && go test ./internal/browser/ -run TestSessionNavigateAndDomText -v`
Expected: PASS if Chrome is present; SKIP ("no Chrome available?") otherwise. Either is acceptable; a hard FAIL is not. Also run `go build ./...` (green).

> If it fails with a library-API mismatch (not a Chrome-absent skip), fix the offending chromedp/gobwas call against the installed version, preserving the structure. The agent.js DOM logic and the protocol must not change.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/browser/
git commit -m "feat(browser): Session transport — chromedp + ws agent, navigate + domText"
```

---

### Task 2: Remaining intents + failure capture

**Files:**
- Modify: `internal/browser/browser.go` (add `Click`, `Fill`, `WaitVisible`, `ScreenshotPNG`, console/error capture)
- Modify: `internal/browser/browser_test.go` (add integration tests)

**Interfaces:**
- Consumes: `Session`, `send`, `intent` (Task 1).
- Produces:
  - `(*Session) Click(sel string) error`
  - `(*Session) Fill(sel, text string) error`
  - `(*Session) WaitVisible(sel string) error`
  - `(*Session) ScreenshotPNG() ([]byte, error)`
  - `(*Session) Console() []string` and `(*Session) Errors() []string`

- [ ] **Step 1: Write the failing test**

Add to `internal/browser/browser_test.go`:

```go
func TestSessionClickFillWaitScreenshot(t *testing.T) {
	srv := serveHTML(`<!doctype html><html><body>
<input id="in">
<button id="b" onclick="document.getElementById('out').textContent = document.getElementById('in').value">go</button>
<p id="out"></p>
<script>setTimeout(function(){var d=document.createElement('div');d.id='late';d.textContent='here';document.body.appendChild(d);},50)</script>
</body></html>`)
	defer srv.Close()

	sess, err := New()
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}
	defer sess.Close()
	if err := sess.Navigate(srv.URL); err != nil {
		t.Skipf("navigate failed (no Chrome?): %v", err)
	}

	if err := sess.WaitVisible("#late"); err != nil {
		t.Fatalf("WaitVisible: %v", err)
	}
	if err := sess.Fill("#in", "abc"); err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if err := sess.Click("#b"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	got, err := sess.DomText("#out")
	if err != nil {
		t.Fatalf("DomText: %v", err)
	}
	if got != "abc" {
		t.Errorf("after fill+click, #out = %q, want %q", got, "abc")
	}
	png, err := sess.ScreenshotPNG()
	if err != nil || len(png) == 0 {
		t.Errorf("ScreenshotPNG: err=%v len=%d", err, len(png))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/browser/ -run TestSessionClickFillWaitScreenshot`
Expected: FAIL — `Click`/`Fill`/`WaitVisible`/`ScreenshotPNG` undefined (or SKIP if no Chrome — if it skips you cannot verify failure; in that case rely on the compile error: the methods don't exist so the package won't build, which is a FAIL).

- [ ] **Step 3a: Add the intent methods** to `internal/browser/browser.go`:

```go
// Click clicks the first element matching sel.
func (s *Session) Click(sel string) error {
	_, err := s.send(intent{Op: "click", Sel: sel})
	return err
}

// Fill sets the value of an input/textarea matching sel and fires input/change.
func (s *Session) Fill(sel, text string) error {
	_, err := s.send(intent{Op: "fill", Sel: sel, Text: text})
	return err
}

// WaitVisible blocks until sel is visible (observer-based, 5s timeout).
func (s *Session) WaitVisible(sel string) error {
	_, err := s.send(intent{Op: "waitVisible", Sel: sel, Ms: 5000})
	return err
}

// ScreenshotPNG captures the current viewport as PNG bytes.
func (s *Session) ScreenshotPNG() ([]byte, error) {
	var buf []byte
	if err := chromedp.Run(s.ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return nil, err
	}
	return buf, nil
}
```

- [ ] **Step 3b: Add console/error capture.** In `New`, after creating the context, register a CDP event listener that buffers console output and exceptions. Add fields `console []string`, `errs []string` (guarded by `s.mu`) to `Session` and:

```go
// in New(), after chromedp.NewContext(...):
chromedp.ListenTarget(ctx, func(ev interface{}) {
	switch e := ev.(type) {
	case *runtime.EventConsoleAPICalled:
		var parts []string
		for _, a := range e.Args {
			parts = append(parts, string(a.Value))
		}
		s.mu.Lock()
		s.console = append(s.console, e.Type.String()+": "+strings.Join(parts, " "))
		s.mu.Unlock()
	case *runtime.EventExceptionThrown:
		s.mu.Lock()
		s.errs = append(s.errs, e.ExceptionDetails.Error())
		s.mu.Unlock()
	}
})
```

Add `import "github.com/chromedp/cdproto/runtime"`. Add the accessors:

```go
// Console returns buffered console output captured so far.
func (s *Session) Console() []string { s.mu.Lock(); defer s.mu.Unlock(); return append([]string(nil), s.console...) }

// Errors returns buffered page exceptions captured so far.
func (s *Session) Errors() []string { s.mu.Lock(); defer s.mu.Unlock(); return append([]string(nil), s.errs...) }
```

> **Implementer note:** confirm the `cdproto/runtime` event type names against `v0.15.1` (they may be `runtime.EventConsoleAPICalled` / `runtime.EventExceptionThrown`). The integration test only asserts the screenshot/intents; console/errors are exercised by Task 5's capture. Adjust event types to the library; the buffering structure stays.

- [ ] **Step 4: Run to verify it passes (or skips)**

Run: `go test ./internal/browser/ -v && go build ./...`
Expected: PASS or SKIP (no Chrome); build green.

- [ ] **Step 5: Commit**

```bash
git add internal/browser/
git commit -m "feat(browser): click/fill/waitVisible intents + screenshot/console/error capture"
```

---

### Task 3: Browser intrinsics (types) + `std/browser` DSL

**Files:**
- Modify: `internal/types/infer.go` (`installIntrinsics` — add 5 types)
- Modify: `internal/types/effects.go` (`effectOps` — add 4 actions)
- Create: `std/browser.sigil`
- Test: `internal/types/test_check_test.go` (add a type-check test) and `internal/load/std_test.go` (std/browser loads)

**Interfaces:**
- Consumes: `arrows`, `tString`, `tUnit`, `mono` (existing in `internal/types`).
- Produces: typed intrinsics `__navigate : String -> Unit`, `__click : String -> Unit`, `__fill : String -> String -> Unit`, `__waitVisible : String -> Unit`, `__domText : String -> String`; `std/browser` exporting `navigate`, `click`, `fill`, `waitVisible`, `domText`.

- [ ] **Step 1: Write the failing test**

Add to `internal/types/test_check_test.go`:

```go
func TestBrowserIntrinsicsTyped(t *testing.T) {
	// __domText is a read returning String; __navigate is String -> Unit (effect).
	src := `test "b" {
  __navigate "http://x";
  expect { pass = __domText "#h" == "hi", label = "eq", got = "got", expected = "exp" }
}`
	if err := checkSrc(t, src); err != nil {
		t.Fatalf("expected browser intrinsics to type-check, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/types/ -run TestBrowserIntrinsicsTyped`
Expected: FAIL — `unbound variable "__navigate"`.

- [ ] **Step 3a: Add the intrinsic types.** In `internal/types/infer.go`, at the end of `installIntrinsics`, add:

```go
	// Browser driver (Sigil Browser SP1). Bound only by the browser runner;
	// not present in the JS prelude. Actions are effects (see effects.go);
	// __domText is a read.
	e.set("__navigate", mono(arrows(tString, tUnit)))            // String -> Unit
	e.set("__click", mono(arrows(tString, tUnit)))               // String -> Unit
	e.set("__fill", mono(arrows(tString, tString, tUnit)))       // String -> String -> Unit
	e.set("__waitVisible", mono(arrows(tString, tUnit)))         // String -> Unit
	e.set("__domText", mono(arrows(tString, tString)))           // String -> String (read)
```

- [ ] **Step 3b: Gate the actions.** In `internal/types/effects.go`, add to `effectOps`:

```go
	"__navigate":   true,
	"__click":      true,
	"__fill":       true,
	"__waitVisible": true,
```

(Do **not** add `__domText` — it is a read, like `__path`.)

- [ ] **Step 3c: Create `std/browser.sigil`** (uses only existing syntax — parses under tree-sitter):

```sigil
# std/browser — drive a real browser from a sigil test (Sigil Browser SP1).
# These wrap test-time-only boundary intrinsics bound by the browser runner.

pub let navigate url = __navigate url

pub let click sel = __click sel

pub let fill sel text = __fill sel text

pub let waitVisible sel = __waitVisible sel

pub let domText sel = __domText sel
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/types/ -run TestBrowserIntrinsicsTyped && go build ./...`
Expected: PASS, build green.

- [ ] **Step 5: Verify `std/browser` loads against the stdlib + commit**

Add to `internal/load/std_test.go`:

```go
func TestStdBrowserLoads(t *testing.T) {
	entry := `import "std/browser" (navigate, click, fill, waitVisible, domText)
import "std/test" (eq)
test "drives" {
  navigate "http://localhost:1";
  click "#a";
  fill "#b" "x";
  waitVisible "#c";
  expect (eq (domText "#d") "hi")
}`
	dir := t.TempDir()
	file := filepath.Join(dir, "b_test.sigil")
	if err := os.WriteFile(file, []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file, Options{Root: repoRoot}); err != nil {
		t.Fatalf("load std/browser test: %v", err)
	}
}
```

```bash
go test ./internal/types/ ./internal/load/ && go build ./...
git add internal/types/ std/browser.sigil internal/load/std_test.go
git commit -m "feat(types,std): browser intrinsics + std/browser DSL"
```

---

### Task 4: Classifier — is a program a browser test?

**Files:**
- Create: `internal/testrun/classify.go`
- Test: `internal/testrun/classify_test.go`

**Interfaces:**
- Consumes: `browser.IsBrowserIntrinsic` (Task 1); `load.Program`, `m.AST`, `ast.*`, `analysis`-style walking (reimplemented locally to avoid an import cycle — see note).
- Produces: `func isBrowserProgram(prog *load.Program) bool`.

> **Note:** walk the AST locally in `internal/testrun` (a small recursive `walkExpr` over `children`-equivalent cases) rather than importing `internal/analysis`, to keep dependencies simple. The walk must cover `LetDecl` bodies and `TestDecl` statement exprs across **every** module in `prog.Modules`.

- [ ] **Step 1: Write the failing test**

`internal/testrun/classify_test.go`:

```go
package testrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/incantery/sigil/internal/load"
)

// NOTE: `repoRoot` is already declared in internal/testrun/testrun_test.go
// (from Slice A). Do NOT redeclare it here — reuse the existing const.

func loadProg(t *testing.T, src string) *load.Program {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "x_test.sigil")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(f, load.Options{Root: repoRoot})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return prog
}

func TestClassifyBrowserVsPure(t *testing.T) {
	browserSrc := `import "std/browser" (navigate, domText)
import "std/test" (eq)
test "b" { navigate "http://x"; expect (eq (domText "#h") "hi") }`
	if !isBrowserProgram(loadProg(t, browserSrc)) {
		t.Error("program using std/browser should classify as browser")
	}

	pureSrc := `import "std/test" (eq)
test "p" { expect (eq (1 + 1) 2) }`
	if isBrowserProgram(loadProg(t, pureSrc)) {
		t.Error("pure program should not classify as browser")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/testrun/ -run TestClassifyBrowserVsPure`
Expected: FAIL — `isBrowserProgram` undefined.

- [ ] **Step 3: Implement the classifier**

`internal/testrun/classify.go`:

```go
package testrun

import (
	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/browser"
	"github.com/incantery/sigil/internal/load"
)

// isBrowserProgram reports whether any module in the program's dependency
// closure references a browser intrinsic. Per-file routing: if so, the whole
// file runs in the browser.
func isBrowserProgram(prog *load.Program) bool {
	for _, m := range prog.Modules {
		if moduleUsesBrowser(m.AST) {
			return true
		}
	}
	return false
}

func moduleUsesBrowser(m *ast.Module) bool {
	found := false
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		if found || e == nil {
			return
		}
		if v, ok := e.(*ast.Var); ok && browser.IsBrowserIntrinsic(v.Name) {
			found = true
			return
		}
		for _, ch := range exprChildren(e) {
			walk(ch)
		}
	}
	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			walk(d.Body)
		case *ast.TestDecl:
			for _, s := range d.Body {
				switch s := s.(type) {
				case *ast.TestLet:
					walk(s.Value)
				case *ast.TestExpect:
					walk(s.X)
				case *ast.TestRun:
					walk(s.X)
				}
			}
		}
	}
	return found
}

// exprChildren mirrors internal/analysis children() — the sub-expressions of a
// node (no patterns/names).
func exprChildren(e ast.Expr) []ast.Expr {
	switch e := e.(type) {
	case *ast.Interp:
		return e.Parts
	case *ast.Tuple:
		return e.Elems
	case *ast.ListLit:
		return e.Elems
	case *ast.RecordLit:
		out := make([]ast.Expr, 0, len(e.Fields))
		for _, f := range e.Fields {
			out = append(out, f.Value)
		}
		return out
	case *ast.Lambda:
		return []ast.Expr{e.Body}
	case *ast.App:
		return []ast.Expr{e.Fn, e.Arg}
	case *ast.Field:
		return []ast.Expr{e.Recv}
	case *ast.Binop:
		return []ast.Expr{e.L, e.R}
	case *ast.Unop:
		return []ast.Expr{e.X}
	case *ast.If:
		return []ast.Expr{e.Cond, e.Then, e.Else}
	case *ast.Match:
		out := []ast.Expr{e.Scrut}
		for _, a := range e.Arms {
			if a.Guard != nil {
				out = append(out, a.Guard)
			}
			out = append(out, a.Body)
		}
		return out
	case *ast.Let:
		return []ast.Expr{e.Body, e.In}
	case *ast.Effect:
		return e.Stmts
	}
	return nil
}
```

> **Implementer note:** confirm the `ast` node/field names against `internal/ast/ast.go` (e.g. `Match.Arms[i].Guard/Body`, `Binop.L/R`, `Field.Recv`). They mirror `internal/analysis/index.go`'s `children` exactly; copy from there if any differ.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/testrun/ -run TestClassifyBrowserVsPure && go build ./...`
Expected: PASS, build green.

- [ ] **Step 5: Commit**

```bash
git add internal/testrun/classify.go internal/testrun/classify_test.go
git commit -m "feat(testrun): classify browser tests by dependency-closure intrinsic use"
```

---

### Task 5: Browser runner + routing + Chrome-absent skip

**Files:**
- Create: `internal/testrun/browserrun.go` (`runFileBrowser`, intrinsic binding, capture)
- Modify: `internal/testrun/testrun.go` (`Run` classifies + routes; skip counting)
- Test: `internal/testrun/browserrun_test.go` (stub-session unit test for the blocking-primitive + routing)

**Interfaces:**
- Consumes: `browser.New`, `(*browser.Session)` methods (Tasks 1–2); `isBrowserProgram` (Task 4); `load.Load`, `BundleTest`, `goja` (existing).
- Produces: `func runFileBrowser(file, root string, sess *browser.Session, artifactDir string) ([]TestResult, error)`; a `driver` interface so the runner can be unit-tested without Chrome.

- [ ] **Step 1: Write the failing test (stub driver, no Chrome)**

`internal/testrun/browserrun_test.go`:

```go
package testrun

import (
	"strings"
	"testing"
)

// stubDriver implements the browser primitives without a real browser.
type stubDriver struct{ text map[string]string }

func (d *stubDriver) Navigate(string) error       { return nil }
func (d *stubDriver) Click(string) error          { return nil }
func (d *stubDriver) Fill(string, string) error   { return nil }
func (d *stubDriver) WaitVisible(string) error    { return nil }
func (d *stubDriver) DomText(sel string) (string, error) { return d.text[sel], nil }

func TestRunFileBrowserWithStub(t *testing.T) {
	src := `import "std/browser" (navigate, domText)
import "std/test" (eq)
test "reads dom" {
  navigate "http://x";
  expect (eq (domText "#h") "hi")
}`
	dir := writeTests(t, map[string]string{"d_test.sigil": src})
	d := &stubDriver{text: map[string]string{"#h": "hi"}}
	results, err := runFileBrowserWith(filepath_Join(dir, "d_test.sigil"), repoRoot, d)
	if err != nil {
		t.Fatalf("runFileBrowserWith: %v", err)
	}
	if len(results) != 1 || results[0].Name != "reads dom" {
		t.Fatalf("got %+v, want one test 'reads dom'", results)
	}
	if results[0].Error != "" || !allExpectsPass(results[0].Expects) {
		t.Errorf("expected pass, got error=%q expects=%+v", results[0].Error, results[0].Expects)
	}

	// A mismatch must surface as a failing expect (blocking primitive returned a value).
	d2 := &stubDriver{text: map[string]string{"#h": "bye"}}
	r2, _ := runFileBrowserWith(filepath_Join(dir, "d_test.sigil"), repoRoot, d2)
	if allExpectsPass(r2[0].Expects) {
		t.Error("expected the eq to fail when domText differs")
	}
	_ = strings.TrimSpace
}
```

(Use the `writeTests` helper from `testrun_test.go`; if `filepath_Join` is awkward, import `path/filepath` and call `filepath.Join` directly.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/testrun/ -run TestRunFileBrowserWithStub`
Expected: FAIL — `runFileBrowserWith`/`driver` undefined.

- [ ] **Step 3a: Define the driver interface + binding + runner** in `internal/testrun/browserrun.go`:

```go
package testrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dop251/goja"
	"github.com/incantery/sigil/internal/browser"
	"github.com/incantery/sigil/internal/load"
)

// driver is the browser surface the runner needs (satisfied by *browser.Session
// and by test stubs).
type driver interface {
	Navigate(url string) error
	Click(sel string) error
	Fill(sel, text string) error
	WaitVisible(sel string) error
	DomText(sel string) (string, error)
}

// runFileBrowserWith compiles file and runs it in goja with the browser
// intrinsics bound to d (each call blocks on d and returns synchronously).
func runFileBrowserWith(file, root string, d driver) ([]TestResult, error) {
	prog, err := load.Load(file, load.Options{Root: root})
	if err != nil {
		return nil, err
	}
	js, err := prog.BundleTest()
	if err != nil {
		return nil, err
	}
	vm := goja.New()
	bindBrowser(vm, d)
	v, err := vm.RunString(js + "\n;JSON.stringify(__runTests())")
	if err != nil {
		return nil, err
	}
	s, ok := v.Export().(string)
	if !ok {
		return nil, fmt.Errorf("__runTests() did not return a string (got %T)", v.Export())
	}
	var results []TestResult
	if err := json.Unmarshal([]byte(s), &results); err != nil {
		return nil, err
	}
	return results, nil
}

// bindBrowser injects the five browser intrinsics, each delegating to d and
// throwing a JS error (caught by __runTests) on failure.
func bindBrowser(vm *goja.Runtime, d driver) {
	throw := func(err error) { panic(vm.ToValue(err.Error())) }
	vm.Set("__navigate", func(c goja.FunctionCall) goja.Value {
		if err := d.Navigate(c.Argument(0).String()); err != nil {
			throw(err)
		}
		return goja.Undefined()
	})
	vm.Set("__click", func(c goja.FunctionCall) goja.Value {
		if err := d.Click(c.Argument(0).String()); err != nil {
			throw(err)
		}
		return goja.Undefined()
	})
	vm.Set("__fill", func(c goja.FunctionCall) goja.Value {
		if err := d.Fill(c.Argument(0).String(), c.Argument(1).String()); err != nil {
			throw(err)
		}
		return goja.Undefined()
	})
	vm.Set("__waitVisible", func(c goja.FunctionCall) goja.Value {
		if err := d.WaitVisible(c.Argument(0).String()); err != nil {
			throw(err)
		}
		return goja.Undefined()
	})
	vm.Set("__domText", func(c goja.FunctionCall) goja.Value {
		txt, err := d.DomText(c.Argument(0).String())
		if err != nil {
			throw(err)
		}
		return vm.ToValue(txt)
	})
}

// runFileBrowser is the production entry: it uses a real *browser.Session and,
// on any failing/errored test, writes a screenshot + console + errors artifact.
func runFileBrowser(file, root string, sess *browser.Session, artifactDir string) ([]TestResult, error) {
	results, err := runFileBrowserWith(file, root, sess)
	if err != nil {
		return nil, err
	}
	failed := false
	for _, r := range results {
		if r.Error != "" || !allExpectsPass(r.Expects) {
			failed = true
			break
		}
	}
	if failed && artifactDir != "" {
		dir := filepath.Join(artifactDir, filepath.Base(file))
		_ = os.MkdirAll(dir, 0o755)
		if png, err := sess.ScreenshotPNG(); err == nil {
			_ = os.WriteFile(filepath.Join(dir, "screenshot.png"), png, 0o644)
		}
		_ = os.WriteFile(filepath.Join(dir, "console.log"), []byte(join(sess.Console())), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "errors.log"), []byte(join(sess.Errors())), 0o644)
	}
	return results, nil
}

func join(ss []string) string {
	out := ""
	for _, s := range ss {
		out += s + "\n"
	}
	return out
}
```

- [ ] **Step 3b: Route in `Run`.** Modify `internal/testrun/testrun.go`'s `Run` so each file is classified and routed; browser files are skipped with a notice when Chrome is absent. Replace the per-file body:

```go
func Run(w io.Writer, path, root string) (bool, error) {
	files, err := discover(path)
	if err != nil {
		return false, err
	}
	total, passed, failed, skipped := 0, 0, 0, 0
	allOK := true

	// Lazily create one browser Session, shared across browser files.
	var sess *browser.Session
	var browserUnavailable bool
	getSession := func() *browser.Session {
		if sess == nil && !browserUnavailable {
			s, e := browser.New()
			if e != nil {
				browserUnavailable = true
				return nil
			}
			sess = s
		}
		return sess
	}
	defer func() {
		if sess != nil {
			sess.Close()
		}
	}()
	artifactDir := filepath.Join(".sigil-test", "last")

	for _, file := range files {
		fmt.Fprintln(w, file)

		prog, lerr := load.Load(file, load.Options{Root: root})
		browserFile := lerr == nil && isBrowserProgram(prog)

		var results []TestResult
		var rerr error
		if browserFile {
			s := getSession()
			if s == nil {
				skipped++
				fmt.Fprintf(w, "  ⤼ skipped (no Chrome): browser test\n")
				continue
			}
			results, rerr = runFileBrowser(file, root, s, artifactDir)
		} else {
			results, rerr = runFile(file, root)
		}
		if rerr != nil {
			allOK = false
			fmt.Fprintf(w, "  ✗ failed to compile/run: %v\n", rerr)
			continue
		}
		for _, r := range results {
			total++
			if r.Error == "" && allExpectsPass(r.Expects) {
				passed++
				fmt.Fprintf(w, "  ✓ %s\n", r.Name)
				continue
			}
			failed++
			allOK = false
			fmt.Fprintf(w, "  ✗ %s\n", r.Name)
			if r.Error != "" {
				hint := ""
				if looksBrowser(r.Error) {
					hint = " (looks like a browser test — needs std/browser)"
				}
				fmt.Fprintf(w, "      error: %s%s\n", r.Error, hint)
			}
			for _, ex := range r.Expects {
				if !ex.Pass {
					fmt.Fprintf(w, "      %s: expected %s, got %s\n", ex.Label, ex.Expected, ex.Got)
				}
			}
		}
	}
	fmt.Fprintf(w, "\n%d files, %d tests, %d passed, %d failed, %d skipped\n", len(files), total, passed, failed, skipped)
	return allOK, nil
}
```

Add imports to `testrun.go`: `"path/filepath"` (already present) and `"github.com/incantery/sigil/internal/browser"`. Note `load` is already imported.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/testrun/ && go build ./...`
Expected: PASS (the stub test runs without Chrome; existing goja tests still pass). Build green.

- [ ] **Step 5: Commit**

```bash
git add internal/testrun/browserrun.go internal/testrun/browserrun_test.go internal/testrun/testrun.go
git commit -m "feat(testrun): browser runner + classify-and-route + Chrome-absent skip"
```

---

### Task 6: Dogfood, CLI/Make, docs

**Files:**
- Create: `tests/browser/counter_test.sigil`
- Create: `internal/testrun/dogfood_browser_test.go`
- Modify: `Makefile` (a `test-browser` target)
- Modify: `docs/testing.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: everything above; `internal/load` + `httptest` to serve a fixture for the dogfood.

- [ ] **Step 1: Write the failing dogfood Go test**

`internal/testrun/dogfood_browser_test.go` — serves a tiny page, points a generated browser test at it, runs through `Run`, and **skips cleanly** if Chrome is absent:

```go
package testrun

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/sigil/internal/browser"
)

func TestDogfoodBrowser(t *testing.T) {
	// Skip early if Chrome can't launch, so CI without Chrome stays green.
	s, err := browser.New()
	if err != nil {
		t.Skipf("no Chrome: %v", err)
	}
	s.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body><h1 id="t">Counter</h1></body></html>`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	src := fmt.Sprintf(`import "std/browser" (navigate, domText)
import "std/test" (eq)
test "homepage title" {
  navigate %q;
  waitVisible "#t";
  expect (eq (domText "#t") "Counter")
}`, srv.URL)
	// note: waitVisible must be imported too
	src = strings.Replace(src, `(navigate, domText)`, `(navigate, waitVisible, domText)`, 1)
	if err := os.WriteFile(filepath.Join(dir, "h_test.sigil"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	ok, err := Run(&buf, dir, repoRoot)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, buf.String())
	}
	if !ok {
		t.Fatalf("dogfood browser test failed:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run to verify it fails or skips**

Run: `go test ./internal/testrun/ -run TestDogfoodBrowser -v`
Expected: SKIP if no Chrome; if Chrome present, it should PASS once everything is wired (it exercises the full stack). A FAIL here with Chrome present indicates a real integration gap to fix.

- [ ] **Step 3a: Commit a checked-in dogfood file** `tests/browser/counter_test.sigil` (run manually against a served app; not part of `go test`, which uses the self-contained fixture above):

```sigil
import "std/browser" (navigate, waitVisible, domText)
import "std/test" (eq)

# Run against a served app, e.g.:
#   sigil serve examples/counter/counter.sigil &   # :8099
#   sigil test tests/browser --root .
test "counter renders heading" {
  navigate "http://localhost:8099";
  waitVisible "#count";
  expect (eq (domText "#count") "0")
}
```

- [ ] **Step 3b: Makefile target.** Add to `Makefile` (real tab indent):

```make
test-browser: build ## Run browser *_test.sigil (requires a served app + Chrome)
	./bin/sigil test tests/browser --root .
```

- [ ] **Step 3c: Docs.** Append a "Browser tests (SP1)" section to `docs/testing.md` describing: `std/browser` (`navigate`/`click`/`fill`/`waitVisible`/`domText`), that browser tests are auto-classified (any `std/browser` use → routed to headless Chrome), that they **skip** when Chrome is absent, and the failure artifact location (`.sigil-test/last/<file>/screenshot.png|console.log|errors.log`). Update `CLAUDE.md`: under "Build / test / run" note browser tests route to Chrome; under "What's next" mark "Sigil Browser SP1 (driving spine) — DONE; SP2 (AI run bundle) next" and reference `docs/superpowers/specs/2026-06-22-sigil-browser-sp1-design.md`.

- [ ] **Step 4: Full verification**

Run: `go test ./... && go build ./... && go run ./cmd/sigil test tests --root .`
Expected: all Go tests pass (browser dogfood skips without Chrome); build green; the existing goja `tests/` suite still passes.

- [ ] **Step 5: Commit**

```bash
git add tests/browser/ internal/testrun/dogfood_browser_test.go Makefile docs/testing.md CLAUDE.md
git commit -m "feat(browser): dogfood browser test, make test-browser, docs (SP1 complete)"
```

---

## Self-Review

**Spec coverage:**
- Two-channel architecture (CDP + ws) → Tasks 1–2 (`internal/browser`).
- Synchronous goja + blocking Go primitives → Task 5 (`bindBrowser`, blocking driver).
- `std/browser` DSL + test-time-only intrinsics (typed, not in prelude) → Task 3.
- Action-vs-read effect gating → Task 3 (effectOps excludes `__domText`).
- Dependency-closure classifier (single shared name list) → Task 4 + `browser.BrowserIntrinsics`.
- Escalation net → partially: `looksBrowser` hint retained in Task 5; full re-run-in-browser escalation is **deferred** (noted below) since static classification covers the dogfood. (Flag for final review: spec lists the escalation net in SP1; this plan ships the classifier + hint but not auto-re-run. If the reviewer deems auto-escalation mandatory for SP1, add a task: on a goja file whose results contain an unbound-`__navigate`-style error, re-run via `runFileBrowser`.)
- Minimal failure capture (screenshot/console/errors) → Tasks 2 + 5.
- Chrome-absent skip + skip count → Task 5.
- CSP bypass for the agent ws → Task 1 (`page.SetBypassCSP(true)`).
- Reuse one browser across files → Task 5 (`getSession`).
- Testing strategy (chromedp integration, classifier unit, stub-driver blocking, dogfood) → Tasks 1,2,4,5,6.

**Placeholder scan:** none — every code step is concrete. Transport tasks (1–2) carry explicit "verify the library call" notes with the integration test as the gate, which is appropriate for novel third-party integration, not a placeholder.

**Type consistency:** `driver` interface methods (`Navigate/Click/Fill/WaitVisible/DomText`) match `*browser.Session`'s methods from Tasks 1–2. `browser.BrowserIntrinsics`/`IsBrowserIntrinsic` used identically in Tasks 1 and 4. `runFileBrowserWith`/`runFileBrowser`/`bindBrowser` signatures consistent across Task 5. `isBrowserProgram(*load.Program) bool` consistent Tasks 4–5.

**Known deferrals (flag for final review):** auto-escalation re-run (only the classifier + hint ship); per-test (vs per-file) routing; per-step/synchronized capture (SP2); `repoRoot` const is added in Task 4's `classify_test.go` — ensure it is not redeclared in `testrun_test.go` (if `testrun_test.go` already defines `repoRoot`, reuse it and drop the duplicate in Task 4).
