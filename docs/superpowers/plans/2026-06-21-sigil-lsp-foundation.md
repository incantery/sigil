# `sigil lsp` Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `sigil lsp` language server giving live diagnostics + document symbols for `.sigil` files, built over existing `internal/load`/`internal/types` with no new compiler machinery.

**Architecture:** A new `internal/lsp` package hand-rolls JSON-RPC over stdio (no LSP library), keeps an in-memory document store, and on every edit runs `load.Load` against an in-memory **overlay** (the one compiler-side change) to produce one diagnostic per file. Document symbols are a flat AST walk. A thin `sigil lsp` cobra command wires stdin/stdout to the server.

**Tech Stack:** Go stdlib only (`bufio`, `encoding/json`, `net/url`, `io`, `sync`, `errors`), cobra for the CLI. No new dependencies.

## Global Constraints

- No new Go module dependencies — hand-rolled JSON-RPC, stdlib only.
- The ONLY change outside `internal/lsp` + `internal/cli/lsp.go` is `load.Options.Overlay` + an overlay-aware read at `internal/load/load.go:116`. `internal/parse` and `internal/types` are NOT touched.
- Errors returned by `load.Load` are wrapped with `fmt.Errorf("%s: %w", file, err)` — recover the underlying `*lex.Error` / `*parse.Error` / `*types.Error` via `errors.As`. All three are `struct{ Line, Col int; Msg string }`, 1-based.
- `load.Load` stops at the first error → **one diagnostic per file** (accepted v1 limitation). Positionless errors (import resolution: missing module, cycle — `fmt.Errorf`, no `*Error`) map to a `(0,0)` diagnostic.
- LSP positions are **0-based**; compiler positions are **1-based**. Convert: `lspLine = Line-1`, `lspChar = Col-1`.
- Capabilities advertised: `textDocumentSync: 1` (Full) and `documentSymbolProvider: true` — nothing we don't implement.
- Document symbols are **flat** (top-level `LetDecl`/`TypeDecl` only); `Variant`/`FieldType` have no `Pos`, so no children.
- Follow existing repo idioms: cobra `newXxxCmd` constructors, the `run(args...)` CLI test helper, focused tests.

## File structure

- `internal/load/load.go` — add `Options.Overlay map[string]string` + overlay-aware read (Task 1).
- `internal/lsp/jsonrpc.go` — Content-Length framing, JSON-RPC envelopes, `Conn` read/write (Task 2).
- `internal/lsp/protocol.go` — the LSP message structs we use; grows per task that needs new ones (Tasks 3, 5, 6).
- `internal/lsp/server.go` — `Server`, run loop, method dispatch, lifecycle, URI helpers (Task 3; handlers added in 4–6).
- `internal/lsp/docs.go` — open-document store (Task 4).
- `internal/lsp/diagnostics.go` — analysis + error→Diagnostic mapping (Task 5).
- `internal/lsp/symbols.go` — AST → `[]DocumentSymbol` (Task 6).
- `internal/cli/lsp.go` — the `sigil lsp` command (Task 7).
- `internal/cli/root.go` — register `lsp` (Task 7).
- `editor/` note + `CLAUDE.md` (Task 8).

---

### Task 1: `load.Options.Overlay` — check buffers before disk

**Files:**
- Modify: `internal/load/load.go` (the `Options` struct near line 32; the read at line 116)
- Test: `internal/load/overlay_test.go` (create)

**Interfaces:**
- Consumes: existing `load.Load`, `load.Options`.
- Produces: `Options.Overlay map[string]string` — absolute-path → file content. When a file being loaded is present in the overlay, its content is used instead of `os.ReadFile`. Keys are compared against the exact `file` path `load` reads (callers pass `filepath.Clean`/absolute paths).

- [ ] **Step 1: Write the failing test**

Create `internal/load/overlay_test.go`:

```go
package load

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An overlay entry shadows the on-disk content for that path.
func TestOverlayShadowsDisk(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	// On disk: a valid module.
	if err := os.WriteFile(entry, []byte("pub let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Overlay: a BROKEN version. Load must see the overlay and fail.
	_, err := Load(entry, Options{
		Root:    dir,
		Overlay: map[string]string{entry: "pub let x = (\n"},
	})
	if err == nil {
		t.Fatal("expected overlay (broken) to fail the load, but it succeeded")
	}
	if !strings.Contains(err.Error(), entry) {
		t.Errorf("error %q should name the entry file", err)
	}
}

// With no overlay entry for a path, the on-disk content is used.
func TestOverlayFallsBackToDisk(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte("pub let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Overlay names a DIFFERENT file, so the entry reads from disk (valid).
	if _, err := Load(entry, Options{
		Root:    dir,
		Overlay: map[string]string{filepath.Join(dir, "other.sigil"): "garbage"},
	}); err != nil {
		t.Fatalf("expected disk fallback to succeed, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/load/ -run TestOverlay -v`
Expected: FAIL — `Overlay` is not a field of `Options`.

- [ ] **Step 3: Implement**

In `internal/load/load.go`, add the field to `Options`:

```go
type Options struct {
	// Root is the filesystem directory import paths resolve against.
	Root string
	// Prefix, if set, is stripped from each import path before resolving (the
	// module's own import-path prefix, so in-repo imports map to local files).
	Prefix string
	// Overlay maps an absolute file path to in-memory content. When load reads a
	// file present here, it uses this content instead of the on-disk bytes. Used
	// by the LSP to type-check unsaved editor buffers. nil = always read disk.
	Overlay map[string]string
}
```

Then replace the read at `load.go:116`:

```go
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
```

with an overlay-aware read:

