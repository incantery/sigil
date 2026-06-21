package lsp

import (
	"io"
	"path/filepath"
	"testing"
)

func TestHoverReturnsType(t *testing.T) {
	root := t.TempDir()
	// `inc` is a top-level binding; hovering its use shows its scheme.
	writeFile(t, filepath.Join(root, "app.sigil"), "let inc n = n + 1\nlet main = inc 41\n")
	uri := "file://" + filepath.Join(root, "app.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"let inc n = n + 1\nlet main = inc 41\n"}}}`)
	// hover the use of `inc` at line 2 (0-based line 1), col 12 (0-based char 11).
	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":1,"character":11}}}`)
	waitFor(t, &out, "Int -> Int")

	// hover an empty region -> null result (no panic, no type).
	send(cw, `{"jsonrpc":"2.0","id":3,"method":"textDocument/hover","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":40,"character":0}}}`)
	waitFor(t, &out, `"id":3`)
	waitFor(t, &out, `"result":null`)

	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func TestHoverCapabilityAdvertised(t *testing.T) {
	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()
	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///tmp"}}`)
	waitFor(t, &out, "hoverProvider")
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}
