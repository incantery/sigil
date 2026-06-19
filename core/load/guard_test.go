package load

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// guardApp has a public route and a guarded /admin route. The admin content is
// gated by an `authed` cell, declared as a Guard — the access policy is a
// required, typed argument to `route`.
const guardApp = `import "std/router" (router, route, Public, Guard)
import "std/reactive" (cell)
import "std/ui" (card, column, button, text, label)
import "std/html" (mount)

pub let app =
  let (path, navigate) = router ()
  let (authed, setAuthed) = cell false
  let view =
    card [
      column [
        button "admin" (fun () -> navigate "/admin"),
        button "login" (fun () -> setAuthed (!(authed ()))),
        route path "/" Public (fun () -> text "public home"),
        route path "/admin" (Guard authed) (fun () -> text "secret admin panel"),
        label (fun () -> "at ${path ()}")
      ]
    ]
  mount view "#app"`

// TestGuardDefaultDeny is the security capstone: a route with no access policy
// does not type-check. Passing the view where the Access argument belongs is a
// type error, so "every route must declare Public or a Guard" is enforced by the
// type system — default-deny by construction.
func TestGuardDefaultDeny(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.mako")
	// route expects (current, path, access, view); this omits access.
	src := `import "std/router" (router, route)
import "std/ui" (text)
import "std/html" (mount)

pub let app =
  let (path, navigate) = router ()
  mount (route path "/admin" (fun () -> text "secret")) "#app"`
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(entry, Options{Root: repoRoot}); err == nil {
		t.Fatal("expected a type error: a route with no access policy must not compile")
	}
}

// TestGuardGatesContent drives the guarded SPA: the admin panel is hidden until
// the auth guard passes, then appears — all reactively.
func TestGuardGatesContent(t *testing.T) {
	js, _ := buildAgainstStd(t, guardApp)
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
		chromedp.WaitVisible(`//button[text()="admin"]`, chromedp.BySearch),
		chromedp.Click(`//button[text()="admin"]`, chromedp.BySearch),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}

	// On /admin but unauthenticated: the guard fails, panel is hidden.
	if err := waitForText(ctx, "#app", "at /admin", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	var gated string
	_ = chromedp.Run(ctx, chromedp.Text("#app", &gated, chromedp.ByID))
	if strings.Contains(gated, "secret admin panel") {
		t.Fatalf("guard failed: admin panel shown while unauthenticated: %q", gated)
	}

	// Authenticate: the guard now passes and the panel appears.
	if err := chromedp.Run(ctx, chromedp.Click(`//button[text()="login"]`, chromedp.BySearch)); err != nil {
		t.Fatal(err)
	}
	if err := waitForText(ctx, "#app", "secret admin panel", 3*time.Second); err != nil {
		t.Fatal(err)
	}
}