```go
	var src []byte
	if text, ok := l.opts.Overlay[file]; ok {
		src = []byte(text)
	} else {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		src = b
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/load/ -run TestOverlay -v`
Expected: PASS.

- [ ] **Step 5: Run the full load suite (overlay must not regress existing loads)**

Run: `go test ./internal/load/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/load/load.go internal/load/overlay_test.go
git commit -m "feat(load): Options.Overlay — check in-memory buffers before disk"
```

---

### Task 2: JSON-RPC transport (`jsonrpc.go`)

**Files:**
- Create: `internal/lsp/jsonrpc.go`
- Test: `internal/lsp/jsonrpc_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Message struct { JSONRPC string; ID json.RawMessage; Method string; Params json.RawMessage }`
  - `func (m *Message) IsNotification() bool` — true when `ID` is empty.
  - `type Conn struct { ... }`; `func NewConn(r io.Reader, w io.Writer) *Conn`.
  - `func (c *Conn) Read() (*Message, error)` — reads one `Content-Length`-framed message; returns `io.EOF` at stream end.
  - `func (c *Conn) Reply(id json.RawMessage, result any) error`
  - `func (c *Conn) ReplyError(id json.RawMessage, code int, message string) error`
  - `func (c *Conn) Notify(method string, params any) error`
  - `const CodeMethodNotFound = -32601`

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/jsonrpc_test.go`:

```go
package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// frame wraps a JSON body in the LSP base-protocol framing.
func frame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func TestConnReadFramedMessage(t *testing.T) {
	in := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"x":1}}`)
	c := NewConn(strings.NewReader(in), &bytes.Buffer{})
	msg, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Method != "initialize" {
		t.Errorf("method = %q, want initialize", msg.Method)
	}
	if msg.IsNotification() {
		t.Error("message with an id must not be a notification")
	}
	if string(msg.ID) != "1" {
		t.Errorf("id = %s, want 1", msg.ID)
	}
}

func TestConnReadNotification(t *testing.T) {
	in := frame(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	c := NewConn(strings.NewReader(in), &bytes.Buffer{})
	msg, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !msg.IsNotification() {
		t.Error("message without an id must be a notification")
	}
}

func TestConnReplyFramesResult(t *testing.T) {
	var out bytes.Buffer
	c := NewConn(strings.NewReader(""), &out)
	if err := c.Reply(json.RawMessage("7"), map[string]string{"hello": "world"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "Content-Length: ") {
		t.Errorf("output missing Content-Length framing: %q", got)
	}
	// Body is after the blank line; parse it back.
	body := got[strings.Index(got, "\r\n\r\n")+4:]
	var env struct {
		JSONRPC string            `json:"jsonrpc"`
		ID      json.RawMessage   `json:"id"`
		Result  map[string]string `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("reply body not valid JSON: %v\n%s", err, body)
	}
	if env.JSONRPC != "2.0" || string(env.ID) != "7" || env.Result["hello"] != "world" {
		t.Errorf("unexpected reply envelope: %+v", env)
	}
}

func TestConnReadTwoMessages(t *testing.T) {
	in := frame(`{"jsonrpc":"2.0","id":1,"method":"a"}`) + frame(`{"jsonrpc":"2.0","id":2,"method":"b"}`)
	c := NewConn(strings.NewReader(in), &bytes.Buffer{})
	m1, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	m2, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	if m1.Method != "a" || m2.Method != "b" {
		t.Errorf("methods = %q,%q want a,b", m1.Method, m2.Method)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lsp/ -run TestConn -v`
Expected: FAIL — package/`NewConn` undefined.

- [ ] **Step 3: Implement `internal/lsp/jsonrpc.go`**

```go
// Package lsp implements a minimal Language Server Protocol server for sigil:
// live diagnostics and document symbols over internal/load + internal/types.
// The base protocol (JSON-RPC 2.0 over Content-Length-framed stdio) is
// hand-rolled here — no external LSP dependency.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

const CodeMethodNotFound = -32601

// Message is a decoded JSON-RPC envelope. ID is raw so a request (number or
// string id) is distinguishable from a notification (absent id).
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the message has no id (so no reply is owed).
func (m *Message) IsNotification() bool { return len(m.ID) == 0 }

// Conn reads and writes Content-Length-framed JSON-RPC messages.
type Conn struct {
	r   *bufio.Reader
	w   io.Writer
	wmu sync.Mutex // serialize writes (diagnostics + responses share one stream)
}

func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{r: bufio.NewReader(r), w: w}
}

// Read returns the next framed message, or io.EOF at end of stream.
func (c *Conn) Read() (*Message, error) {
	length := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" { // blank line: headers done
			break
		}
		if name, val, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return nil, fmt.Errorf("bad Content-Length: %q", val)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("message missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, err
	}
	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("invalid JSON-RPC body: %w", err)
	}
	return &m, nil
}

func (c *Conn) write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

// Reply sends a successful response for the given request id.
func (c *Conn) Reply(id json.RawMessage, result any) error {
	return c.write(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{"2.0", id, result})
}

// ReplyError sends an error response for the given request id.
func (c *Conn) ReplyError(id json.RawMessage, code int, message string) error {
	return c.write(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   any             `json:"error"`
	}{"2.0", id, map[string]any{"code": code, "message": message}})
}

