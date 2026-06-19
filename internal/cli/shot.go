package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"github.com/spf13/cobra"

	"github.com/incantery/mako/pkg/render/html"
)

var (
	shotOut      string
	shotURL      string
	shotWidth    int
	shotHeight   int
	shotWaitMS   int
	shotFullPage bool
	shotTimeout  time.Duration
	shotClicks   []string
	shotThenWait int
	shotTheme    string
)

var shotCmd = &cobra.Command{
	Use:   "shot [file.mako]",
	Short: "Render a Sigil file in headless Chromium and save a PNG",
	Long: `Compiles a .mako source file (or hits a running URL), renders it
in a headless Chromium tab, and writes a PNG of the page. The
fastest available loop for "did my layout change do what I wanted":

  sigil shot examples/sigil/hello.mako --out /tmp/hello.png

For app-target demos that depend on a running backend, point at the
URL directly instead of compiling the .mako here — the file's view
references queries / commands that only resolve against the server:

  sigil shot --url http://localhost:8080 --out /tmp/pokedex.png

The default viewport (1280x800) approximates a typical desktop;
--full-page captures the entire scrollable height instead.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if shotURL == "" && len(args) == 0 {
			return fmt.Errorf("provide a .mako file path or --url")
		}
		if shotURL != "" && len(args) > 0 {
			return fmt.Errorf("pass either a file path OR --url, not both")
		}

		// Resolve URL — either an ephemeral server we spin up around the
		// compiled doc, or the caller-supplied --url.
		url, cleanup, err := resolveShotURL(args)
		if err != nil {
			return err
		}
		defer cleanup()

		ctx, cancel := context.WithTimeout(cmd.Context(), shotTimeout)
		defer cancel()

		allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("hide-scrollbars", true),
			chromedp.WindowSize(shotWidth, shotHeight),
		)
		allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
		defer cancelAlloc()
		bctx, cancelB := chromedp.NewContext(allocCtx)
		defer cancelB()

		var buf []byte
		actions := []chromedp.Action{
			chromedp.EmulateViewport(int64(shotWidth), int64(shotHeight)),
		}
		// Theme override (light / dark). Sigil's rendered page ships
		// both palettes gated on `prefers-color-scheme` — overriding
		// the emulated media feature flips the page without changing
		// the rendered HTML. "auto" leaves the OS-default in place.
		if shotTheme != "" && shotTheme != "auto" {
			if shotTheme != "light" && shotTheme != "dark" {
				return fmt.Errorf("--theme must be light, dark, or auto (got %q)", shotTheme)
			}
			actions = append(actions, emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
				{Name: "prefers-color-scheme", Value: shotTheme},
			}))
		}
		actions = append(actions,
			chromedp.Navigate(url),
			chromedp.Sleep(time.Duration(shotWaitMS)*time.Millisecond),
		)
		// Drive interactive state before the capture. Each --click
		// names a button by visible label; the action looks the button
		// up by direct-text match (same rule as the e2e bundle's
		// __findButton) and triggers a click via JS. A per-click
		// settle wait gives any fired async ops a chance to land.
		for _, label := range shotClicks {
			click := label
			actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
				return clickButtonByLabel(ctx, click)
			}))
			actions = append(actions, chromedp.Sleep(time.Duration(shotThenWait)*time.Millisecond))
		}
		if shotFullPage {
			actions = append(actions, chromedp.FullScreenshot(&buf, 90))
		} else {
			actions = append(actions, chromedp.CaptureScreenshot(&buf))
		}
		if err := chromedp.Run(bctx, actions...); err != nil {
			return fmt.Errorf("capture: %w", err)
		}
		if err := os.WriteFile(shotOut, buf, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", shotOut, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes)\n", shotOut, len(buf))
		return nil
	},
}

// clickButtonByLabel finds a <button> whose direct text content
// matches `label` (after trim) and clicks it. Uses the same matching
// rule as the e2e bundle's __findButton so authoring expectations
// stay consistent between `sigil test` and `sigil shot --click`.
// Errors when no match is found, including the page's current button
// labels in the diagnostic so the author can correct the typo
// without re-opening the browser.
func clickButtonByLabel(ctx context.Context, label string) error {
	js := fmt.Sprintf(`(() => {
  const want = %q;
  const buttons = document.querySelectorAll("button");
  for (const btn of buttons) {
    let direct = "";
    for (const n of btn.childNodes) {
      if (n.nodeType === 3) direct += n.textContent;
    }
    if (direct.trim() === want) { btn.click(); return ""; }
  }
  const labels = Array.from(buttons).map(b => {
    let d = "";
    for (const n of b.childNodes) if (n.nodeType === 3) d += n.textContent;
    return d.trim();
  });
  return "no button matches " + JSON.stringify(want) +
         "; visible buttons: " + JSON.stringify(labels);
})()`, label)
	var failure string
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &failure)); err != nil {
		return fmt.Errorf("click %q: %w", label, err)
	}
	if failure != "" {
		return fmt.Errorf("click: %s", failure)
	}
	return nil
}

// resolveShotURL returns the URL chromedp should navigate to and a
// cleanup function the caller defers. For --url, that's the URL
// straight through with a no-op cleanup. For a file path, we
// compile + render the doc, host it on an ephemeral 127.0.0.1
// listener, and return the cleanup that shuts that server down.
func resolveShotURL(args []string) (string, func(), error) {
	if shotURL != "" {
		return shotURL, func() {}, nil
	}
	doc, err := compileFile(args[0])
	if err != nil {
		return "", nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		title := "Sigil"
		if doc.Name != "" {
			title = "Sigil — " + doc.Name
		}
		_ = html.WriteDoc(w, title, doc)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()
	url := "http://" + listener.Addr().String() + "/"
	cleanup := func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}
	return url, cleanup, nil
}

func init() {
	shotCmd.Flags().StringVarP(&shotOut, "out", "o", "sigil-shot.png",
		"output PNG path")
	shotCmd.Flags().StringVar(&shotURL, "url", "",
		"screenshot a running URL instead of compiling+serving a file")
	shotCmd.Flags().IntVar(&shotWidth, "width", 1280,
		"viewport width in CSS pixels")
	shotCmd.Flags().IntVar(&shotHeight, "height", 800,
		"viewport height in CSS pixels")
	shotCmd.Flags().IntVar(&shotWaitMS, "wait", 250,
		"milliseconds to wait after navigation before capturing (lets fonts + layout settle)")
	shotCmd.Flags().BoolVar(&shotFullPage, "full-page", false,
		"capture the entire scrollable page instead of just the viewport")
	shotCmd.Flags().DurationVar(&shotTimeout, "timeout", 30*time.Second,
		"overall timeout for the capture")
	shotCmd.Flags().StringArrayVar(&shotClicks, "click", nil,
		"button label to click before the capture (repeatable; ordered)")
	shotCmd.Flags().IntVar(&shotThenWait, "then-wait", 500,
		"milliseconds to wait after each --click before continuing")
	shotCmd.Flags().StringVar(&shotTheme, "theme", "",
		"force `prefers-color-scheme` to light or dark (default: OS-default)")
}
