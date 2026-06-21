package devserver

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/incantery/sigil/internal/load"
)

const counterSrc = `import "std/reactive" (cell)
import "std/ui" (card, column, row, button, label)
import "std/html" (mount)

pub let app =
  let (count, setCount) = cell 0
  let view =
    card [ column [
      label (fun () -> "%LABEL% ${count ()}"),
      row [
        button "-" (fun () -> setCount (count () - 1)),
        button "+" (fun () -> setCount (count () + 1))
      ]
    ] ]
  mount view "#app"
`

// devBuild compiles entry under root with the dev prelude.
func devBuild(entry, root string) (string, error) {
	prog, err := load.Load(entry, load.Options{Root: root})
	if err != nil {
		return "", err
	}
	return prog.BundleDev()
}

func TestHMRPreservesCounterState(t *testing.T) {
	// Build an isolated root: copy std/ and write a mutable entry.
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "std"), filepath.Join(root, "std"))
	entry := filepath.Join(root, "app.sigil")
	writeFile(t, entry, strings.Replace(counterSrc, "%LABEL%", "count:", 1))

	s := New(entry, root, devBuild)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Headless)...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
	defer cancelTimeout()

	var afterInc, afterHMR string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible(`//button[text()="+"]`, chromedp.BySearch),
		chromedp.Click(`//button[text()="+"]`, chromedp.BySearch),
		chromedp.Click(`//button[text()="+"]`, chromedp.BySearch),
		chromedp.Text("#app", &afterInc, chromedp.ByID), // "count: 2"
		// Edit the entry's label text on disk, then trigger a rebuild+broadcast.
		chromedp.ActionFunc(func(context.Context) error {
			writeFile(t, entry, strings.Replace(counterSrc, "%LABEL%", "value:", 1))
			s.Rebuild()
			return nil
		}),
		// Wait for the swapped markup using a polling loop (chromedp.WaitFunc is a
		// QueryOption, not an Action, so we poll manually with a deadline).
		chromedp.ActionFunc(func(ctx context.Context) error {
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				var txt string
				if err := chromedp.Text("#app", &txt, chromedp.ByID).Do(ctx); err == nil {
					if strings.Contains(txt, "value:") {
						afterHMR = txt
						return nil
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			// Capture final state for the assertion message.
			_ = chromedp.Text("#app", &afterHMR, chromedp.ByID).Do(ctx)
			return fmt.Errorf("timed out waiting for 'value:' in #app text (got: %q)", afterHMR)
		}),
	)
	if err != nil {
		if strings.Contains(err.Error(), "exec") {
			t.Skipf("chrome unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if !strings.Contains(afterInc, "count: 2") {
		t.Errorf("before HMR: %q does not contain %q", afterInc, "count: 2")
	}
	// The label text was swapped AND the reactive count (2) was preserved.
	if !strings.Contains(afterHMR, "value: 2") {
		t.Errorf("after HMR: %q does not contain %q (state not preserved)", afterHMR, "value: 2")
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