// Notify sends a notification (no id, no reply expected).
func (c *Conn) Notify(method string, params any) error {
	return c.write(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", method, params})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/lsp/ -run TestConn -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lsp/jsonrpc.go internal/lsp/jsonrpc_test.go
git commit -m "feat(lsp): hand-rolled JSON-RPC over Content-Length-framed stdio"
```

---

### Task 3: Server skeleton + lifecycle (`server.go`, `protocol.go`)

**Files:**
- Create: `internal/lsp/server.go`, `internal/lsp/protocol.go`
- Test: `internal/lsp/lifecycle_test.go` (create)

**Interfaces:**
- Consumes: `Conn` (Task 2).
- Produces:
  - `type Server struct { ... }`; `func NewServer(r io.Reader, w io.Writer) *Server`.
  - `func (s *Server) Run() error` — read/dispatch loop; returns nil on `exit`.
  - `func uriToPath(uri string) string` — `file:///a%20b.sigil` → `/a b.sigil`.
  - In `protocol.go`: `InitializeParams`, `InitializeResult`, `ServerCapabilities`, `TextDocumentSyncFull = 1`.
  - Dispatch: `initialize` (sets `s.root`, replies capabilities), `initialized` (noop), `shutdown` (reply null, set flag), `exit` (stop loop). Unknown request → `ReplyError(CodeMethodNotFound)`; unknown notification → ignored.

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/lifecycle_test.go`. It defines two shared test helpers used by later tasks too — `safeBuffer` (a concurrency-safe `io.Writer` capturing server output) and `waitFor` (polls that output for a substring). The tests feed framed client messages into a pipe and assert on what the server writes back.

```go
package lsp

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeBuffer is a concurrency-safe io.Writer + string reader for capturing the
// server's framed output while the server goroutine writes to it.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitFor polls the captured output until it contains want, or fails.
func waitFor(t *testing.T, b *safeBuffer, want string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if strings.Contains(b.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in server output:\n%s", want, b.String())
}

// startServer wires a client->server pipe and a captured server->client buffer,
// runs the server, and returns the client write end + the captured output.
func startServer(t *testing.T) (clientWrite io.Writer, out *safeBuffer) {
	t.Helper()
	cr, cw := io.Pipe()
	out = &safeBuffer{}
	srv := NewServer(cr, out)
	go srv.Run()
	return cw, out
}

func TestInitializeReplyAndCapabilities(t *testing.T) {
	cw, out := startServer(t)
	io.WriteString(cw, frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///tmp/proj"}}`))
	// The initialize reply carries id 1 and advertises documentSymbolProvider.
	waitFor(t, out, `"id":1`)
	waitFor(t, out, "documentSymbolProvider")
	io.WriteString(cw, frame(`{"jsonrpc":"2.0","method":"exit"}`))
}

func TestUnknownRequestGetsMethodNotFound(t *testing.T) {
	cw, out := startServer(t)
	io.WriteString(cw, frame(`{"jsonrpc":"2.0","id":9,"method":"textDocument/hover","params":{}}`))
	waitFor(t, out, "-32601") // MethodNotFound for an unimplemented request
	io.WriteString(cw, frame(`{"jsonrpc":"2.0","method":"exit"}`))
}
```

(`frame` is defined in `jsonrpc_test.go` — same package.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/lsp/ -run 'TestInitialize|TestUnknownRequest' -v`
Expected: FAIL — `NewServer` undefined.

- [ ] **Step 3: Implement `internal/lsp/protocol.go`**

```go
package lsp

// Subset of the LSP types this server uses. Field names/JSON tags follow the
// LSP spec so editors interoperate.

const TextDocumentSyncFull = 1

type InitializeParams struct {
	RootURI          string            `json:"rootUri"`
	WorkspaceFolders []WorkspaceFolder `json:"workspaceFolders"`
}

type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

type ServerCapabilities struct {
	TextDocumentSync       int  `json:"textDocumentSync"`
	DocumentSymbolProvider bool `json:"documentSymbolProvider"`
}
```

- [ ] **Step 4: Implement `internal/lsp/server.go`**

```go
package lsp

import (
	"encoding/json"
	"io"
	"net/url"
)

// Server is a sigil LSP server speaking JSON-RPC over a Conn.
type Server struct {
	conn        *Conn
	root        string // workspace root = load Root (where std/ lives)
	gotShutdown bool
}

func NewServer(r io.Reader, w io.Writer) *Server {
	return &Server{conn: NewConn(r, w)}
}

// Run reads and dispatches messages until exit (or stream EOF).
func (s *Server) Run() error {
	for {
		msg, err := s.conn.Read()
		if err != nil {
			return nil // EOF or broken pipe: stop cleanly
		}
		if stop := s.dispatch(msg); stop {
			return nil
		}
	}
}

// dispatch handles one message; returns true to stop the run loop (on exit).
func (s *Server) dispatch(msg *Message) (stop bool) {
	switch msg.Method {
	case "initialize":
		var p InitializeParams
		_ = json.Unmarshal(msg.Params, &p)
		s.root = s.resolveRoot(p)
		_ = s.conn.Reply(msg.ID, InitializeResult{Capabilities: ServerCapabilities{
			TextDocumentSync:       TextDocumentSyncFull,
			DocumentSymbolProvider: true,
		}})
	case "initialized":
		// no-op
	case "shutdown":
		s.gotShutdown = true
		_ = s.conn.Reply(msg.ID, nil)
	case "exit":
		return true
	default:
		if !msg.IsNotification() {
			_ = s.conn.ReplyError(msg.ID, CodeMethodNotFound, "method not found: "+msg.Method)
		}
	}
	return false
}

// resolveRoot picks the load Root: rootUri, else first workspace folder.
func (s *Server) resolveRoot(p InitializeParams) string {
	if p.RootURI != "" {
		return uriToPath(p.RootURI)
	}
	if len(p.WorkspaceFolders) > 0 {
		return uriToPath(p.WorkspaceFolders[0].URI)
	}
	return "."
}

// uriToPath converts a file:// URI to a filesystem path (percent-decoded).
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	return u.Path
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/lsp/ -run 'TestInitialize|TestUnknownRequest' -v`
Expected: PASS (both lifecycle tests).

- [ ] **Step 6: Commit**

```bash
git add internal/lsp/server.go internal/lsp/protocol.go internal/lsp/lifecycle_test.go
git commit -m "feat(lsp): server skeleton + initialize/shutdown/exit lifecycle"
```

---

### Task 4: Document store + text sync (`docs.go`)

**Files:**
- Create: `internal/lsp/docs.go`
- Modify: `internal/lsp/server.go` (add the store + sync handlers + protocol structs)
- Modify: `internal/lsp/protocol.go` (text-document sync params)
- Test: `internal/lsp/docs_test.go` (create)

**Interfaces:**
- Consumes: `Server` (Task 3).
- Produces:
  - `type docStore struct { ... }` with `func newDocStore() *docStore`, `set(uri, text string)`, `get(uri string) (string, bool)`, `remove(uri string)`, `overlay() map[string]string` (path→text for all open docs).
  - `Server` gains `docs *docStore` (init in `NewServer`) and dispatch cases `textDocument/didOpen`, `textDocument/didChange` (full sync), `textDocument/didClose`.
  - Protocol: `DidOpenParams`, `DidChangeParams`, `DidCloseParams`, `TextDocumentItem`, `VersionedTextDocumentIdentifier`, `TextDocumentContentChangeEvent`.

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/docs_test.go`:

```go
package lsp

import "testing"

func TestDocStoreSetGetRemove(t *testing.T) {
	d := newDocStore()
	if _, ok := d.get("file:///a.sigil"); ok {
		t.Fatal("empty store should not have the doc")
	}
	d.set("file:///a.sigil", "pub let x = 1")
	got, ok := d.get("file:///a.sigil")
	if !ok || got != "pub let x = 1" {
		t.Errorf("get = %q,%v want the text", got, ok)
	}
	d.remove("file:///a.sigil")
	if _, ok := d.get("file:///a.sigil"); ok {
		t.Error("removed doc should be gone")
	}
}

func TestDocStoreOverlayKeysByPath(t *testing.T) {
	d := newDocStore()
	d.set("file:///proj/app.sigil", "text-a")
	ov := d.overlay()
	if ov["/proj/app.sigil"] != "text-a" {
		t.Errorf("overlay should key by filesystem path; got %v", ov)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lsp/ -run TestDocStore -v`
Expected: FAIL — `newDocStore` undefined.

- [ ] **Step 3: Implement `internal/lsp/docs.go`**

```go
package lsp

import "sync"

// docStore holds the text of open documents, keyed by LSP URI.
type docStore struct {
	mu   sync.RWMutex
	docs map[string]string // uri -> text
}

func newDocStore() *docStore { return &docStore{docs: map[string]string{}} }

func (d *docStore) set(uri, text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.docs[uri] = text
}

func (d *docStore) get(uri string) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	t, ok := d.docs[uri]
	return t, ok
}

func (d *docStore) remove(uri string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.docs, uri)
}

// overlay returns a load.Options.Overlay: filesystem path -> text for every
// open doc, so the type checker sees unsaved buffers.
func (d *docStore) overlay() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	ov := make(map[string]string, len(d.docs))
	for uri, text := range d.docs {
		ov[uriToPath(uri)] = text
	}
	return ov
}
```

- [ ] **Step 4: Add protocol structs to `internal/lsp/protocol.go`**

```go
type TextDocumentItem struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
	Text    string `json:"text"`
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type DidOpenParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type DidCloseParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}
```

- [ ] **Step 5: Wire sync handlers into `server.go`**

In `NewServer`, initialize the store:

```go
func NewServer(r io.Reader, w io.Writer) *Server {
	return &Server{conn: NewConn(r, w), docs: newDocStore()}
}
```

Add the field to `Server`: `docs *docStore`.

Add cases to `dispatch` (before `default`):

```go
	case "textDocument/didOpen":
		var p DidOpenParams
		_ = json.Unmarshal(msg.Params, &p)
		s.docs.set(p.TextDocument.URI, p.TextDocument.Text)
	case "textDocument/didChange":
		var p DidChangeParams
		_ = json.Unmarshal(msg.Params, &p)
		if n := len(p.ContentChanges); n > 0 {
			s.docs.set(p.TextDocument.URI, p.ContentChanges[n-1].Text) // full sync: last wins
		}
	case "textDocument/didClose":
		var p DidCloseParams
		_ = json.Unmarshal(msg.Params, &p)
		s.docs.remove(p.TextDocument.URI)
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/lsp/`
Expected: PASS (docs + lifecycle).

- [ ] **Step 7: Commit**

```bash
git add internal/lsp/docs.go internal/lsp/docs_test.go internal/lsp/protocol.go internal/lsp/server.go
git commit -m "feat(lsp): document store + didOpen/didChange/didClose full-text sync"
```

---

### Task 5: Diagnostics (`diagnostics.go`)

**Files:**
- Create: `internal/lsp/diagnostics.go`
- Modify: `internal/lsp/server.go` (publish on open/change/save), `internal/lsp/protocol.go` (Diagnostic structs + DidSaveParams)
- Test: `internal/lsp/diagnostics_test.go` (create)

**Interfaces:**
- Consumes: `load.Load` + `Options.Overlay` (Task 1), `docStore.overlay` (Task 4), `*lex.Error`/`*parse.Error`/`*types.Error`.
- Produces:
  - `func diagnosticsFor(err error, text string) []Diagnostic` — maps a `load.Load` error to a one-element diagnostics slice; `nil` error → empty slice.
  - `func (s *Server) publishDiagnostics(uri string)` — runs analysis for `uri` and notifies `textDocument/publishDiagnostics`.
  - `Server` dispatch: call `publishDiagnostics` after didOpen/didChange, and add `textDocument/didSave`.
  - Protocol: `Position{Line,Character}`, `Range{Start,End}`, `Diagnostic{Range,Severity,Source,Message}`, `PublishDiagnosticsParams{URI,Diagnostics}`, `SeverityError = 1`.

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/diagnostics_test.go`:

```go
package lsp

import (
	"fmt"
	"testing"

	"github.com/incantery/sigil/internal/parse"
	"github.com/incantery/sigil/internal/types"
)

func TestDiagnosticsForParseError(t *testing.T) {
	err := fmt.Errorf("/x.sigil: %w", &parse.Error{Line: 3, Col: 5, Msg: "expected expression"})
	ds := diagnosticsFor(err, "line1\nline2\nabcdefgh\n")
	if len(ds) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(ds))
	}
	d := ds[0]
	if d.Range.Start.Line != 2 || d.Range.Start.Character != 4 {
		t.Errorf("start = %d:%d, want 2:4 (0-based)", d.Range.Start.Line, d.Range.Start.Character)
	}
	if d.Range.End.Character != 8 { // end of "abcdefgh"
		t.Errorf("end char = %d, want 8 (end of line 3)", d.Range.End.Character)
	}
	if d.Severity != SeverityError || d.Message != "expected expression" {
		t.Errorf("unexpected severity/message: %d %q", d.Severity, d.Message)
	}
}

func TestDiagnosticsForTypeError(t *testing.T) {
	err := fmt.Errorf("/x.sigil: %w", &types.Error{Line: 1, Col: 1, Msg: "type mismatch: Int vs String"})
	ds := diagnosticsFor(err, "x\n")
	if len(ds) != 1 || ds[0].Range.Start.Line != 0 || ds[0].Message != "type mismatch: Int vs String" {
		t.Fatalf("unexpected type diagnostic: %+v", ds)
	}
}

func TestDiagnosticsForPositionlessError(t *testing.T) {
	err := fmt.Errorf("cannot resolve import \"std/nope\"")
	ds := diagnosticsFor(err, "import \"std/nope\"\n")
	if len(ds) != 1 || ds[0].Range.Start.Line != 0 || ds[0].Range.Start.Character != 0 {
		t.Fatalf("positionless error should map to (0,0): %+v", ds)
	}
}

func TestDiagnosticsForNilError(t *testing.T) {
	if ds := diagnosticsFor(nil, "anything"); len(ds) != 0 {
		t.Errorf("nil error should give no diagnostics, got %d", len(ds))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lsp/ -run TestDiagnostics -v`
Expected: FAIL — `diagnosticsFor` undefined.

- [ ] **Step 3: Add protocol structs to `protocol.go`**

```go
const SeverityError = 1

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type DidSaveParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}
```

- [ ] **Step 4: Implement `internal/lsp/diagnostics.go`**

```go
package lsp

import (
	"errors"
	"strings"

	"github.com/incantery/sigil/internal/lex"
	"github.com/incantery/sigil/internal/load"
	"github.com/incantery/sigil/internal/parse"
	"github.com/incantery/sigil/internal/types"
)

// diagnosticsFor maps a load.Load error to LSP diagnostics. A nil error yields
// an empty slice (which clears stale squiggles when published).
func diagnosticsFor(err error, text string) []Diagnostic {
	if err == nil {
		return []Diagnostic{}
	}
	line, col, msg := 1, 1, err.Error()
	positioned := false

	var le *lex.Error
	var pe *parse.Error
	var te *types.Error
	switch {
	case errors.As(err, &le):
		line, col, msg, positioned = le.Line, le.Col, le.Msg, true
	case errors.As(err, &pe):
		line, col, msg, positioned = pe.Line, pe.Col, pe.Msg, true
	case errors.As(err, &te):
		line, col, msg, positioned = te.Line, te.Col, te.Msg, true
	}

	if !positioned {
		// Import-resolution / read errors carry no position: pin to file top.
		return []Diagnostic{{
			Range:    Range{Start: Position{0, 0}, End: Position{0, 0}},
			Severity: SeverityError,
			Source:   "sigil",
			Message:  msg,
		}}
	}

	startLine := line - 1
	startChar := col - 1
	endChar := startChar
	if l := lineLength(text, startLine); l > startChar {
		endChar = l // extend to end of line so the squiggle is visible
	}
	return []Diagnostic{{
		Range:    Range{Start: Position{startLine, startChar}, End: Position{startLine, endChar}},
		Severity: SeverityError,
		Source:   "sigil",
		Message:  msg,
	}}
}

// lineLength returns the character length of the 0-based line in text, or 0 if
// the line is out of range.
func lineLength(text string, line int) int {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return 0
	}
	return len(strings.TrimRight(lines[line], "\r"))
}

// analyze loads the document at path with the given root and overlay, returning
// the first compiler error (or nil).
func analyze(path, root string, overlay map[string]string) error {
	_, err := load.Load(path, load.Options{Root: root, Overlay: overlay})
	return err
}
```

- [ ] **Step 5: Wire publishing into `server.go`**

Add to `server.go`:

```go
import "path/filepath" // add to the import block

// publishDiagnostics analyzes the open document at uri and sends its diagnostics.
func (s *Server) publishDiagnostics(uri string) {
	text, ok := s.docs.get(uri)
	if !ok {
		return
	}
	path := uriToPath(uri)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	root := s.root
	if root == "" {
		root = filepath.Dir(path)
	}
	err := analyze(path, root, s.docs.overlay())
	_ = s.conn.Notify("textDocument/publishDiagnostics", PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnosticsFor(err, text),
	})
}
```

In `dispatch`, after the `didOpen` and `didChange` store updates, add `s.publishDiagnostics(p.TextDocument.URI)`. Add a `didSave` case:

```go
	case "textDocument/didSave":
		var p DidSaveParams
		_ = json.Unmarshal(msg.Params, &p)
		s.publishDiagnostics(p.TextDocument.URI)
```

(For `didClose`, also publish an empty list to clear squiggles: after `s.docs.remove(...)`, add
`_ = s.conn.Notify("textDocument/publishDiagnostics", PublishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []Diagnostic{}})`.)

Note: the overlay built by `s.docs.overlay()` keys by `uriToPath` (not `filepath.Abs`). To keep the entry path consistent with the overlay key, the overlay must use the same path form as `path` above. Adjust `docStore.overlay()` to absolutize: change its body to use `filepath.Abs` per path (import `path/filepath` in `docs.go`):

```go
	for uri, text := range d.docs {
		p := uriToPath(uri)
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		ov[p] = text
	}
```

Update the `TestDocStoreOverlayKeysByPath` expectation accordingly: on most systems `/proj/app.sigil` is already absolute, so `filepath.Abs` returns it unchanged — the test stays valid.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/lsp/`
Expected: PASS (diagnostics + docs + lifecycle).

- [ ] **Step 7: Commit**

```bash
git add internal/lsp/diagnostics.go internal/lsp/diagnostics_test.go internal/lsp/protocol.go internal/lsp/server.go internal/lsp/docs.go
git commit -m "feat(lsp): live diagnostics via load overlay, mapped to LSP ranges"
```

---

### Task 6: Document symbols (`symbols.go`)

**Files:**
- Create: `internal/lsp/symbols.go`
- Modify: `internal/lsp/server.go` (handle `textDocument/documentSymbol`), `internal/lsp/protocol.go` (symbol structs)
- Test: `internal/lsp/symbols_test.go` (create)

**Interfaces:**
- Consumes: `parse.Module`, `ast.LetDecl`/`ast.TypeDecl`, `docStore.get` (Task 4).
- Produces:
  - `func documentSymbols(text string) []DocumentSymbol` — parses text; returns flat symbols; parse failure → empty slice.
  - `Server` dispatch: `textDocument/documentSymbol` → reply `documentSymbols(text)`.
  - Protocol: `DocumentSymbol{Name,Kind,Range,SelectionRange}`, `DocumentSymbolParams{TextDocument}`, kind consts `SymbolKindFunction=12`, `SymbolKindVariable=13`, `SymbolKindEnum=10`, `SymbolKindStruct=23`.

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/symbols_test.go`:

```go
package lsp

import "testing"

func TestDocumentSymbolsKinds(t *testing.T) {
	src := `pub let answer = 42
let add a b = a + b
pub type Color = Red | Green | Blue
type Point = { x: Int, y: Int }
`
	syms := documentSymbols(src)
	byName := map[string]DocumentSymbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	if len(syms) != 4 {
		t.Fatalf("want 4 symbols, got %d: %+v", len(syms), syms)
	}
	if byName["answer"].Kind != SymbolKindVariable {
		t.Errorf("answer should be Variable, got %d", byName["answer"].Kind)
	}
	if byName["add"].Kind != SymbolKindFunction {
		t.Errorf("add should be Function, got %d", byName["add"].Kind)
	}
	if byName["Color"].Kind != SymbolKindEnum {
		t.Errorf("Color should be Enum, got %d", byName["Color"].Kind)
	}
	if byName["Point"].Kind != SymbolKindStruct {
		t.Errorf("Point should be Struct, got %d", byName["Point"].Kind)
	}
	// Range is 0-based; "answer" is on the first line.
	if byName["answer"].Range.Start.Line != 0 {
		t.Errorf("answer line = %d, want 0", byName["answer"].Range.Start.Line)
	}
}

func TestDocumentSymbolsParseErrorEmpty(t *testing.T) {
	if syms := documentSymbols("pub let x = ("); len(syms) != 0 {
		t.Errorf("unparsable source should yield no symbols, got %d", len(syms))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lsp/ -run TestDocumentSymbols -v`
Expected: FAIL — `documentSymbols` undefined.

- [ ] **Step 3: Add protocol structs to `protocol.go`**

```go
const (
	SymbolKindEnum     = 10
	SymbolKindFunction = 12
	SymbolKindVariable = 13
	SymbolKindStruct   = 23
)

type DocumentSymbol struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
}

type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}
```

- [ ] **Step 4: Implement `internal/lsp/symbols.go`**

```go
package lsp

import (
	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/parse"
)

// documentSymbols returns a flat list of top-level symbols for the source.
// Symbols need only a parse (no imports, no type info); a parse failure yields
// an empty list (the parse error is already reported as a diagnostic).
func documentSymbols(text string) []DocumentSymbol {
	m, err := parse.Module(text)
	if err != nil {
		return []DocumentSymbol{}
	}
	syms := []DocumentSymbol{}
	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			if d.Name == "" {
				continue // destructuring `let (a,b) = ...`: no single name
			}
			kind := SymbolKindVariable
			if len(d.Params) > 0 {
				kind = SymbolKindFunction
			}
			syms = append(syms, symbolAt(d.Name, kind, d.Pos))
		case *ast.TypeDecl:
			kind := SymbolKindEnum
			if d.Record != nil {
				kind = SymbolKindStruct
			}
			syms = append(syms, symbolAt(d.Name, kind, d.Pos))
		}
	}
	return syms
}

// symbolAt builds a DocumentSymbol whose range spans the name at pos (1-based
// pos -> 0-based LSP range).
func symbolAt(name string, kind int, pos ast.Pos) DocumentSymbol {
	start := Position{Line: pos.Line - 1, Character: pos.Col - 1}
	end := Position{Line: pos.Line - 1, Character: pos.Col - 1 + len(name)}
	r := Range{Start: start, End: end}
	return DocumentSymbol{Name: name, Kind: kind, Range: r, SelectionRange: r}
}
```

- [ ] **Step 5: Handle the request in `server.go`**

Add a case to `dispatch`:

```go
	case "textDocument/documentSymbol":
		var p DocumentSymbolParams
		_ = json.Unmarshal(msg.Params, &p)
		text, _ := s.docs.get(p.TextDocument.URI)
		_ = s.conn.Reply(msg.ID, documentSymbols(text))
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/lsp/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/lsp/symbols.go internal/lsp/symbols_test.go internal/lsp/protocol.go internal/lsp/server.go
git commit -m "feat(lsp): flat document symbols from the AST"
```

---

### Task 7: `sigil lsp` command + end-to-end integration test

**Files:**
- Create: `internal/cli/lsp.go`
- Modify: `internal/cli/root.go` (register `newLspCmd`)
- Test: `internal/lsp/integration_test.go` (create), `internal/cli/lsp_test.go` (create)

**Interfaces:**
- Consumes: `lsp.NewServer(r, w).Run()` (Tasks 3–6), the cobra tree + `run(...)` test helper.
- Produces: `func newLspCmd() *cobra.Command` — runs `lsp.NewServer(cmd.InOrStdin(), cmd.OutOrStdout()).Run()`.

- [ ] **Step 1: Write the failing integration test**

Create `internal/lsp/integration_test.go`:

```go
package lsp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEndDiagnosticsAndSymbols drives the assembled server over pipes:
// initialize -> didOpen (a file with a type error) -> expect publishDiagnostics;
// then documentSymbol -> expect the symbol list.
func TestEndToEndDiagnosticsAndSymbols(t *testing.T) {
	// A workspace with std/ so imports resolve; entry has a deliberate type error.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app.sigil"), "pub let bad = 1 + \"two\"\n")
	uri := "file://" + filepath.Join(root, "app.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"pub let bad = 1 + \"two\"\n"}}}`)
	waitFor(t, &out, "publishDiagnostics")
	waitFor(t, &out, "type mismatch")

	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/documentSymbol","params":{"textDocument":{"uri":"`+uri+`"}}}`)
	waitFor(t, &out, `"bad"`)

	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func send(w io.Writer, body string) { io.WriteString(w, frame(body)) }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sanity: clean source yields an empty diagnostics array.
func TestEndToEndCleanFileClearsDiagnostics(t *testing.T) {
	root := t.TempDir()
	uri := "file://" + filepath.Join(root, "ok.sigil")
	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()
	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"pub let x = 1\n"}}}`)
	waitFor(t, &out, "publishDiagnostics")
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
	// The published diagnostics array for a clean file must be empty.
	if !strings.Contains(out.String(), `"diagnostics":[]`) {
		// Decode the last publishDiagnostics to be robust to field ordering.
		if !containsEmptyDiagnostics(t, out.String()) {
			t.Errorf("clean file should publish empty diagnostics; got:\n%s", out.String())
		}
	}
}

func containsEmptyDiagnostics(t *testing.T, raw string) bool {
	t.Helper()
	for _, chunk := range strings.Split(raw, "Content-Length:") {
		i := strings.Index(chunk, "{")
		if i < 0 {
			continue
		}
		var env struct {
			Method string `json:"method"`
			Params struct {
				Diagnostics []json.RawMessage `json:"diagnostics"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(chunk[i:]), &env) == nil &&
			env.Method == "textDocument/publishDiagnostics" &&
			len(env.Params.Diagnostics) == 0 {
			return true
		}
	}
	return false
}
```

(Reuses `frame`, `safeBuffer`, `waitFor` from `lifecycle_test.go`/`jsonrpc_test.go` — same package.)

- [ ] **Step 2: Run integration test to verify it fails or passes**

Run: `go test ./internal/lsp/ -run TestEndToEnd -v`
Expected: PASS (all server pieces exist by now). If it FAILS on the overlay/path, confirm the `didOpen` text reaches analysis — the overlay path must equal `filepath.Abs(uriToPath(uri))`.

- [ ] **Step 3: Create `internal/cli/lsp.go`**

```go
package cli

