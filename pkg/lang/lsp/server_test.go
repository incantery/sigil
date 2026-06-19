package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// testClient drives a Server over in-memory pipes the way an editor
// would: requests get matched responses; notifications queue up.
type testClient struct {
	t      *testing.T
	toSrv  io.WriteCloser
	fromSv *bufio.Reader
	nextID int

	notifications chan rpcIncoming
	responses     chan rpcIncoming
	done          chan error
}

type rpcIncoming struct {
	ID     *json.RawMessage `json:"id"`
	Method string           `json:"method"`
	Result json.RawMessage  `json:"result"`
	Error  *rpcError        `json:"error"`
	Params json.RawMessage  `json:"params"`
}

func newTestClient(t *testing.T) *testClient {
	t.Helper()
	srvIn, cliOut := io.Pipe()
	cliIn, srvOut := io.Pipe()
	srv := NewServer(srvIn, srvOut, nil, "test")
	c := &testClient{
		t:             t,
		toSrv:         cliOut,
		fromSv:        bufio.NewReader(cliIn),
		notifications: make(chan rpcIncoming, 16),
		responses:     make(chan rpcIncoming, 16),
		done:          make(chan error, 1),
	}
	go func() { c.done <- srv.Run() }()
	go func() {
		for {
			body, err := readMessage(c.fromSv)
			if err != nil {
				close(c.notifications)
				close(c.responses)
				return
			}
			var msg rpcIncoming
			if err := json.Unmarshal(body, &msg); err != nil {
				continue
			}
			if msg.ID != nil {
				c.responses <- msg
			} else {
				c.notifications <- msg
			}
		}
	}()
	t.Cleanup(func() {
		c.notifyRaw("exit", nil)
		_ = c.toSrv.Close()
	})
	return c
}

func (c *testClient) call(method string, params any) json.RawMessage {
	c.t.Helper()
	c.nextID++
	id := json.RawMessage(fmt.Sprintf("%d", c.nextID))
	p, _ := json.Marshal(params)
	msg := map[string]any{
		"jsonrpc": "2.0", "id": &id, "method": method, "params": json.RawMessage(p),
	}
	if err := writeMessage(c.toSrv, msg); err != nil {
		c.t.Fatalf("write %s: %v", method, err)
	}
	select {
	case resp, ok := <-c.responses:
		if !ok {
			c.t.Fatalf("server closed before responding to %s", method)
		}
		if resp.Error != nil {
			c.t.Fatalf("%s returned error: %+v", method, resp.Error)
		}
		return resp.Result
	case <-time.After(5 * time.Second):
		c.t.Fatalf("timeout waiting for %s response", method)
	}
	return nil
}

func (c *testClient) notifyRaw(method string, params any) {
	p, _ := json.Marshal(params)
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": json.RawMessage(p)}
	_ = writeMessage(c.toSrv, msg)
}

func (c *testClient) waitNotification(method string) rpcIncoming {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case n, ok := <-c.notifications:
			if !ok {
				c.t.Fatalf("server closed waiting for %s", method)
			}
			if n.Method == method {
				return n
			}
		case <-deadline:
			c.t.Fatalf("timeout waiting for notification %s", method)
		}
	}
}

func (c *testClient) openDoc(uri, text string) {
	c.notifyRaw("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: uri, Version: 1, Text: text},
	})
}

const counterSrc = `// counter demo
view Counter =
  state count = 0
  card
    title "Counter"
    stack horizontal gap=1
      button "-" on click { count -= 1 }
      text count
      button "+" on click { count += 1 }
`

