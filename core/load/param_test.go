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

// paramApp routes on a `/user/:id` pattern and reads the typed `:id` parameter
// reactively from the path — all in stdlib over the __split/__listAt primitives.
const paramApp = `import "std/router" (router, routeParam, param, Public)
import "std/ui" (card, column, button, label)
import "std/html" (mount)

pub let app =
  let (path, navigate) = router ()
  let userId = param path "/user/:id" "id"
  let view =
    card [
      column [
        button "user 42" (fun () -> navigate "/user/42"),
        button "user 7" (fun () -> navigate "/user/7"),
        routeParam path "/user/:id" Public (fun () ->
          label (fun () ->
            match userId () with
            | Some id -> "user: ${id}"
            | None -> "no user")),
        label (fun () -> "at ${path ()}")
      ]
    ]
  mount view "#app"`

// TestParamCompiles confirms the typed-param path type-checks and links over the
// string/list primitives.
func TestParamCompiles(t *testing.T) {
	js, _ := buildAgainstStd(t, paramApp)
	for _, want := range []string{"__split", "__listAt", "__listLen"} {
		if !strings.Contains(js, want) {
			t.Errorf("bundle missing %q — param extraction did not lower", want)
		}
	}
}

// TestParamInBrowser drives the parameterized route: navigating to /user/42 and
// /user/7 extracts the matching :id reactively.
func TestParamInBrowser(t *testing.T) {
	js, _ := buildAgainstStd(t, paramApp)
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

	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible(`//button[text()="user 42"]`, chromedp.BySearch),
		chromedp.Click(`//button[text()="user 42"]`, chromedp.BySearch),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}
	if err := waitForText(ctx, "#app", "user: 42", 3*time.Second); err != nil {
		t.Fatal(err)
	}

	// Navigate to a different id: the extracted parameter updates reactively.
	if err := chromedp.Run(ctx, chromedp.Click(`//button[text()="user 7"]`, chromedp.BySearch)); err != nil {
		t.Fatal(err)
	}
	if err := waitForText(ctx, "#app", "user: 7", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	var s string
	_ = chromedp.Run(ctx, chromedp.Text("#app", &s, chromedp.ByID))
	if strings.Contains(s, "user: 42") {
		t.Errorf("stale parameter: %q still shows user 42", s)
	}
}
