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

// listApp is a real data pipeline: fetch a newline-separated list, decode it with
// split, map each item, and render the rows with `each`. The "add" button builds
// a new list with append. Everything but the kernel primitives is stdlib.
const listApp = `import "std/reactive" (cell)
import "std/http" (get)
import "std/result" (Ok, Err)
import "std/string" (split)
import "std/list" (map, append)
import "std/ui" (card, column, button, text)
import "std/html" (el, each, mount)

pub let app =
  let (items, setItems) = cell []
  let load = fun () ->
    get "/items" (fun r ->
      effect {
        match r with
        | Ok body -> setItems (map (fun x -> "* ${x}") (split body "\n"))
        | Err e -> setItems [e]
      })
  let view =
    card [
      column [
        button "load" load,
        button "add" (fun () -> setItems (append (items ()) "* added")),
        each (fun () -> items ()) (fun x -> el "div" [] [ text x ])
      ]
    ]
  mount view "#app"`

// TestListCompiles confirms the fetch→decode→map→render pipeline type-checks and
// links over the list-builder primitive.
func TestListCompiles(t *testing.T) {
	js, _ := buildAgainstStd(t, listApp)
	for _, want := range []string{"__listConcat", "__each", "__split", "__fetch"} {
		if !strings.Contains(js, want) {
			t.Errorf("bundle missing %q — data-list pipeline did not lower", want)
		}
	}
}

// TestListInBrowser drives the pipeline: load fetches and renders the mapped
// rows; add appends a new one — all reactively reconciled by `each`.
func TestListInBrowser(t *testing.T) {
	js, _ := buildAgainstStd(t, listApp)
	page := fmt.Sprintf(`<!doctype html><html><body><div id="app"></div>
<script>%s
</script></body></html>`, js)

	mux := http.NewServeMux()
	mux.HandleFunc("/items", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "apple\nbanana\ncherry")
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

	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible(`//button[text()="load"]`, chromedp.BySearch),
		chromedp.Click(`//button[text()="load"]`, chromedp.BySearch),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}

	// The fetched, mapped rows render.
	if err := waitForText(ctx, "#app", "* cherry", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	var loaded string
	_ = chromedp.Run(ctx, chromedp.Text("#app", &loaded, chromedp.ByID))
	for _, want := range []string{"* apple", "* banana", "* cherry"} {
		if !strings.Contains(loaded, want) {
			t.Errorf("after load: %q missing %q", loaded, want)
		}
	}

	// Appending builds a new list; `each` reconciles in the new row.
	if err := chromedp.Run(ctx, chromedp.Click(`//button[text()="add"]`, chromedp.BySearch)); err != nil {
		t.Fatal(err)
	}
	if err := waitForText(ctx, "#app", "* added", 3*time.Second); err != nil {
		t.Fatal(err)
	}
}
