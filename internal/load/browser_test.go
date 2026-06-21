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

// counterApp is a complete sigil program whose entire UI is built from
// sigil-defined components (std/ui) and sigil-defined reactivity (std/reactive).
// Nothing here is a compiler built-in — `card`, `column`, `button`, `label`,
// and `cell` are all ordinary library functions resolved through the loader.
const counterApp = `import "std/reactive" (cell)
import "std/ui" (card, column, button, label)
import "std/html" (mount)

pub let app =
  let (count, setCount) = cell 0
  let view =
    card [
      column [
        label (fun () -> "count: ${count ()}"),
        button "+" (fun () -> setCount (count () + 1))
      ]
    ]
  mount view "#app"`

// echoApp is the canonical Echo: a text field whose value is mirrored into a
// label through a cell. It exercises the decoded-event boundary (onInput → the
// kernel's __eventValue decoder) entirely through sigil library code.
const echoApp = `import "std/reactive" (cell)
import "std/ui" (card, column, label, input)
import "std/html" (mount)

pub let app =
  let (name, setName) = cell ""
  let view =
    card [
      column [
        input setName,
        label (fun () -> "hello, ${name ()}")
      ]
    ]
  mount view "#app"`

// TestComponentAppCompiles confirms the stdlib UI path type-checks and links —
// the thesis at the build level (UI as library code), runnable without Chrome.
func TestComponentAppCompiles(t *testing.T) {
	js, _ := buildAgainstStd(t, counterApp)
	for _, want := range []string{"__elem", "__text", "__on", "__mount", "__cell"} {
		if !strings.Contains(js, want) {
			t.Errorf("bundle missing %q — stdlib component did not lower to the host intrinsic", want)
		}
	}
}

// TestComponentAppInBrowser is the milestone the kernel redesign aims at: a
// counter whose components are sigil library code renders and reacts in a real
// browser. Skips if Chrome is unavailable.
func TestComponentAppInBrowser(t *testing.T) {
	js, _ := buildAgainstStd(t, counterApp)
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

	var initial, afterOne, afterTwo string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("button", chromedp.ByQuery),
		chromedp.Text("#app", &initial, chromedp.ByID),
		chromedp.Click("button", chromedp.ByQuery),
		chromedp.Text("#app", &afterOne, chromedp.ByID),
		chromedp.Click("button", chromedp.ByQuery),
		chromedp.Text("#app", &afterTwo, chromedp.ByID),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}

	if !strings.Contains(initial, "count: 0") {
		t.Errorf("initial render: %q does not contain %q", initial, "count: 0")
	}
	if !strings.Contains(afterOne, "count: 1") {
		t.Errorf("after one click: %q does not contain %q", afterOne, "count: 1")
	}
	if !strings.Contains(afterTwo, "count: 2") {
		t.Errorf("after two clicks: %q does not contain %q", afterTwo, "count: 2")
	}
}

// TestEchoCompiles confirms the Echo (input → cell → label) type-checks and links
// — the decoded-event boundary at the build level, runnable without Chrome.
func TestEchoCompiles(t *testing.T) {
	js, _ := buildAgainstStd(t, echoApp)
	for _, want := range []string{"__on", "__eventValue", "addEventListener"} {
		if !strings.Contains(js, want) {
			t.Errorf("bundle missing %q — the event path did not lower correctly", want)
		}
	}
}

// TestEchoInBrowser drives the Echo in a real browser: typing into the field
// updates the cell, which reactively re-renders the label.
func TestEchoInBrowser(t *testing.T) {
	js, _ := buildAgainstStd(t, echoApp)
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

	var initial, afterType string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("input", chromedp.ByQuery),
		chromedp.Text("#app", &initial, chromedp.ByID),
		chromedp.SendKeys("input", "world", chromedp.ByQuery),
		chromedp.Text("#app", &afterType, chromedp.ByID),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}

	// chromedp.Text trims trailing whitespace, so the empty-cell label reads
	// "hello," (the space after the comma is collapsed away).
	if !strings.Contains(initial, "hello,") {
		t.Errorf("initial render: %q does not contain %q", initial, "hello,")
	}
	if !strings.Contains(afterType, "hello, world") {
		t.Errorf("after typing: %q does not contain %q", afterType, "hello, world")
	}
}
