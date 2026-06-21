// Package lsp implements a minimal Language Server Protocol server for sigil:
// live diagnostics and document symbols over internal/load + internal/types.
// The base protocol (JSON-RPC 2.0 over Content-Length-framed stdio) is
// hand-rolled here — no external LSP dependency.
package lsp

import (
	"bufio"
	"bytes"
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
func (m *Message) IsNotification() bool { return len(m.ID) == 0 || string(m.ID) == "null" }

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
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	// json.Encoder.Encode appends exactly one trailing newline; drop just that
	// one byte (TrimRight would also eat any legitimately-trailing newlines).
	body := buf.Bytes()
	if n := len(body); n > 0 && body[n-1] == '\n' {
		body = body[:n-1]
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := c.w.Write(body)
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
