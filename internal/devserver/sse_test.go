package devserver

import (
	"strings"
	"testing"
	"time"
)

func TestMessageFraming(t *testing.T) {
	r := ReloadMsg(`console.log("hi")`)
	if !strings.Contains(r, `"type":"reload"`) || !strings.Contains(r, `console.log`) {
		t.Errorf("reload msg malformed: %s", r)
	}
	if !strings.Contains(r, `"bundle":`) {
		t.Errorf("reload msg missing bundle key: %s", r)
	}
	e := ErrorMsg("type error at 3:1")
	if !strings.Contains(e, `"type":"error"`) || !strings.Contains(e, "type error") {
		t.Errorf("error msg malformed: %s", e)
	}
	if !strings.Contains(e, `"message":`) {
		t.Errorf("error msg missing message key: %s", e)
	}
	// Must be single-line JSON so SSE `data:` framing stays one event.
	if strings.Contains(r, "\n") || strings.Contains(e, "\n") {
		t.Error("messages must be newline-free for SSE framing")
	}
}

func TestHubBroadcast(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	defer cancel()
	h.Broadcast("hello")
	select {
	case got := <-ch:
		if got != "hello" {
			t.Errorf("got %q, want hello", got)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber received nothing")
	}
}
