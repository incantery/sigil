package cli

import (
	"errors"
	"fmt"
	htmlpkg "html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/incantery/sigil/pkg/ir"
	"github.com/incantery/sigil/pkg/lang/diag"
	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/lang/parser"
	"github.com/incantery/sigil/pkg/render/html"
)

var exploreAddr string

var exploreCmd = &cobra.Command{
	Use:   "explore [dir]",
	Short: "Browse every Sigil example in a directory as a live web app with hot reload",
	Long: `Walks a directory for *.sigil files and serves a live explorer at the
given address. The explorer itself is a Sigil view — sidebar of example
names, source pane, rendered pane. Each example is served at
/example/<basename>; source at /source/<basename>.

Hot reload via SSE: edit any .sigil file in the directory and every
visible iframe refreshes. Compile errors render inline as a themed
error page instead of plain-text 500s, so a broken example doesn't
break the explorer.

The point: dogfood the language. The tool we use to inspect examples
is built with what we're inspecting.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return err
		}

		examples, err := collectExamples(absDir)
		if err != nil {
			return err
		}
		if len(examples) == 0 {
			return fmt.Errorf("no *.sigil files found in %s", absDir)
		}

		hub := newReloadHub()
		go watchDir(absDir, hub)

		mux := http.NewServeMux()
		mux.HandleFunc("/_/events", hub.serveSSE)

		mux.HandleFunc("/example/", func(w http.ResponseWriter, r *http.Request) {
			name := strings.TrimPrefix(r.URL.Path, "/example/")
			full, ok := exampleByName(absDir, name)
			if !ok {
				http.NotFound(w, r)
				return
			}
			doc, err := compileFile(full)
			if err != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				writeErrorPage(w, full, err)
				return
			}
			title := "Sigil"
			if doc.Name != "" {
				title = "Sigil — " + doc.Name
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			writeWithHotReload(w, title, doc)
		})

		mux.HandleFunc("/source/", func(w http.ResponseWriter, r *http.Request) {
			name := strings.TrimPrefix(r.URL.Path, "/source/")
			full, ok := exampleByName(absDir, name)
			if !ok {
				http.NotFound(w, r)
				return
			}
			src, err := os.ReadFile(full)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			writeSourcePage(w, name+".sigil", string(src))
		})

		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			examples, err := collectExamples(absDir)
			if err != nil || len(examples) == 0 {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, "sigil: %v\n", err)
				return
			}
			src := buildExplorerSource(examples)
			root, err := parser.Parse(src)
			if err != nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, "sigil: explorer parse: %v\n", err)
				return
			}
			doc, err := lower.Lower(root)
			if err != nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, "sigil: explorer lower: %v\n", err)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			writeWithHotReload(w, "Sigil — Explorer", doc)
		})

		log.Printf("sigil explore %s — %d example(s); listening on %s",
			absDir, len(examples), exploreAddr)
		log.Printf("  open  http://localhost%s/", strings.TrimPrefix(exploreAddr, "0.0.0.0"))
		return http.ListenAndServe(exploreAddr, mux)
	},
}

type exampleEntry struct {
	name string // basename without .sigil suffix
	path string // absolute path
}

func collectExamples(dir string) ([]exampleEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []exampleEntry{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sigil") {
			continue
		}
		base := strings.TrimSuffix(name, ".sigil")
		out = append(out, exampleEntry{name: base, path: filepath.Join(dir, name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func exampleByName(absDir, name string) (string, bool) {
	// Defensive against path traversal — name comes from URL.
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", false
	}
	full := filepath.Join(absDir, name+".sigil")
	if _, err := os.Stat(full); err != nil {
		return "", false
	}
	return full, true
}

// buildExplorerSource emits the explorer view as Sigil source: sidebar
// of buttons + two iframes (source + rendered) side by side. Each
// sidebar button uses a multi-statement handler to update both URL
// cells at once — finally a real use case for the `;` separator
// introduced in L8.
func buildExplorerSource(examples []exampleEntry) string {
	first := examples[0]
	var b strings.Builder
	b.WriteString("view Explorer =\n")
	fmt.Fprintf(&b, "  state url = %q\n", "/example/"+first.name)
	fmt.Fprintf(&b, "  state src_url = %q\n", "/source/"+first.name)
	b.WriteString("  card\n")
	b.WriteString("    title \"Sigil examples\"\n")
	b.WriteString("    stack horizontal gap=2\n")
	b.WriteString("      stack vertical gap=1\n")
	for _, e := range examples {
		fmt.Fprintf(&b, "        button %q on click { url = %q; src_url = %q }\n",
			e.name, "/example/"+e.name, "/source/"+e.name)
	}
	b.WriteString("      iframe src=src_url width=440 height=700\n")
	b.WriteString("      iframe src=url height=700\n")
	return b.String()
}

// writeWithHotReload writes the doc via html.WriteDoc then injects the
// SSE-subscriber script before </body>. Buffered so we can do the
// string replace; the doc isn't big enough for streaming to matter.
func writeWithHotReload(w io.Writer, title string, doc ir.Document) {
	var buf strings.Builder
	if err := html.WriteDoc(&buf, title, doc); err != nil {
		fmt.Fprintf(&buf, "<!-- sigil write: %v -->", err)
	}
	out := strings.Replace(buf.String(), "</body>", hotReloadScript+"</body>", 1)
	_, _ = io.WriteString(w, out)
}

const hotReloadScript = `<script>
(function(){
  // Dev-only hot reload. Connect to /_/events SSE and reload on any
  // "reload" message. Reconnect on error so the runtime survives
  // a server restart.
  function connect() {
    var es = new EventSource("/_/events");
    es.onmessage = function(e) {
      if (e.data === "reload") window.location.reload();
    };
    es.onerror = function() {
      es.close();
      setTimeout(connect, 500);
    };
  }
  connect();
})();
</script>
`

// writeSourcePage emits the raw .sigil source as a minimal themed page.
// We don't reuse the full Sigil pipeline — these pages have no state,
// no primitives, no runtime — just <pre> with a small stylesheet that
// matches the explorer's light/dark theme via the same media queries.
func writeSourcePage(w io.Writer, title, source string) {
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>%s</title>
  <style>%s</style>
</head>
<body>
  <section style="padding:var(--space-md);background:var(--color-surface-bg);color:var(--color-surface-fg);font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:13px;line-height:1.5">
    <div style="color:var(--color-muted);margin-bottom:var(--space-sm)">%s</div>
    <pre style="margin:0;white-space:pre-wrap">%s</pre>
  </section>
  %s
</body>
</html>`,
		htmlpkg.EscapeString(title),
		miniThemeCSS(),
		htmlpkg.EscapeString(title),
		htmlpkg.EscapeString(source),
		hotReloadScript,
	)
}

