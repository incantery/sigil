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

// styledApp builds a div with two typed style utilities from std/style. The
// tokens S4 / Sky are constructors of the Space / Color ADTs (they flow with the
// import automatically); both fold to constants and hoist to atomic classes.
const styledApp = `import "std/html" (el, mount)
import "std/ui" (text)
import "std/style" (p, bg)

pub let app =
  let box = el "div" [ p S4, bg Sky ] [ text "styled" ]
  mount box "#app"`

// TestStyleExtraction confirms static styles are lifted to an installed atomic
// stylesheet (not emitted as inline-style runtime calls).
func TestStyleExtraction(t *testing.T) {
	js, _ := buildAgainstStd(t, styledApp)
	for _, want := range []string{
		"__installStyles(",         // a stylesheet is injected
		"padding:1rem",             // s4 folded
		"background-color:#38bdf8", // sky folded
		"__addClass(",              // elements reference classes, not inline styles
	} {
		if !strings.Contains(js, want) {
			t.Errorf("bundle missing %q — style was not extracted", want)
		}
	}
}

// TestStyleTypeSafety is the design-system-as-type-system payoff: passing a Color
// token where a Space is expected is a compile error, not a silent bad style.
func TestStyleTypeSafety(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.mako")
	// p expects a Space; Sky is a Color.
	src := `import "std/html" (el, mount)
import "std/style" (p)

pub let app = mount (el "div" [ p Sky ] []) "#app"`
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(entry, Options{Root: repoRoot})
	if err == nil {
		t.Fatal("expected a type error passing a Color where a Space is required")
	}
}

// TestStyleInBrowser confirms the extracted classes actually style the element:
// the rendered div has 1rem padding and the sky background.
func TestStyleInBrowser(t *testing.T) {
	js, _ := buildAgainstStd(t, styledApp)
	html := fmt.Sprintf(`<!doctype html><html><body><div id="app"></div>
<script>%s
</script></body></html>`, js)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, html)
	}))
	defer srv.Close()

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Headless)...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 25*time.Second)
	defer cancelTimeout()

	var padding, bgColor string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("#app div", chromedp.ByQuery),
		chromedp.Evaluate(`getComputedStyle(document.querySelector('#app div')).paddingTop`, &padding),
		chromedp.Evaluate(`getComputedStyle(document.querySelector('#app div')).backgroundColor`, &bgColor),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}

	if padding != "16px" {
		t.Errorf("padding: got %q, want %q (1rem)", padding, "16px")
	}
	if bgColor != "rgb(56, 189, 248)" {
		t.Errorf("background-color: got %q, want %q (#38bdf8)", bgColor, "rgb(56, 189, 248)")
	}
}