import (
	"github.com/incantery/sigil/internal/lsp"
	"github.com/spf13/cobra"
)

func newLspCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lsp",
		Short: "Run the sigil language server (LSP) over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return lsp.NewServer(cmd.InOrStdin(), cmd.OutOrStdout()).Run()
		},
	}
}
```

- [ ] **Step 4: Register it in `internal/cli/root.go`**

Add after `root.AddCommand(newDevCmd())`:

```go
	root.AddCommand(newLspCmd())
```

- [ ] **Step 5: Add a CLI smoke test `internal/cli/lsp_test.go`**

```go
package cli

import (
	"strings"
	"testing"
)

func TestLspCommandRegistered(t *testing.T) {
	// `sigil lsp` exists and shows usage with --help (does not start the server).
	out, _, err := run("lsp", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "language server") {
		t.Errorf("lsp --help should describe the language server; got: %s", out)
	}
}
```

- [ ] **Step 6: Run tests + build the binary**

Run: `go test ./internal/lsp/ ./internal/cli/ && go run ./cmd/sigil lsp --help`
Expected: PASS; `lsp --help` prints usage.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/lsp.go internal/cli/root.go internal/cli/lsp_test.go internal/lsp/integration_test.go
git commit -m "feat(cli): sigil lsp command + end-to-end LSP integration test"
```