// writeErrorPage renders a compile failure as a styled themed page
// inside the iframe slot. Beats plain-text 500s — broken examples
// don't visually break the surrounding explorer chrome.
func writeErrorPage(w io.Writer, path string, err error) {
	msg := err.Error()
	var multi *diag.MultiError
	if errors.As(err, &multi) {
		var b strings.Builder
		for _, d := range multi.Items {
			fmt.Fprintf(&b, "%s\n", d.Error())
		}
		msg = b.String()
	}
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Sigil — error</title>
  <style>%s</style>
</head>
<body>
  <section style="padding:var(--space-md);background:var(--color-danger-bg);color:var(--color-danger-fg);border-radius:var(--radius-md);font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:13px">
    <strong>Compile error</strong>
    <div style="margin-top:var(--space-sm);font-size:12px;opacity:0.85">%s</div>
    <pre style="margin-top:var(--space-md);white-space:pre-wrap">%s</pre>
  </section>
  %s
</body>
</html>`,
		miniThemeCSS(),
		htmlpkg.EscapeString(path),
		htmlpkg.EscapeString(msg),
		hotReloadScript,
	)
}

// miniThemeCSS is the subset of theme tokens the static (non-Sigil)
// auxiliary pages need: surface + muted + danger pairs, sm/md spacing,
// md radius, plus prefers-color-scheme dark coverage. Keeps these pages
// visually consistent with the surrounding explorer chrome without
// pulling in the full pkg/theme CSS generator.
func miniThemeCSS() string {
	return `
*, *::before, *::after { box-sizing: border-box; }
:root {
  --color-surface-bg: #ffffff;
  --color-surface-fg: #18181b;
  --color-muted: #71717a;
  --color-outline: #d4d4d8;
  --color-danger-bg: #dc2626;
  --color-danger-fg: #ffffff;
  --space-sm: 8px;
  --space-md: 16px;
  --radius-md: 6px;
}
@media (prefers-color-scheme: dark) {
  :root {
    --color-surface-bg: #18181b;
    --color-surface-fg: #fafafa;
    --color-muted: #a1a1aa;
    --color-outline: #404040;
  }
}
body { margin:0; padding:var(--space-md); background:var(--color-surface-bg); color:var(--color-surface-fg); font:14px system-ui,-apple-system,sans-serif; }
`
}

// --- SSE reload hub ---

// reloadHub is a goroutine-safe set of subscribers. Each browser tab
// holds one channel. broadcast() pushes "reload" to all of them
// non-blockingly — a stuck client doesn't hold up other tabs.
type reloadHub struct {
	mu          sync.Mutex
	subscribers map[chan string]struct{}
}

func newReloadHub() *reloadHub {
	return &reloadHub{subscribers: map[chan string]struct{}{}}
}

func (h *reloadHub) subscribe() chan string {
	ch := make(chan string, 4)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *reloadHub) unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.subscribers, ch)
	close(ch)
	h.mu.Unlock()
}

func (h *reloadHub) broadcast(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subscribers {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (h *reloadHub) serveSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE requires flushable writer", http.StatusInternalServerError)
		return
	}
	ch := h.subscribe()
	defer h.unsubscribe(ch)

	fmt.Fprintf(w, "data: hello\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// watchDir runs fsnotify over dir and pushes "reload" on every *.sigil
// change. 100ms debounce coalesces editor save bursts (rename+create+
// write) into one broadcast.
func watchDir(dir string, hub *reloadHub) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("explore: fsnotify init failed: %v (hot reload disabled)", err)
		return
	}
	defer w.Close()
	if err := w.Add(dir); err != nil {
		log.Printf("explore: cannot watch %s: %v", dir, err)
		return
	}
	var debounce *time.Timer
	for {
		select {
		case e, ok := <-w.Events:
			if !ok {
				return
			}
			if !strings.HasSuffix(e.Name, ".sigil") {
				continue
			}
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(100*time.Millisecond, func() {
				hub.broadcast("reload")
			})
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Printf("explore: watcher error: %v", err)
		}
	}
}

func init() {
	exploreCmd.Flags().StringVarP(&exploreAddr, "addr", "a", ":8080",
		"HTTP listen address")
}
