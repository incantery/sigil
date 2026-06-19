// Command gauntlet-server serves the automation gauntlet's reference
// pages — plain, Sigil-free frontends that each exhibit one browser-
// automation hazard. Sigil scenarios drive these pages to prove the
// automation suite works against any frontend, not just Sigil-compiled
// apps.
//
// Pages live under pages/<challenge>/<variant>.html and are embedded so
// the binary is self-contained. Routes:
//
//	GET /                          index of challenges (for humans)
//	GET /c/<challenge>/<variant>   the reference page
//
// Run with `go run ./gauntlet/server` (default :7373, override with
// -addr). The page set is intentionally static: no backend, no state —
// the hazards live entirely in each page's own client JS.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strings"
)

//go:embed pages
var pagesFS embed.FS

func main() {
	addr := flag.String("addr", ":7373", "listen address")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/c/", servePage)
	mux.HandleFunc("/", serveIndex)

	log.Printf("gauntlet server on %s — pages: %s", *addr, strings.Join(listPages(), ", "))
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

// servePage maps /c/<challenge>/<variant> to the embedded
// pages/<challenge>/<variant>.html. A missing page is a 404 — challenges
// declare their own variants and we don't synthesize fallbacks.
func servePage(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/c/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "want /c/<challenge>/<variant>", http.StatusBadRequest)
		return
	}
	data, err := pagesFS.ReadFile(fmt.Sprintf("pages/%s/%s.html", parts[0], parts[1]))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// No-cache: the runner reloads pages between scenarios and a stale
	// cached page is the classic source of phantom pass/fail.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// serveIndex lists the available challenge pages as plain links so a
// human can click through them while authoring.
func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, "<!doctype html><meta charset=utf-8><title>Sigil gauntlet</title>")
	fmt.Fprint(w, "<h1>Sigil automation gauntlet</h1><ul>")
	for _, p := range listPages() {
		fmt.Fprintf(w, `<li><a href="/c/%s">%s</a></li>`, p, p)
	}
	fmt.Fprint(w, "</ul>")
}

// listPages walks the embedded pages tree and returns "<challenge>/<variant>"
// for every .html it finds, sorted for stable output.
func listPages() []string {
	var out []string
	_ = fs.WalkDir(pagesFS, "pages", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(path, "pages/"), ".html")
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out
}