---

### Task 8: Docs — editor LSP note + CLAUDE.md

**Files:**
- Create: `editor/lsp.md`
- Modify: `CLAUDE.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: Write `editor/lsp.md`**

```markdown
# `sigil lsp` — language server

`sigil lsp` is a stdio Language Server Protocol server giving **live
diagnostics** and **document symbols** for `.sigil` files. It is a thin layer
over the compiler (`internal/load` + `internal/types`); the JSON-RPC base
protocol is hand-rolled (no external LSP dependency).

## What it provides (v1)

- **Diagnostics** — type/parse/lex errors as you type (the editor's unsaved
  buffer is type-checked via an in-memory overlay). One diagnostic per file:
  the compiler stops at the first error, so fix it and the next appears.
- **Document symbols** — top-level `let`/`type` declarations (functions,
  values, enums, records) for the outline / symbol picker. Flat for now;
  constructors and record fields as children come with #3 (they need source
  positions added to those AST nodes).

## Neovim

```lua
vim.api.nvim_create_autocmd("FileType", {
  pattern = "sigil",
  callback = function(args)
    vim.lsp.start({
      name = "sigil",
      cmd = { "sigil", "lsp" },
      root_dir = vim.fs.root(args.buf, { "std", ".git" }),
    })
  end,
})
```

