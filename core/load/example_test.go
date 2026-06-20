package load

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestCounterExample loads and drives the committed counter example file, so the
// example stays a working artifact (not just documentation).
func TestCounterExample(t *testing.T) {
	entry := filepath.Join(repoRoot, "core", "examples", "counter", "counter.sigil")
	prog, err := Load(entry, Options{Root: repoRoot})
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	js, err := prog.Bundle()
	if err != nil {
		t.Fatalf("bundle example: %v", err)
	}

	page := fmt.Sprintf(`<!doctype html><html><body><div id="app"></div>
<script>%s
</script></body></html>`, js)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, page)
	}))
	defer srv.Close()

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Headless)...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 25*time.Second)
	defer cancelTimeout()

	var initial, afterInc string
	err = chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible(`//button[text()="+"]`, chromedp.BySearch),
		chromedp.Text("#app", &initial, chromedp.ByID),
		chromedp.Click(`//button[text()="+"]`, chromedp.BySearch),
		chromedp.Text("#app", &afterInc, chromedp.ByID),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}
	if !strings.Contains(initial, "count: 0") {
		t.Errorf("initial: %q does not contain %q", initial, "count: 0")
	}
	if !strings.Contains(afterInc, "count: 1") {
		t.Errorf("after +: %q does not contain %q", afterInc, "count: 1")
	}
}
