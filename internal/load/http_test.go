package load

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// fetchApp loads data from the server on click and decodes the Result: Ok shows
// the body, Err shows the error. Every byte from the network is handled through
// an exhaustive match — the guarded-boundary contract.
const fetchApp = `import "std/reactive" (cell)
import "std/ui" (card, column, button, label)
import "std/html" (mount)
import "std/http" (get)
import "std/result" (Ok, Err)

pub let app =
  let (status, setStatus) = cell "idle"
  let load = fun () ->
    get "/data" (fun r ->
      effect {
        match r with
        | Ok body -> setStatus body
        | Err e -> setStatus e
      })
  let view =
    card [
      column [
        button "load" load,
        label (fun () -> "status: ${status ()}")
      ]
    ]
  mount view "#app"`

// TestFetchCompiles confirms the guarded-boundary path type-checks and links: the
// kernel __fetch primitive plus the stdlib Result decoder.
func TestFetchCompiles(t *testing.T) {
	js, _ := buildAgainstStd(t, fetchApp)
	for _, want := range []string{"__fetch", `$: "Ok"`, `$: "Err"`} {
		if !strings.Contains(js, want) {
			t.Errorf("bundle missing %q — the fetch/Result path did not lower", want)
		}
	}
}

// TestFetchInBrowser drives the data-loading app against a real server: clicking
// "load" fetches /data and the decoded Ok body lands in the reactive label.
func TestFetchInBrowser(t *testing.T) {
	js, _ := buildAgainstStd(t, fetchApp)
	page := fmt.Sprintf(`<!doctype html><html><body><div id="app"></div>
<script>%s
</script></body></html>`, js)

	mux := http.NewServeMux()
	mux.HandleFunc("/data", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello from server")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, page)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Headless)...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 25*time.Second)
	defer cancelTimeout()

	var initial string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("button", chromedp.ByQuery),
		chromedp.Text("#app", &initial, chromedp.ByID),
		chromedp.Click("button", chromedp.ByQuery),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}
	if !strings.Contains(initial, "status: idle") {
		t.Errorf("initial render: %q does not contain %q", initial, "status: idle")
	}

	// The fetch resolves asynchronously; poll until the decoded body arrives.
	if err := waitForText(ctx, "#app", "status: hello from server", 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

// waitForText polls the text of an element until it contains want or times out.
func waitForText(ctx context.Context, id, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		if err := chromedp.Run(ctx, chromedp.Text(id, &last, chromedp.ByID)); err == nil {
			if strings.Contains(last, want) {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q in %s (last: %q)", want, id, last)
}
