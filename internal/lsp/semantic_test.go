package lsp

import (
	"io"
	"path/filepath"
	"testing"
)

func TestSemanticTokensFull(t *testing.T) {
	root := t.TempDir()
	src := "let inc n = n + 1\n"
	writeFile(t, filepath.Join(root, "app.sigil"), src)
	uri := "file://" + filepath.Join(root, "app.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"let inc n = n + 1\n"}}}`)
	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/semanticTokens/full","params":{"textDocument":{"uri":"`+uri+`"}}}`)
	// The reply carries a non-empty "data" array; the first token is `let`
	// (keyword=6) at delta 0,0 length 3 → starts with [0,0,3,6,0,...
	waitFor(t, &out, `"data":[0,0,3,6,0`)
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func TestSemanticTokensCapabilityAdvertised(t *testing.T) {
	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()
	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///tmp"}}`)
	waitFor(t, &out, "semanticTokensProvider")
	waitFor(t, &out, "enumMember") // the legend is present
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}
