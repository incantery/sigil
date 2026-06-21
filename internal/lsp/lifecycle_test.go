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
	t.Cleanup(func() { cw.Close() }) // unblock the server goroutine if a test fails before sending exit
	return cw, out
}

func TestInitializeReplyAndCapabilities(t *testing.T) {
	cw, out := startServer(t)
	io.WriteString(cw, frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///tmp/proj"}}`))
	// The initialize reply carries id 1 and advertises documentSymbolProvider.
	waitFor(t, out, `"id":1`)
	waitFor(t, out, "documentSymbolProvider")
	waitFor(t, out, `"result"`)
	io.WriteString(cw, frame(`{"jsonrpc":"2.0","method":"exit"}`))
}

func TestUnknownRequestGetsMethodNotFound(t *testing.T) {
	cw, out := startServer(t)
	io.WriteString(cw, frame(`{"jsonrpc":"2.0","id":9,"method":"textDocument/unknownMethod","params":{}}`))
	waitFor(t, out, "-32601") // MethodNotFound for an unimplemented request
	io.WriteString(cw, frame(`{"jsonrpc":"2.0","method":"exit"}`))
}
