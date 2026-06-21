# `sigil dev` HMR Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `sigil dev` command that watches the source tree, rebuilds on change, and performs state-preserving in-place hot module replacement in the browser; redefine `sigil serve` as a build-once production server.

**Architecture:** A new `internal/devserver` package owns a mtime-polling watcher, an SSE hub, and the HTTP handlers (shell page, embedded client agent, event stream). On a `.sigil` change it rebuilds a **dev** bundle (`emit` with a dev-variant prelude) and pushes the new bundle source over SSE. The browser-side agent snapshots reactive cell values by call order, disposes global listeners, empties `#app`, evals the new bundle via `new Function`, and the dev `__cell` rehydrates each cell from the snapshot by index. Production emit is untouched.

**Tech Stack:** Go (stdlib `net/http` SSE, `os` mtime polling, `//go:embed` for the agent), cobra CLI, goja (headless JS tests), chromedp (browser e2e). No new dependencies.

## Global Constraints

- No new Go module dependencies — SSE over stdlib `net/http`, watching via `os.Stat` mtime polling.
- Production output must stay byte-for-byte identical: `emit.Bundle` / `load.Program.Bundle` keep emitting today's prelude. A golden test enforces this.
- Default dev port `8099`; flags `--root` (default `.`) and `--port` mirror `serve`/`build`.
- The dev prelude is **derived** from the production `prelude` by exact string replacement of four intrinsic definitions, with a package-init guard that panics if any replacement target is not found exactly once (prevents silent drift).
- The dev prelude's instrumented intrinsics read a global `__sigilDev` registry object; they never assign it (strict-mode safe). The agent (browser) and the goja test (headless) each set up `__sigilDev` before the bundle runs.
- chromedp/goja tests skip when the engine is absent, matching the existing suite.
- Follow existing repo idioms: cobra command constructors (`newXxxCmd`), the `run(args...)` CLI test helper, table-free focused tests.

---

### Task 1: Dev-variant prelude + dev bundle path in `emit`

**Files:**
- Modify: `internal/emit/emit.go` (add `devPrelude`, `BundleDev`; refactor `Bundle`)
- Test: `internal/emit/dev_test.go` (create)

**Interfaces:**
- Consumes: existing `prelude` const, existing `Bundle(mods []LinkedModule, env *peval.Env) (string, error)`.
- Produces:
  - `func BundleDev(mods []LinkedModule, env *peval.Env) (string, error)` — identical to `Bundle` but emits `devPrelude`.
  - `var devPrelude string` — the production prelude with `__cell`, `__onPopState`, `__installStyles`, `__fetch` swapped for instrumented versions that call into a global `__sigilDev`.

- [ ] **Step 1: Write the failing test**

Create `internal/emit/dev_test.go`:

```go
package emit

import (
	"strings"
	"testing"
)

// Production prelude must remain exactly today's bytes.
func TestProdPreludeUnchanged(t *testing.T) {
	if !strings.Contains(prelude, "const __cell = (init) => ({ v: init, subs: new Set() });") {
		t.Fatal("production __cell line changed; update the golden expectation deliberately")
	}
}

// The dev prelude swaps in the instrumented intrinsics.
func TestDevPreludeInstrumented(t *testing.T) {
	if strings.Contains(devPrelude, "const __cell = (init) => ({ v: init, subs: new Set() });") {
		t.Error("dev prelude still has the production __cell")
	}
	if !strings.Contains(devPrelude, "__sigilDev.counter++") {
		t.Error("dev __cell must take a call-order index from __sigilDev.counter")
	}
	if !strings.Contains(devPrelude, "__sigilDev.hydration") {
		t.Error("dev __cell must consult the hydration map")
	}
	if !strings.Contains(devPrelude, "__sigilDev.disposers.push") {
		t.Error("dev __onPopState must register a disposer")
	}
	if !strings.Contains(devPrelude, `getElementById("__sigil_styles")`) {
		t.Error("dev __installStyles must reuse a keyed <style> tag")
	}
	if !strings.Contains(devPrelude, "__sigilDev.generation") {
		t.Error("dev __fetch must guard on generation")
	}
	// Everything else (e.g. __each) is preserved verbatim.
	if !strings.Contains(devPrelude, "const __each = (src) => (render) => {") {
		t.Error("dev prelude dropped a non-instrumented intrinsic")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/emit/ -run 'TestDevPrelude|TestProdPrelude' -v`
Expected: FAIL — `devPrelude` undefined (compile error).

- [ ] **Step 3: Add `devPrelude` and `BundleDev`**

In `internal/emit/emit.go`, after the `prelude` const + `func Runtime()`, add:

```go
// devPrelude is the production prelude with four intrinsics swapped for
// HMR-instrumented versions that call into a global __sigilDev registry (set up
// by the dev client agent, or by tests). It is derived from prelude by exact
// string replacement so any future prelude edit propagates; the replacements are
// guarded below.
var devPrelude = buildDevPrelude()

type preludeSwap struct{ from, to string }

var devSwaps = []preludeSwap{
	{
		from: `const __cell = (init) => ({ v: init, subs: new Set() });`,
		to:   `const __cell = (init) => { const __i = __sigilDev.counter++; const __v = __sigilDev.hydration.has(__i) ? __sigilDev.hydration.get(__i) : init; const __c = { v: __v, subs: new Set() }; __sigilDev.cells.set(__i, __c); return __c; };`,
	},
	{
		from: `const __onPopState = (cb) => { window.addEventListener("popstate", () => cb()); return null; };`,
		to:   `const __onPopState = (cb) => { const __h = () => cb(); window.addEventListener("popstate", __h); __sigilDev.disposers.push(() => window.removeEventListener("popstate", __h)); return null; };`,
	},
	{
		from: `const __installStyles = (css) => { const s = document.createElement("style"); s.textContent = css; document.head.appendChild(s); };`,
		to:   `const __installStyles = (css) => { let __s = document.getElementById("__sigil_styles"); if (!__s) { __s = document.createElement("style"); __s.id = "__sigil_styles"; document.head.appendChild(__s); } __s.textContent = css; };`,
	},
	{
		from: "const __fetch = (url) => (cb) => {\n  fetch(url).then((r) => r.text().then((t) => cb(r.ok)(t)())).catch((e) => cb(false)(String(e))());\n  return null;\n};",
		to:   "const __fetch = (url) => (cb) => {\n  const __g = __sigilDev.generation; const __live = () => __g === __sigilDev.generation;\n  fetch(url).then((r) => r.text().then((t) => { if (__live()) cb(r.ok)(t)(); })).catch((e) => { if (__live()) cb(false)(String(e))(); });\n  return null;\n};",
	},
}

func buildDevPrelude() string {
	p := prelude
	for _, s := range devSwaps {
		if strings.Count(p, s.from) != 1 {
			panic("emit: dev prelude swap target not found exactly once: " + s.from)
		}
		p = strings.Replace(p, s.from, s.to, 1)
	}
	return p
}
```

Then refactor `Bundle` to share its body. Replace the existing `func Bundle(...)` so the prelude is parameterized:

```go
func Bundle(mods []LinkedModule, env *peval.Env) (string, error) {
	return bundle(mods, env, prelude)
}

// BundleDev is Bundle with the HMR-instrumented dev prelude.
func BundleDev(mods []LinkedModule, env *peval.Env) (string, error) {
	return bundle(mods, env, devPrelude)
}

func bundle(mods []LinkedModule, env *peval.Env, pre string) (string, error) {
```

Inside that moved body, change the single line `b.WriteString(prelude)` (the one near the end that prepends the runtime to the bundle — NOT the one in `Module`) to `b.WriteString(pre)`. Leave everything else identical.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/emit/ -run 'TestDevPrelude|TestProdPrelude' -v`
Expected: PASS.

- [ ] **Step 5: Run the whole emit + load suite to confirm production output is unchanged**

Run: `go test ./internal/emit/ ./internal/load/`
Expected: PASS (existing golden/browser/goja tests still green — proves `Bundle` output unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/emit/emit.go internal/emit/dev_test.go
git commit -m "feat(emit): dev-variant prelude + BundleDev for HMR"
```

---

### Task 2: goja test — dev `__cell` call-order hydration

**Files:**
- Test: `internal/emit/dev_hydrate_test.go` (create)

**Interfaces:**
- Consumes: `devPrelude` (Task 1).
- Produces: nothing new — a behavioral guard that the dev `__cell` adopts hydration values by creation order.

- [ ] **Step 1: Write the failing test**

Create `internal/emit/dev_hydrate_test.go`:

```go
package emit

import (
	"testing"

	"github.com/dop251/goja"
)

// The dev __cell takes its identity from creation order: with a hydration map
// {0: 41, 1: 99}, the first cell created starts at 41 and the second at 99,
// regardless of their init values. A cell with no hydration entry uses init.
func TestDevCellHydratesByOrder(t *testing.T) {
	vm := goja.New()
	// Stand in for the browser/agent registry.
	_, err := vm.RunString(`
var __sigilDev = {
  counter: 0,
  hydration: new Map([[0, 41], [1, 99]]),
  cells: new Map(),
  disposers: [],
  generation: 0,
};
`)
	if err != nil {
		t.Fatal(err)
	}
	// Run the dev prelude, then create three cells with init 0, 0, 7.
	v, err := vm.RunString(devPrelude + `
