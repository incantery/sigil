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

// routerApp is a two-route SPA built entirely from std/router — no compiler
// routing primitive. Each `route` is a reactive match on the current path.
const routerApp = `import "std/router" (router, route, Public)
import "std/ui" (card, column, button, text, label)
import "std/html" (mount)

pub let app =
  let (path, navigate) = router ()
  let view =
    card [
      column [
        button "home" (fun () -> navigate "/"),
        button "about" (fun () -> navigate "/about"),
        route path "/" Public (fun () -> text "you are home"),
        route path "/about" Public (fun () -> text "about us"),
        label (fun () -> "at ${path ()}")
      ]
    ]
  mount view "#app"`

// TestRouterCompiles confirms routing-as-library type-checks and links over the
// location intrinsics.
func TestRouterCompiles(t *testing.T) {
	js, _ := buildAgainstStd(t, routerApp)
	for _, want := range []string{"__path", "__pushPath", "__onPopState", "__when"} {
		if !strings.Contains(js, want) {
			t.Errorf("bundle missing %q — router did not lower over the location boundary", want)
		}
	}
}

// TestRouterInBrowser drives the SPA: navigating swaps the route view reactively
// and updates the URL, and browser back (popstate) restores the prior route.
func TestRouterInBrowser(t *testing.T) {
	js, _ := buildAgainstStd(t, routerApp)
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

	var home string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible(`//button[text()="about"]`, chromedp.BySearch),
		chromedp.Text("#app", &home, chromedp.ByID),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}
	if !strings.Contains(home, "you are home") || strings.Contains(home, "about us") {
		t.Errorf("initial route: %q, want home view (not about)", home)
	}

	// Navigate to /about: the about view mounts, home unmounts.
	if err := chromedp.Run(ctx, chromedp.Click(`//button[text()="about"]`, chromedp.BySearch)); err != nil {
		t.Fatal(err)
	}
	if err := waitForText(ctx, "#app", "about us", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	var about string
	_ = chromedp.Run(ctx, chromedp.Text("#app", &about, chromedp.ByID))
	if strings.Contains(about, "you are home") {
		t.Errorf("after navigate: %q still shows the home view", about)
	}
	if !strings.Contains(about, "at /about") {
		t.Errorf("after navigate: %q does not show the new path", about)
	}

	// Browser back: popstate restores the home route.
	if err := chromedp.Run(ctx, chromedp.Evaluate("history.back()", nil)); err != nil {
		t.Fatal(err)
	}
	if err := waitForText(ctx, "#app", "you are home", 3*time.Second); err != nil {
		t.Fatal(err)
	}
}
