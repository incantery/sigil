package lsp

import (
	"io"
	"path/filepath"
	"testing"
)

func TestCompletion(t *testing.T) {
	root := t.TempDir()
	src := "import \"std/ui\" (card)\nlet inc n = n\n"
	writeFile(t, filepath.Join(root, "app.sigil"), src)
	uri := "file://" + filepath.Join(root, "app.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"import \"std/ui\" (card)\nlet inc n = n\n"}}}`)
	// completion in inc's body (line 2, 0-based line 1, char 12 = on `n`).
	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":1,"character":12}}}`)
	// The reply lists the local `n`, the top-level fn `inc`, the import `card`,
	// and a keyword. Assert a few labels appear in the items.
	waitFor(t, &out, `"label":"inc"`)
	waitFor(t, &out, `"label":"card"`)
	waitFor(t, &out, `"label":"match"`)
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func TestCompletionCapabilityAdvertised(t *testing.T) {
	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()
	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///tmp"}}`)
	waitFor(t, &out, "completionProvider")
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}
