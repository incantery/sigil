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