`root_dir` must be the directory that contains `std/`, so imports like
`import "std/ui"` resolve. (`sigil` must be on `PATH` — `make build` then add
`bin/` to PATH, or use an absolute `cmd`.)

## Not yet (→ #3/#4)

Hover, go-to-definition, semantic tokens, completion, multi-error reporting,
incremental sync. See `docs/superpowers/specs/2026-06-21-sigil-lsp-foundation-design.md`.
```

- [ ] **Step 2: Update `CLAUDE.md`**

- In the `internal/` description, change the CLI subcommand list from
  `version, check, build, serve, dev` to `version, check, build, serve, dev, lsp`.
- In "What's next", under the editor roadmap item, mark **#2 LSP foundation —
  DONE** (diagnostics + document symbols; `sigil lsp` over `internal/load`/
  `internal/types`; live via a `load` overlay; one diagnostic per file; flat
  symbols). Note the remaining editor sub-projects (#3 type-aware, #4 completion)
  and that flat→hierarchical symbols needs `Pos` on `Variant`/`FieldType`.
- Optionally add a one-line pointer to `editor/lsp.md` near the editor notes.

Make targeted edits matching the surrounding prose; don't restructure.

- [ ] **Step 3: Full-repo validation**

Run: `go build ./... && go test ./...`
Expected: PASS (browser tests run or skip; either is fine).

- [ ] **Step 4: Commit**

```bash
git add editor/lsp.md CLAUDE.md
git commit -m "docs: sigil lsp foundation (diagnostics + document symbols)"
```

---

## Self-Review

**Spec coverage:**
- §Architecture/1 command + transport → Task 2 (jsonrpc) + Task 7 (command). ✓
- §2 lifecycle + capabilities → Task 3. ✓
- §3 document sync + load overlay → Task 1 (overlay) + Task 4 (store/sync). ✓
- §4 diagnostics (overlay analysis, error→range, empty-on-success, positionless→(0,0), single diagnostic) → Task 5. ✓
- §5 document symbols (flat, kinds, re-parse, parse-fail→empty) → Task 6. ✓
- §Testing (framing round-trip, diagnostics mapping incl. overlay & clean & missing-import, symbols, integration over a pipe) → Tasks 2, 5, 6, 7; overlay shadow test in Task 1. ✓
- §Manual Neovim note → Task 8. ✓
- §Affected code (internal/lsp/*, cli/lsp.go, load overlay, root registration, docs) → all covered. ✓

**Placeholder scan:** No TBD/TODO. Every code step has complete code; every run step states the command + expected result. Task 3's test note about the minimal `Read` helper gives concrete fallback assertions (id-1 reply + capability string via `waitFor`).

**Type consistency:** `Options.Overlay map[string]string` (Task 1) consumed identically in `docStore.overlay()` (Task 4/5) and `analyze` (Task 5). `Conn` methods `Read`/`Reply`/`ReplyError`/`Notify` (Task 2) used in Tasks 3/5/6. `Server.dispatch`/`publishDiagnostics`/`docs` consistent across Tasks 3–7. Protocol structs (`Position`,`Range`,`Diagnostic`,`DocumentSymbol`, kind/severity consts) defined once in `protocol.go` and reused. `documentSymbols`, `diagnosticsFor`, `uriToPath`, `analyze`, `frame`, `safeBuffer`, `waitFor`, `send` names consistent across tasks/tests. ✓

**One cross-task note for the implementer:** the overlay key and the entry path passed to `load.Load` must be the same form. Task 5 absolutizes both (`filepath.Abs(uriToPath(uri))` in `publishDiagnostics`, and `filepath.Abs` inside `docStore.overlay()`). Keep them identical or live diagnostics for the open buffer won't apply.
```
