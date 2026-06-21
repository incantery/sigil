package devserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// Hub fans dev events out to every connected browser over Server-Sent Events.
type Hub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan string]struct{}{}} }

// Subscribe returns a channel of messages and a cancel function.
func (h *Hub) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// Broadcast delivers msg to every subscriber, dropping it for any slow consumer.
func (h *Hub) Broadcast(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// ServeHTTP is the /__sigil/events SSE endpoint.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := h.Subscribe()
	defer cancel()
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				return // client gone; stop streaming so the goroutine exits promptly
			}
			flusher.Flush()
		}
	}
}

func ReloadMsg(bundle string) string { return marshal("reload", "bundle", bundle) }
func ErrorMsg(message string) string { return marshal("error", "message", message) }

func marshal(typ, key, val string) string {
	b, _ := json.Marshal(map[string]string{"type": typ, key: val})
	return string(b)
}