;(() => {
  const a = __cell(0);
  const b = __cell(0);
  const c = __cell(7);
  return [a.v, b.v, c.v];
})()`)
	if err != nil {
		t.Fatalf("JS error: %v", err)
	}
	got := v.Export().([]any)
	if got[0] != int64(41) || got[1] != int64(99) || got[2] != int64(7) {
		t.Errorf("hydrated values = %v, want [41 99 7]", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (or passes only with Task 1 present)**

Run: `go test ./internal/emit/ -run TestDevCellHydratesByOrder -v`
Expected: PASS if Task 1 is correct. If it FAILS, the dev `__cell` swap is wrong — fix `devSwaps` in Task 1 before continuing.

- [ ] **Step 3: Commit**

```bash
git add internal/emit/dev_hydrate_test.go
git commit -m "test(emit): dev __cell hydrates cells by creation order"
```

---

### Task 3: dev bundle path through `load`

**Files:**
- Modify: `internal/load/load.go` (`Bundle` → share body; add `BundleDev`)
- Test: `internal/load/dev_test.go` (create)

**Interfaces:**
- Consumes: `emit.BundleDev` (Task 1), existing `Program.Bundle`.
- Produces: `func (p *Program) BundleDev() (string, error)` — links the program and emits the dev prelude.

- [ ] **Step 1: Write the failing test**

Create `internal/load/dev_test.go`:

```go
package load

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProgramBundleDev(t *testing.T) {
	entry := filepath.Join("..", "..", "examples", "counter", "counter.sigil")
	prog, err := Load(entry, Options{Root: filepath.Join("..", "..")})
	if err != nil {
		t.Fatal(err)
	}
	dev, err := prog.BundleDev()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dev, "__sigilDev.counter++") {
		t.Error("dev bundle is missing the instrumented __cell")
	}
	prod, err := prog.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prod, "__sigilDev") {
		t.Error("production bundle must not contain dev instrumentation")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/load/ -run TestProgramBundleDev -v`
Expected: FAIL — `BundleDev` undefined.

- [ ] **Step 3: Implement**

In `internal/load/load.go`, refactor `Bundle` to share linking, add `BundleDev`:

```go
func (p *Program) Bundle() (string, error)    { return p.bundle(false) }
func (p *Program) BundleDev() (string, error) { return p.bundle(true) }

func (p *Program) bundle(dev bool) (string, error) {
	linked := make([]emit.LinkedModule, len(p.Modules))
	env := peval.NewEnv()
	for i, m := range p.Modules {
		env.AddModule(m.AST)
		linked[i] = emit.LinkedModule{
			ID:      m.ID,
			AST:     m.AST,
			Imports: importBindings(m),
			Exports: exportNames(m),
		}
	}
	if dev {
		return emit.BundleDev(linked, env)
	}
	return emit.Bundle(linked, env)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/load/ -run TestProgramBundleDev -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/load/load.go internal/load/dev_test.go
git commit -m "feat(load): Program.BundleDev links with the dev prelude"
```

---

### Task 4: client agent (`agent.js`) as an embedded asset

**Files:**
- Create: `internal/devserver/agent.js`
- Create: `internal/devserver/assets.go` (embeds agent.js)
- Test: `internal/devserver/assets_test.go` (create)

**Interfaces:**
- Consumes: nothing (browser-side JS, plus a Go `//go:embed`).
- Produces: `var AgentJS string` (the embedded agent source) in package `devserver`.

The agent owns the `__sigilDev` registry and the HMR lifecycle. Full behavioral verification is the e2e (Task 8); this task ships the asset and a Go test that it embeds and contains the lifecycle hooks.

- [ ] **Step 1: Write `internal/devserver/agent.js`**

```js
"use strict";
// Sigil dev client agent. Owns the cross-reload reactive registry and the
// in-place hot-module-replacement lifecycle. Loaded before the initial bundle.
(function () {
  window.__sigilDev = {
    counter: 0,
    hydration: new Map(),
    cells: new Map(),
    disposers: [],
    generation: 0,
  };

  function runBundle(src) {
    // Fresh function scope each time: the bundle redeclares its own const
    // intrinsics without colliding with a prior eval.
    new Function(src)();
  }

  function teardown() {
    var dev = window.__sigilDev;
    // Snapshot live cell values by their creation-order index.
    var snap = new Map();
    dev.cells.forEach(function (cell, i) { snap.set(i, cell.v); });
    // Dispose global listeners (popstate, etc.) and invalidate in-flight fetches.
    dev.disposers.forEach(function (d) { try { d(); } catch (e) {} });
    dev.disposers = [];
    dev.generation++;
    // Empty the mount root.
    var app = document.querySelector("#app");
    if (app) app.replaceChildren();
    dev.cells = new Map();
    dev.counter = 0;
    return snap;
  }

  function hotSwap(src) {
    var snap = teardown();
    window.__sigilDev.hydration = snap;
    runBundle(src);
    window.__sigilDev.hydration = new Map();
    hideOverlay();
  }

  // --- build-error overlay -------------------------------------------------
  function overlay() {
    var el = document.getElementById("__sigil_overlay");
    if (!el) {
      el = document.createElement("pre");
      el.id = "__sigil_overlay";
      el.style.cssText =
        "position:fixed;inset:0;margin:0;padding:24px;z-index:2147483647;" +
        "background:rgba(20,20,20,.95);color:#f88;font:13px/1.5 monospace;" +
        "white-space:pre-wrap;overflow:auto;";
      el.addEventListener("click", hideOverlay);
      document.body.appendChild(el);
    }
    return el;
  }
  function showOverlay(msg) { overlay().textContent = "build error\n\n" + msg + "\n\n(click to dismiss)"; }
  function hideOverlay() {
    var el = document.getElementById("__sigil_overlay");
    if (el) el.remove();
  }

  // --- live connection -----------------------------------------------------
  var es = new EventSource("/__sigil/events");
  es.onmessage = function (e) {
    var msg = JSON.parse(e.data);
    if (msg.type === "reload") hotSwap(msg.bundle);
    else if (msg.type === "error") showOverlay(msg.message);
  };
})();
```

- [ ] **Step 2: Write the embedding + failing test**

Create `internal/devserver/assets.go`:

```go
package devserver

import _ "embed"

//go:embed agent.js
var AgentJS string
```

Create `internal/devserver/assets_test.go`:

```go
package devserver

import (
	"strings"
	"testing"
)

func TestAgentJSEmbedded(t *testing.T) {
	for _, want := range []string{
		"window.__sigilDev",
		"new EventSource(\"/__sigil/events\")",
		"function hotSwap",
		"replaceChildren()",
		"__sigil_overlay",
	} {
		if !strings.Contains(AgentJS, want) {
			t.Errorf("agent.js missing %q", want)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

Run: `go test ./internal/devserver/ -run TestAgentJSEmbedded -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/devserver/agent.js internal/devserver/assets.go internal/devserver/assets_test.go
git commit -m "feat(devserver): embedded client agent with HMR lifecycle + error overlay"
```

---

### Task 5: mtime-polling watcher

**Files:**
- Create: `internal/devserver/watch.go`
- Test: `internal/devserver/watch_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func Snapshot(root string) (map[string]int64, error)` — maps every `.sigil` file under `root` to its mtime in unix-nanos.
  - `func Changed(prev, cur map[string]int64) bool` — true if any file was added, removed, or has a newer mtime.
  - `func Watch(root string, interval time.Duration, onChange func()) (stop func())` — polls on `interval`, calls `onChange` once per detected change (coalescing a burst into the next tick).

- [ ] **Step 1: Write the failing test**

Create `internal/devserver/watch_test.go`:

```go
package devserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotAndChanged(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.sigil")
	if err := os.WriteFile(a, []byte("pub let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A non-sigil file is ignored.
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	s1, err := Snapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s1) != 1 {
		t.Fatalf("snapshot tracked %d files, want 1 (.sigil only)", len(s1))
	}
	if Changed(s1, s1) {
		t.Error("identical snapshots reported as changed")
	}

	// Touch with a strictly newer mtime so the test is not clock-resolution flaky.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(a, future, future); err != nil {
		t.Fatal(err)
	}
	s2, err := Snapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !Changed(s1, s2) {
		t.Error("modified file not detected as changed")
	}
}

func TestWatchFiresOnChange(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.sigil")
	if err := os.WriteFile(a, []byte("pub let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fired := make(chan struct{}, 4)
	stop := Watch(dir, 15*time.Millisecond, func() { fired <- struct{}{} })
	defer stop()

	time.Sleep(30 * time.Millisecond) // let the baseline snapshot settle
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(a, future, future); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not fire on change")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/devserver/ -run 'TestSnapshot|TestWatch' -v`
Expected: FAIL — `Snapshot` undefined.

- [ ] **Step 3: Implement `internal/devserver/watch.go`**

```go
package devserver

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot maps every .sigil file under root to its mtime (unix nanos).
func Snapshot(root string) (map[string]int64, error) {
	out := map[string]int64{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sigil") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[path] = info.ModTime().UnixNano()
		return nil
	})
	return out, err
}

// Changed reports whether any file was added, removed, or has a newer mtime.
func Changed(prev, cur map[string]int64) bool {
	if len(prev) != len(cur) {
		return true
	}
	for p, m := range cur {
		if prev[p] != m {
			return true
		}
	}
	return false
}

// Watch polls root every interval and calls onChange whenever the .sigil set
// changes. A burst of edits between ticks coalesces into a single onChange. The
// returned stop function ends polling.
func Watch(root string, interval time.Duration, onChange func()) (stop func()) {
	done := make(chan struct{})
	go func() {
		prev, _ := Snapshot(root)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				cur, err := Snapshot(root)
				if err != nil {
					continue
				}
				if Changed(prev, cur) {
					prev = cur
					onChange()
				}
			}
		}
	}()
	return func() { close(done) }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/devserver/ -run 'TestSnapshot|TestWatch' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devserver/watch.go internal/devserver/watch_test.go
git commit -m "feat(devserver): mtime-polling .sigil watcher"
```

---

### Task 6: SSE hub + messages

**Files:**
- Create: `internal/devserver/sse.go`
- Test: `internal/devserver/sse_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Hub` with `func NewHub() *Hub`, `func (h *Hub) Subscribe() (<-chan string, func())`, `func (h *Hub) Broadcast(msg string)`, and `func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request)` (the `/__sigil/events` handler).
  - `func ReloadMsg(bundle string) string` → `{"type":"reload","bundle":...}`.
  - `func ErrorMsg(message string) string` → `{"type":"error","message":...}`.

- [ ] **Step 1: Write the failing test**

Create `internal/devserver/sse_test.go`:

```go
package devserver

import (
	"strings"
	"testing"
	"time"
)

func TestMessageFraming(t *testing.T) {
	r := ReloadMsg(`console.log("hi")`)
	if !strings.Contains(r, `"type":"reload"`) || !strings.Contains(r, `console.log`) {
		t.Errorf("reload msg malformed: %s", r)
	}
	e := ErrorMsg("type error at 3:1")
	if !strings.Contains(e, `"type":"error"`) || !strings.Contains(e, "type error") {
		t.Errorf("error msg malformed: %s", e)
	}
	// Must be single-line JSON so SSE `data:` framing stays one event.
	if strings.Contains(r, "\n") || strings.Contains(e, "\n") {
		t.Error("messages must be newline-free for SSE framing")
	}
}

func TestHubBroadcast(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	defer cancel()
	h.Broadcast("hello")
	select {
	case got := <-ch:
		if got != "hello" {
			t.Errorf("got %q, want hello", got)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber received nothing")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/devserver/ -run 'TestMessageFraming|TestHubBroadcast' -v`
Expected: FAIL — undefined `ReloadMsg`/`NewHub`.

- [ ] **Step 3: Implement `internal/devserver/sse.go`**

```go
package devserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// Hub fans dev events out to every connected browser over Server-Sent Events.
type Hub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan string]struct{}{}} }

// Subscribe returns a channel of messages and a cancel function.
func (h *Hub) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// Broadcast delivers msg to every subscriber, dropping it for any slow consumer.
func (h *Hub) Broadcast(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// ServeHTTP is the /__sigil/events SSE endpoint.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := h.Subscribe()
	defer cancel()
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func ReloadMsg(bundle string) string { return marshal("reload", "bundle", bundle) }
func ErrorMsg(message string) string { return marshal("error", "message", message) }

func marshal(typ, key, val string) string {
	b, _ := json.Marshal(map[string]string{"type": typ, key: val})
	return string(b)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/devserver/ -run 'TestMessageFraming|TestHubBroadcast' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devserver/sse.go internal/devserver/sse_test.go
git commit -m "feat(devserver): SSE hub + reload/error message framing"
```

---

### Task 7: dev `Server` (HTTP handlers + rebuild wiring)

**Files:**
- Create: `internal/devserver/server.go`
- Test: `internal/devserver/server_test.go` (create)

**Interfaces:**
- Consumes: `AgentJS` (Task 4), `Watch` (Task 5), `Hub`/`ReloadMsg`/`ErrorMsg` (Task 6).
- Produces:
  - `type Server` with `func New(entry, root string) *Server`.
  - `func (s *Server) Handler() http.Handler` — routes `/` (shell), `/__sigil/agent.js`, `/__sigil/events`.
  - `func (s *Server) Rebuild()` — builds the dev bundle and broadcasts `reload` or `error`.
  - `func (s *Server) ListenAndServe(addr string) error` — starts the watcher then serves.
  - The shell page embeds the initial dev bundle inline after the agent `<script>`.

The build dependency: `Server.Rebuild` needs a dev bundle from an entry+root. Define a small builder seam so the package does not import `cli`:

```go
type BuildFunc func(entry, root string) (string, error)
```

`New` takes the entry/root and a `BuildFunc`; `internal/cli` will pass one backed by `load.Program.BundleDev` (Task 8).

- [ ] **Step 1: Write the failing test**

Create `internal/devserver/server_test.go`:

```go
package devserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func okBuild(entry, root string) (string, error) {
	return `window.__built = "yes";`, nil
}

func TestShellServesAgentAndBundle(t *testing.T) {
	s := New("entry.sigil", ".", okBuild)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// Shell page references the agent and inlines the initial bundle.
	body := get(t, srv.URL+"/")
	if !strings.Contains(body, `<script src="/__sigil/agent.js"></script>`) {
		t.Error("shell missing agent script tag")
	}
	if !strings.Contains(body, `window.__built = "yes";`) {
		t.Error("shell missing inlined initial bundle")
	}
	if !strings.Contains(body, `id="app"`) {
		t.Error("shell missing #app mount node")
	}

	// Agent asset is served as JS.
	agent := get(t, srv.URL+"/__sigil/agent.js")
	if !strings.Contains(agent, "window.__sigilDev") {
		t.Error("agent.js not served")
	}
}

func TestRebuildBroadcastsReload(t *testing.T) {
	s := New("entry.sigil", ".", okBuild)
	ch, cancel := s.Hub().Subscribe()
	defer cancel()
	s.Rebuild()
	got := <-ch
	if !strings.Contains(got, `"type":"reload"`) || !strings.Contains(got, "__built") {
		t.Errorf("rebuild did not broadcast a reload: %s", got)
	}
}

func TestRebuildBroadcastsErrorOnBadBuild(t *testing.T) {
	bad := func(entry, root string) (string, error) { return "", io.EOF }
	s := New("entry.sigil", ".", bad)
	ch, cancel := s.Hub().Subscribe()
	defer cancel()
	s.Rebuild()
	got := <-ch
	if !strings.Contains(got, `"type":"error"`) {
		t.Errorf("bad build did not broadcast an error: %s", got)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/devserver/ -run 'TestShell|TestRebuild' -v`
Expected: FAIL — undefined `New`.

- [ ] **Step 3: Implement `internal/devserver/server.go`**

```go
package devserver

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"
)

// BuildFunc compiles the dev bundle for an entry under root.
type BuildFunc func(entry, root string) (string, error)

// Server is the sigil dev server: a shell page, the client agent, an SSE hub,
// and a file watcher that rebuilds and broadcasts on change.
type Server struct {
	entry string
	root  string
	build BuildFunc
	hub   *Hub
}

func New(entry, root string, build BuildFunc) *Server {
	return &Server{entry: entry, root: root, build: build, hub: NewHub()}
}

func (s *Server) Hub() *Hub { return s.hub }

// shellTmpl hosts the initial bundle. The agent loads first (sets up
// __sigilDev), then the bundle runs in global scope and registers its cells.
var shellTmpl = template.Must(template.New("shell").Parse(
	`<!doctype html>
<html>
  <head><meta charset="utf-8"><title>{{.Title}} (dev)</title></head>
  <body>
    <div id="app"></div>
    <script src="/__sigil/agent.js"></script>
    <script>{{.Bundle}}</script>
  </body>
</html>`))

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/__sigil/agent.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, AgentJS)
	})
	mux.Handle("/__sigil/events", s.hub)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		js, err := s.build(s.entry, s.root)
		if err != nil {
			// Serve the shell with an empty bundle; the agent shows the overlay
			// once the SSE error arrives. Still emit a readable note inline.
			js = "/* build error: " + template.JSEscapeString(err.Error()) + " */"
		}
		w.Header().Set("Content-Type", "text/html")
		_ = shellTmpl.Execute(w, struct{ Title, Bundle template.JS }{
			Title:  template.JS(s.entry),
			Bundle: template.JS(js),
		})
	})
	return mux
}

// Rebuild compiles the dev bundle and broadcasts the result to all browsers.
func (s *Server) Rebuild() {
	js, err := s.build(s.entry, s.root)
	if err != nil {
		s.hub.Broadcast(ErrorMsg(err.Error()))
		return
	}
	s.hub.Broadcast(ReloadMsg(js))
}

// ListenAndServe starts the watcher and serves until the process exits.
func (s *Server) ListenAndServe(addr string) error {
	stop := Watch(s.root, 150*time.Millisecond, s.Rebuild)
	defer stop()
	log.Printf("dev-serving %s on http://localhost%s", s.entry, addr)
	return http.ListenAndServe(addr, s.Handler())
}
```

Note: in the shell, `template.JS` is used for the bundle so `html/template` does not HTML-escape the JS. The shell is dev-only and the input is our own compiler output.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/devserver/ -run 'TestShell|TestRebuild' -v`
Expected: PASS.

- [ ] **Step 5: Run the full devserver package**

Run: `go test ./internal/devserver/`
Expected: PASS (all of Tasks 4–7).

- [ ] **Step 6: Commit**

```bash
git add internal/devserver/server.go internal/devserver/server_test.go
git commit -m "feat(devserver): dev Server — shell, agent, SSE, rebuild-on-change"
```

---

### Task 8: CLI wiring — `sigil dev` + `serve` becomes build-once

**Files:**
- Create: `internal/cli/dev.go`
- Modify: `internal/cli/serve.go` (build once, serve static)
- Modify: `internal/cli/compile.go` (add `bundleDev`)
- Modify: `internal/cli/root.go` (register `dev`)
- Test: `internal/cli/dev_test.go` (create), `internal/cli/serve_test.go` (extend)

**Interfaces:**
- Consumes: `load.Program.BundleDev` (Task 3), `devserver.New`/`Server.ListenAndServe` (Task 7).
- Produces: `func newDevCmd() *cobra.Command`; `func bundleDev(entry, root string) (string, error)`.

- [ ] **Step 1: Write failing tests**

Create `internal/cli/dev_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDevFailsFastOnBadEntry(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.sigil")
	if err := os.WriteFile(bad, []byte("pub let x = (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A broken entry fails the up-front dev build before binding a port.
	_, _, err := run("dev", "--root", dir, "--port", "0", bad)
	if err == nil {
		t.Fatal("expected dev to fail fast on a broken entry")
	}
}

func TestBundleDevIsInstrumented(t *testing.T) {
	js, err := bundleDev(counterEntry(), repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(js, "__sigilDev.counter++") {
		t.Error("dev bundle missing instrumented __cell")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (func() bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}()) }
```

Extend `internal/cli/serve_test.go` with a static-serve assertion:

```go
func TestServeBuildsOnceAndServesStatic(t *testing.T) {
	// A good entry builds at startup; we exercise the bundle path directly
	// (ListenAndServe blocks, so we assert the production bundle is produced
	// and carries no dev instrumentation).
	js, err := bundle(counterEntry(), repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if want := "count: "; !filepathContains(js, "count") {
		_ = want
		t.Error("counter bundle did not compile")
	}
	if filepathContains(js, "__sigilDev") {
		t.Error("production serve bundle must not be instrumented")
	}
}

func filepathContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

(`counterEntry()` and `repoRoot` already exist in `cli_test.go`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestDev|TestBundleDev|TestServeBuildsOnce' -v`
Expected: FAIL — `bundleDev`/`newDevCmd` undefined.

- [ ] **Step 3: Add `bundleDev` to `internal/cli/compile.go`**

Append:

```go
// bundleDev type-checks and links the entry like bundle, but emits the dev
// (HMR-instrumented) prelude.
func bundleDev(entry, root string) (string, error) {
	prog, err := load.Load(entry, load.Options{Root: root})
	if err != nil {
		return "", err
	}
	return prog.BundleDev()
}
```

- [ ] **Step 4: Create `internal/cli/dev.go`**

```go
package cli

import (
	"github.com/incantery/sigil/internal/devserver"
	"github.com/spf13/cobra"
)

func newDevCmd() *cobra.Command {
	var (
		root string
		port string
	)
	cmd := &cobra.Command{
		Use:   "dev ENTRY.sigil",
		Short: "Serve a sigil module with hot module replacement (state-preserving)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry := args[0]
			// Fail fast on a broken entry before binding a port.
			if _, err := bundleDev(entry, root); err != nil {
				return err
			}
			srv := devserver.New(entry, root, bundleDev)
			return srv.ListenAndServe(":" + port)
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "module root directory (where std/ lives)")
	cmd.Flags().StringVar(&port, "port", "8099", "port to serve on")
	return cmd
}
```

- [ ] **Step 5: Rewrite `serve` to build once and serve static**

Replace the `RunE` body in `internal/cli/serve.go` so the bundle is built a single time at startup and served as fixed bytes:

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			entry := args[0]
			// Production: build once up front. A type/parse error aborts before
			// binding a port; there is no per-request rebuild.
			js, err := bundle(entry, root)
			if err != nil {
				return err
			}
			page := htmlPage(entry, js)
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, page)
			})
			addr := ":" + port
			log.Printf("serving %s on http://localhost%s", entry, addr)
			return http.ListenAndServe(addr, mux)
		},
```

(Update the command `Short` to `"Serve a sigil module as a static production page"`.)

- [ ] **Step 6: Register `dev` in `internal/cli/root.go`**

Add after `root.AddCommand(newServeCmd())`:

```go
	root.AddCommand(newDevCmd())
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/cli/`
Expected: PASS (including the existing `TestServeFailsFastOnBadEntry`).

- [ ] **Step 8: Build the binary and smoke-check the command exists**

Run: `go run ./cmd/sigil dev --help`
Expected: usage text for `dev` with `--root`/`--port`.

- [ ] **Step 9: Commit**

```bash
git add internal/cli/dev.go internal/cli/serve.go internal/cli/compile.go internal/cli/root.go internal/cli/dev_test.go internal/cli/serve_test.go
git commit -m "feat(cli): sigil dev (HMR) + serve becomes build-once production"
```

---

### Task 9: Browser e2e — HMR preserves state across an edit

**Files:**
- Test: `internal/devserver/hmr_browser_test.go` (create)

**Interfaces:**
- Consumes: `New`/`Server.Handler` (Task 7); a `BuildFunc` backed by `load` so the test stays in-package (avoid importing `cli`). The test copies `std/` + a counter entry into a temp root so it can mutate the entry on disk.

- [ ] **Step 1: Write the e2e test**

Create `internal/devserver/hmr_browser_test.go`:

```go
package devserver

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/incantery/sigil/internal/load"
)

const counterSrc = `import "std/reactive" (cell)
import "std/ui" (card, column, row, button, label)
import "std/html" (mount)

pub let app =
  let (count, setCount) = cell 0
  let view =
    card [ column [
      label (fun () -> "%LABEL% ${count ()}"),
      row [
        button "-" (fun () -> setCount (count () - 1)),
        button "+" (fun () -> setCount (count () + 1))
      ]
    ] ]
  mount view "#app"
`

// devBuild compiles entry under root with the dev prelude.
func devBuild(entry, root string) (string, error) {
	prog, err := load.Load(entry, load.Options{Root: root})
	if err != nil {
		return "", err
	}
	return prog.BundleDev()
}

func TestHMRPreservesCounterState(t *testing.T) {
	// Build an isolated root: copy std/ and write a mutable entry.
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "std"), filepath.Join(root, "std"))
	entry := filepath.Join(root, "app.sigil")
	writeFile(t, entry, strings.Replace(counterSrc, "%LABEL%", "count:", 1))

	s := New(entry, root, devBuild)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Headless)...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
	defer cancelTimeout()

	var afterInc, afterHMR string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible(`//button[text()="+"]`, chromedp.BySearch),
		chromedp.Click(`//button[text()="+"]`, chromedp.BySearch),
		chromedp.Click(`//button[text()="+"]`, chromedp.BySearch),
		chromedp.Text("#app", &afterInc, chromedp.ByID), // "count: 2"
		// Edit the entry's label text on disk, then trigger a rebuild+broadcast.
		chromedp.ActionFunc(func(context.Context) error {
			writeFile(t, entry, strings.Replace(counterSrc, "%LABEL%", "value:", 1))
			s.Rebuild()
			return nil
		}),
		// Wait for the swapped markup; assert the count survived the swap.
		chromedp.WaitFunc(func(ctx context.Context) (bool, error) {
			var txt string
			if err := chromedp.Text("#app", &txt, chromedp.ByID).Do(ctx); err != nil {
				return false, nil
			}
			return strings.Contains(txt, "value:"), nil
		}),
		chromedp.Text("#app", &afterHMR, chromedp.ByID),
	)
	if err != nil {
		if strings.Contains(err.Error(), "exec") {
			t.Skipf("chrome unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if !strings.Contains(afterInc, "count: 2") {
		t.Errorf("before HMR: %q does not contain %q", afterInc, "count: 2")
	}
	// The label text was swapped AND the reactive count (2) was preserved.
	if !strings.Contains(afterHMR, "value: 2") {
		t.Errorf("after HMR: %q does not contain %q (state not preserved)", afterHMR, "value: 2")
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

Note on `chromedp.WaitFunc`: if the installed chromedp version lacks it, poll instead with a short loop of `chromedp.Text` inside a `chromedp.ActionFunc` until the text contains `value:` or a deadline passes. Keep the same assertions.

- [ ] **Step 2: Run the e2e**

Run: `go test ./internal/devserver/ -run TestHMRPreservesCounterState -v`
Expected: PASS (or SKIP if Chrome is absent). A PASS proves the new code mounted **and** the counter value `2` survived the swap.

- [ ] **Step 3: Commit**

```bash
git add internal/devserver/hmr_browser_test.go
git commit -m "test(devserver): e2e — HMR swaps code and preserves cell state"
```

---

### Task 10: Docs — dev README + CLAUDE.md update

**Files:**
- Create: `docs/dev-server.md`
- Modify: `CLAUDE.md` (build/run section + "What's next")

**Interfaces:** none (documentation).

- [ ] **Step 1: Write `docs/dev-server.md`**

```markdown
# Dev server: `sigil dev` vs `sigil serve`

- **`sigil serve ENTRY.sigil`** — production. Builds the bundle **once** at
  startup (fails fast on a type/parse error) and serves it as static bytes. No
  per-request rebuild.
- **`sigil dev ENTRY.sigil`** — development. Watches every `.sigil` file under
  `--root` (mtime poll, ~150ms) and performs **state-preserving, in-place hot
  module replacement**: on a change it rebuilds and pushes the new bundle over
  Server-Sent Events; the in-page client agent snapshots reactive cell values,
  disposes global listeners, empties `#app`, evals the new bundle, and rehydrates
  each cell. No page reload — scroll, focus, and console survive.

Both default to port `8099` and take `--root` / `--port`.

## How state is preserved (and when it resets)

Every cell funnels through `std/reactive`'s `cell`/`computed`, so the emitted
program has a single `__cell` site. Cells are therefore matched across a reload
by **creation order**: the Nth cell created adopts the Nth saved value.

- **Survives:** editing markup, styles, event handlers, and any code that does
  not change how many cells are created or their order — the common case.
- **Resets:** adding, removing, or reordering `cell` declarations (this shifts
  the indices of cells created afterward), and per-row local state created inside
  an `each` render thunk (v1 limitation; the seam toward preserving it is keyed
  `each`).

## Build errors

A failed rebuild shows a dismissable overlay over the still-running app (which
keeps its state). The next successful build clears the overlay and hot-swaps.
```

- [ ] **Step 2: Update `CLAUDE.md`**

In the "Build / test / run" section, add under the existing commands:

```sh
go run ./cmd/sigil dev examples/counter/counter.sigil       # HMR dev server on :8099
```

And add a line noting the split: `serve` is now build-once/production; `dev` is the watch+HMR loop (see `docs/dev-server.md`). In the CLI description, update the subcommand list from `version, check, build, serve` to `version, check, build, serve, dev`.

In "What's next", mark the dev-server item done and note the v1 state-preservation caveat (list-item-local state resets; keyed-`each` is the follow-up).

- [ ] **Step 3: Verify the whole repo is green**

Run: `go build ./... && go test ./...`
Expected: PASS (browser tests may SKIP without Chrome).

- [ ] **Step 4: Commit**

```bash
git add docs/dev-server.md CLAUDE.md
git commit -m "docs: sigil dev HMR server + serve/dev split"
```

---

## Self-Review

**Spec coverage:**
- §1 command split → Task 8 (serve build-once; dev command). ✓
- §2 watcher / SSE / shell+agent → Tasks 4 (agent), 5 (watcher), 6 (SSE), 7 (shell+rebuild). ✓
- §3 dev build mode (Dev flag swaps prelude; call-order keying; production unchanged) → Tasks 1 (emit), 3 (load), with golden guard in Task 1. ✓
- §4 state preservation (registry, snapshot/dispose/rehydrate sequence, computed self-heal) → dev prelude (Task 1) + agent lifecycle (Task 4), verified by Tasks 2 (unit) and 9 (e2e). ✓
- §5 disposal (onPopState disposer, keyed installStyles, fetch generation guard) → dev prelude swaps in Task 1; agent runs disposers + bumps generation in Task 4. ✓
- §6 wire protocol + error overlay → Task 6 (ReloadMsg/ErrorMsg) + Task 4 (overlay) + Task 7 (broadcast). ✓
- §Testing (watcher, SSE framing, Dev golden, goja hydration, chromedp e2e) → Tasks 5, 6, 1, 2, 9. ✓
- §Docs → Task 10. ✓

**Placeholder scan:** No TBD/TODO. Every code step shows complete code; every run step shows the command and expected result. The one conditional ("if chromedp lacks `WaitFunc`, poll instead") gives the concrete fallback.

**Type consistency:** `BundleDev` (emit + load), `bundleDev` (cli), `Snapshot`/`Changed`/`Watch`, `Hub`/`NewHub`/`Subscribe`/`Broadcast`/`ServeHTTP`, `ReloadMsg`/`ErrorMsg`, `Server`/`New`/`Handler`/`Rebuild`/`Hub()`/`ListenAndServe`, `BuildFunc`, `AgentJS`, and the `__sigilDev` field set (`counter`, `hydration`, `cells`, `disposers`, `generation`) are used identically across the dev prelude (Task 1), the agent (Task 4), and the goja test (Task 2). ✓
```
