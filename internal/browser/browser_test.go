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

func TestSessionClickFillWaitScreenshot(t *testing.T) {
	srv := serveHTML(`<!doctype html><html><body>
<input id="in">
<button id="b" onclick="document.getElementById('out').textContent = document.getElementById('in').value">go</button>
<p id="out"></p>
<script>setTimeout(function(){var d=document.createElement('div');d.id='late';d.textContent='here';document.body.appendChild(d);},50)</script>
</body></html>`)
	defer srv.Close()

	sess, err := New()
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}
	defer sess.Close()
	if err := sess.Navigate(srv.URL); err != nil {
		t.Skipf("navigate failed (no Chrome?): %v", err)
	}

	if err := sess.WaitVisible("#late"); err != nil {
		t.Fatalf("WaitVisible: %v", err)
	}
	if err := sess.Fill("#in", "abc"); err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if err := sess.Click("#b"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	got, err := sess.DomText("#out")
	if err != nil {
		t.Fatalf("DomText: %v", err)
	}
	if got != "abc" {
		t.Errorf("after fill+click, #out = %q, want %q", got, "abc")
	}
	png, err := sess.ScreenshotPNG()
	if err != nil || len(png) == 0 {
		t.Errorf("ScreenshotPNG: err=%v len=%d", err, len(png))
	}
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