func TestInitializeAndDiagnosticsClean(t *testing.T) {
	c := newTestClient(t)
	res := c.call("initialize", map[string]any{})
	var init initializeResult
	if err := json.Unmarshal(res, &init); err != nil {
		t.Fatalf("bad initialize result: %v", err)
	}
	if init.Capabilities.TextDocumentSync != 1 {
		t.Errorf("want full sync, got %d", init.Capabilities.TextDocumentSync)
	}
	if len(init.Capabilities.SemanticTokensProvider.Legend.TokenTypes) == 0 {
		t.Error("missing semantic token legend")
	}

	c.openDoc("untitled:counter", counterSrc)
	n := c.waitNotification("textDocument/publishDiagnostics")
	var p publishDiagnosticsParams
	if err := json.Unmarshal(n.Params, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Diagnostics) != 0 {
		t.Errorf("clean file should have no diagnostics, got %+v", p.Diagnostics)
	}
}

func TestDiagnosticsOnError(t *testing.T) {
	c := newTestClient(t)
	c.call("initialize", map[string]any{})
	c.openDoc("untitled:bad", "view Broken\n")
	n := c.waitNotification("textDocument/publishDiagnostics")
	var p publishDiagnosticsParams
	if err := json.Unmarshal(n.Params, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Diagnostics) == 0 {
		t.Fatal("expected a parse diagnostic for missing `=`")
	}
	d := p.Diagnostics[0]
	if d.Severity != 1 {
		t.Errorf("want severity error, got %d", d.Severity)
	}
	if d.Range.Start.Line != 0 {
		t.Errorf("diagnostic should land on line 0, got %d", d.Range.Start.Line)
	}

	// Fixing the file clears the squiggles.
	c.notifyRaw("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": "untitled:bad", "version": 2},
		"contentChanges": []map[string]any{{"text": counterSrc}},
	})
	n = c.waitNotification("textDocument/publishDiagnostics")
	if err := json.Unmarshal(n.Params, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Diagnostics) != 0 {
		t.Errorf("fixed file should publish empty diagnostics, got %+v", p.Diagnostics)
	}
}

func TestLowerDiagnostics(t *testing.T) {
	c := newTestClient(t)
	c.call("initialize", map[string]any{})
	// Parses fine, fails lower: unknown component kind.
	c.openDoc("untitled:lower", "view X =\n  zorbleflux \"hi\"\n")
	n := c.waitNotification("textDocument/publishDiagnostics")
	var p publishDiagnosticsParams
	if err := json.Unmarshal(n.Params, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Diagnostics) == 0 {
		t.Fatal("expected a lower diagnostic for unknown kind")
	}
	if !strings.Contains(p.Diagnostics[0].Source, "lower") {
		t.Errorf("want lower-stage source, got %q", p.Diagnostics[0].Source)
	}
	if p.Diagnostics[0].Range.Start.Line != 1 {
		t.Errorf("diagnostic should land on line 1, got %d", p.Diagnostics[0].Range.Start.Line)
	}
}

