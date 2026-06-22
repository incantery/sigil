package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveHTML serves a single static HTML page.
func serveHTML(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	}))
}

func TestSessionNavigateAndDomText(t *testing.T) {
	srv := serveHTML(`<!doctype html><html><body><h1 id="title">Hello</h1></body></html>`)
	defer srv.Close()

	sess, err := New()
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}
	defer sess.Close()

	if err := sess.Navigate(srv.URL); err != nil {
		t.Skipf("navigate failed (no Chrome?): %v", err)
	}
	got, err := sess.DomText("#title")
	if err != nil {
		t.Fatalf("DomText: %v", err)
	}
	if got != "Hello" {
		t.Errorf("DomText(#title) = %q, want %q", got, "Hello")
	}
}
