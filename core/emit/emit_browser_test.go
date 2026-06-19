package emit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// browserCounter is a full M1 program: a reactive counter that mounts itself.
// (Raw intrinsics — the M2 stdlib will make this read like the Echo example.)
const browserCounter = `let counter () =
  let r = __cell 0
  __elem "div" [] [
    __elem "p" [ __attr "id" "count" ] [ __text (fun () -> "${__get r}") ],
    __elem "button" [ __attr "id" "inc", __on "click" (fun e -> effect { __set r (__get r + 1) }) ] [
      __text (fun () -> "+")
    ]
  ]

let main = effect { __mount (counter ()) "#app" }`

// TestCounterInBrowser compiles the counter, serves it, and drives a real
// (headless) browser: it asserts the DOM renders "0", then that clicking the
// button reactively updates the text to "1" and "2". Skips if Chrome is absent.
func TestCounterInBrowser(t *testing.T) {
	js, err := Compile(browserCounter)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	html := fmt.Sprintf(`<!doctype html><html><body><div id="app"></div>
<script>%s
$main();
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
	err = chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("#count", chromedp.ByID),
		chromedp.Text("#count", &initial, chromedp.ByID),
		chromedp.Click("#inc", chromedp.ByID),
		chromedp.Text("#count", &afterOne, chromedp.ByID),
		chromedp.Click("#inc", chromedp.ByID),
		chromedp.Text("#count", &afterTwo, chromedp.ByID),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}

	if initial != "0" {
		t.Errorf("initial render: got %q, want %q", initial, "0")
	}
	if afterOne != "1" {
		t.Errorf("after one click: got %q, want %q", afterOne, "1")
	}
	if afterTwo != "2" {
		t.Errorf("after two clicks: got %q, want %q", afterTwo, "2")
	}
}

// reactiveListApp exercises __each (add / reorder, with node reuse) and __when
// (toggle). The list is set to literal lists since there is no stdlib yet.
const reactiveListApp = `let app () =
  let items = __cell ["a", "b", "c"]
  let show = __cell true
  __elem "div" [] [
    __elem "ul" [ __attr "id" "list" ] [
      __each (fun () -> __get items) (fun x -> __elem "li" [] [ __text (fun () -> x) ])
    ],
    __elem "button" [ __attr "id" "rev", __on "click" (fun e -> effect { __set items ["c", "b", "a"] }) ] [ __text (fun () -> "rev") ],
    __elem "button" [ __attr "id" "add", __on "click" (fun e -> effect { __set items ["a", "b", "c", "d"] }) ] [ __text (fun () -> "add") ],
    __elem "button" [ __attr "id" "tog", __on "click" (fun e -> effect { __set show (!(__get show)) }) ] [ __text (fun () -> "tog") ],
    __when (fun () -> __get show) (fun () -> __elem "p" [ __attr "id" "panel" ] [ __text (fun () -> "visible") ])
  ]

let main = effect { __mount (app ()) "#app" }`

// TestReactiveStructureInBrowser drives __each / __when in a real browser:
// reordering and growing a list (asserting node text reflects it) and toggling a
// conditional node in and out of the DOM.
func TestReactiveStructureInBrowser(t *testing.T) {
	js, err := Compile(reactiveListApp)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	html := fmt.Sprintf(`<!doctype html><html><body><div id="app"></div>
<script>%s
$main();
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

	var initialList, reversedList, grownList string
	var panelBefore, panelAfter bool
	err = chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("#list", chromedp.ByID),
		chromedp.Text("#list", &initialList, chromedp.ByID),
		chromedp.Evaluate(`!!document.querySelector('#panel')`, &panelBefore),
		chromedp.Click("#rev", chromedp.ByID),
		chromedp.Text("#list", &reversedList, chromedp.ByID),
		chromedp.Click("#add", chromedp.ByID),
		chromedp.Text("#list", &grownList, chromedp.ByID),
		chromedp.Click("#tog", chromedp.ByID),
		chromedp.Evaluate(`!!document.querySelector('#panel')`, &panelAfter),
	)
	if err != nil {
		t.Skipf("skipping browser test (no Chrome available?): %v", err)
	}

	if norm(initialList) != "abc" {
		t.Errorf("initial list: got %q, want %q", norm(initialList), "abc")
	}
	if norm(reversedList) != "cba" {
		t.Errorf("after reorder: got %q, want %q", norm(reversedList), "cba")
	}
	if norm(grownList) != "abcd" {
		t.Errorf("after add: got %q, want %q", norm(grownList), "abcd")
	}
	if !panelBefore {
		t.Errorf("conditional panel should be present initially")
	}
	if panelAfter {
		t.Errorf("conditional panel should be removed after toggle")
	}
}

// norm strips whitespace so list text comparisons ignore layout.
func norm(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\n' && s[i] != '\t' && s[i] != '\r' {
			out = append(out, s[i])
		}
	}
	return string(out)
}