func TestSemanticTokens(t *testing.T) {
	c := newTestClient(t)
	c.call("initialize", map[string]any{})
	c.openDoc("untitled:counter", counterSrc)
	c.waitNotification("textDocument/publishDiagnostics")

	res := c.call("textDocument/semanticTokens/full", docParams{
		TextDocument: textDocumentIdentifier{URI: "untitled:counter"},
	})
	var st semanticTokens
	if err := json.Unmarshal(res, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Data)%5 != 0 || len(st.Data) == 0 {
		t.Fatalf("token data must be non-empty multiple of 5, got %d", len(st.Data))
	}

	// Decode and spot-check key classifications.
	type decoded struct{ line, col, length, typ, mods int }
	var toks []decoded
	line, col := 0, 0
	for i := 0; i < len(st.Data); i += 5 {
		dl, dc := st.Data[i], st.Data[i+1]
		if dl > 0 {
			line += dl
			col = dc
		} else {
			col += dc
		}
		toks = append(toks, decoded{line, col, st.Data[i+2], st.Data[i+3], st.Data[i+4]})
	}

	find := func(line, col int) *decoded {
		for i := range toks {
			if toks[i].line == line && toks[i].col == col {
				return &toks[i]
			}
		}
		return nil
	}

	// line 0: `// counter demo` → comment.
	if tk := find(0, 0); tk == nil || tk.typ != tokComment {
		t.Errorf("line 0 should be a comment token, got %+v", tk)
	}
	// line 1 col 0: `view` keyword; col 5: Counter as class decl.
	if tk := find(1, 0); tk == nil || tk.typ != tokKeyword {
		t.Errorf("`view` should be keyword, got %+v", tk)
	}
	if tk := find(1, 5); tk == nil || tk.typ != tokClass || tk.mods&modDeclaration == 0 {
		t.Errorf("`Counter` should be class declaration, got %+v", tk)
	}
	// line 2: `state` keyword + `count` property + `0` number.
	if tk := find(2, 8); tk == nil || tk.typ != tokProperty {
		t.Errorf("`count` should be property, got %+v", tk)
	}
	// line 3: `card` builtin → function + defaultLibrary.
	if tk := find(3, 2); tk == nil || tk.typ != tokFunction || tk.mods&modDefaultLibrary == 0 {
		t.Errorf("`card` should be builtin function, got %+v", tk)
	}
	// line 6: handler — `on` keyword at col 17, `click` event at col 20.
	if tk := find(6, 17); tk == nil || tk.typ != tokKeyword {
		t.Errorf("handler `on` should be keyword, got %+v", tk)
	}
	if tk := find(6, 20); tk == nil || tk.typ != tokEvent {
		t.Errorf("`click` should be event, got %+v", tk)
	}
	// line 6: `count` inside handler braces → variable.
	if tk := find(6, 28); tk == nil || tk.typ != tokVariable {
		t.Errorf("handler `count` should be variable, got %+v", tk)
	}
}

func TestDocumentSymbolsAndDefinition(t *testing.T) {
	c := newTestClient(t)
	c.call("initialize", map[string]any{})
	c.openDoc("untitled:counter", counterSrc)
	c.waitNotification("textDocument/publishDiagnostics")

	res := c.call("textDocument/documentSymbol", docParams{
		TextDocument: textDocumentIdentifier{URI: "untitled:counter"},
	})
	var syms []documentSymbol
	if err := json.Unmarshal(res, &syms); err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 || syms[0].Name != "Counter" {
		t.Fatalf("want one Counter symbol, got %+v", syms)
	}
	if len(syms[0].Children) != 1 || syms[0].Children[0].Name != "count" {
		t.Fatalf("Counter should contain the count cell, got %+v", syms[0].Children)
	}

	// Definition of `count` in `text count` (line 7, col 11) → state decl line 2.
	res = c.call("textDocument/definition", docPositionParams{
		TextDocument: textDocumentIdentifier{URI: "untitled:counter"},
		Position:     Position{Line: 7, Character: 11},
	})
	var loc Location
	if err := json.Unmarshal(res, &loc); err != nil {
		t.Fatal(err)
	}
	if loc.Range.Start.Line != 2 {
		t.Errorf("definition of count should be line 2, got %+v", loc)
	}

	// Hover over the same ref shows the state decl line.
	res = c.call("textDocument/hover", docPositionParams{
		TextDocument: textDocumentIdentifier{URI: "untitled:counter"},
		Position:     Position{Line: 7, Character: 11},
	})
	var h hover
	if err := json.Unmarshal(res, &h); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Contents.Value, "state count = 0") {
		t.Errorf("hover should show state decl, got %q", h.Contents.Value)
	}
}

// TestSemanticTokensAllExamples runs the token builder over every
// example app as a does-not-crash + sane-encoding sweep.
func TestSemanticTokensAllExamples(t *testing.T) {
	srcs := exampleSources(t)
	for path, text := range srcs {
		doc := &document{
			uri:   "file://" + path,
			path:  path,
			text:  text,
			lines: strings.Split(text, "\n"),
		}
		doc.an = analyze(doc)
		data := encodeTokens(doc)
		if len(data)%5 != 0 {
			t.Errorf("%s: token stream not a multiple of 5", path)
		}
		if len(data) == 0 {
			t.Errorf("%s: no tokens produced", path)
		}
	}
}
