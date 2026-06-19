// Command serve compiles a Mako entry module against the standard library and
// serves it as a single, self-contained, npm-free page.
//
//	go run ./core/cmd/serve [-root DIR] [-port N] ENTRY.mako
//
// -root is the module root that holds std/ (default "."). The bundle is rebuilt
// on every request, so editing the source and refreshing is the dev loop.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/incantery/mako/core/load"
)

const shell = `<!doctype html>
<html>
  <head><meta charset="utf-8"><title>%s</title></head>
  <body>
    <div id="app"></div>
    <script>%s</script>
  </body>
</html>`

func main() {
	root := flag.String("root", ".", "module root directory (where std/ lives)")
	port := flag.String("port", "8099", "port to serve on")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: serve [-root DIR] [-port N] ENTRY.mako")
		os.Exit(2)
	}
	entry := flag.Arg(0)

	// Build once at startup to fail fast on errors.
	if _, err := build(entry, *root); err != nil {
		log.Fatalf("build error: %v", err)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		js, err := build(entry, *root)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "build error: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, shell, entry, js)
	})

	addr := ":" + *port
	log.Printf("serving %s on http://localhost%s", entry, addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func build(entry, root string) (string, error) {
	prog, err := load.Load(entry, load.Options{Root: root})
	if err != nil {
		return "", err
	}
	return prog.Bundle()
}
