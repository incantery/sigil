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

func TestConnReplyErrorFramesError(t *testing.T) {
	var out bytes.Buffer
	c := NewConn(strings.NewReader(""), &out)
	if err := c.ReplyError(json.RawMessage("5"), CodeMethodNotFound, "nope"); err != nil {
		t.Fatal(err)
	}
	body := out.String()[strings.Index(out.String(), "\r\n\r\n")+4:]
	var env struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("error body not valid JSON: %v\n%s", err, body)
	}
	if env.JSONRPC != "2.0" || string(env.ID) != "5" || env.Error.Code != CodeMethodNotFound || env.Error.Message != "nope" {
		t.Errorf("unexpected error envelope: %+v", env)
	}
}

func TestConnNotifyFramesNotification(t *testing.T) {
	var out bytes.Buffer
	c := NewConn(strings.NewReader(""), &out)
	if err := c.Notify("textDocument/publishDiagnostics", map[string]string{"uri": "file:///x"}); err != nil {
		t.Fatal(err)
	}
	body := out.String()[strings.Index(out.String(), "\r\n\r\n")+4:]
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("notify body not valid JSON: %v\n%s", err, body)
	}
	if _, hasID := env["id"]; hasID {
		t.Error("notification must not carry an id")
	}
	if string(env["method"]) != `"textDocument/publishDiagnostics"` {
		t.Errorf("method = %s", env["method"])
	}
}

func TestIsNotificationNullID(t *testing.T) {
	if !(&Message{ID: json.RawMessage("null")}).IsNotification() {
		t.Error("null id should be treated as a notification")
	}
	if (&Message{ID: json.RawMessage("1")}).IsNotification() {
		t.Error("numeric id is a request, not a notification")
	}
}
