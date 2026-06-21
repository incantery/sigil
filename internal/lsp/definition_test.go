package lsp

import (
	"io"
	"path/filepath"
	"testing"
)

func TestDefinitionParamSameFile(t *testing.T) {
	root := t.TempDir()
	src := "let inc n = n + 1\nlet main = inc 41\n"
	writeFile(t, filepath.Join(root, "app.sigil"), src)
	uri := "file://" + filepath.Join(root, "app.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"let inc n = n + 1\nlet main = inc 41\n"}}}`)
	// go-to-def on the use of `n` at line 1 (0-based 0), col 13 (0-based 12).
	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":0,"character":12}}}`)
	// Expect a Location pointing back into the same file at the param binder
	// (line 0, character 8 — 0-based of 1:9).
	waitFor(t, &out, `"character":8`)

	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func TestDefinitionImportedCrossFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib.sigil"), "pub let answer = 42\n")
	appPath := filepath.Join(root, "app.sigil")
	writeFile(t, appPath, "import \"lib\" (answer)\nlet main = answer\n")
	uri := "file://" + appPath
	libURI := "file://" + filepath.Join(root, "lib.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"import \"lib\" (answer)\nlet main = answer\n"}}}`)
	// def on `answer` use at line 2 (0-based 1), col 12 (0-based 11).
	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":1,"character":11}}}`)
	waitFor(t, &out, libURI) // the reply Location points into lib.sigil

	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func TestDefinitionCapabilityAdvertised(t *testing.T) {
	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()
	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///tmp"}}`)
	waitFor(t, &out, "definitionProvider")
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}
