package devserver

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"
)

// BuildFunc compiles the dev bundle for an entry under root.
type BuildFunc func(entry, root string) (string, error)

// Server is the sigil dev server: a shell page, the client agent, an SSE hub,
// and a file watcher that rebuilds and broadcasts on change.
type Server struct {
	entry string
	root  string
	build BuildFunc
	hub   *Hub
}

func New(entry, root string, build BuildFunc) *Server {
	return &Server{entry: entry, root: root, build: build, hub: NewHub()}
}

func (s *Server) Hub() *Hub { return s.hub }

// shellTmpl hosts the initial bundle. The agent loads first (sets up
// __sigilDev), then the bundle runs in global scope and registers its cells.
var shellTmpl = template.Must(template.New("shell").Parse(
	`<!doctype html>
<html>
  <head><meta charset="utf-8"><title>{{.Title}} (dev)</title></head>
  <body>
    <div id="app"></div>
    <script src="/__sigil/agent.js"></script>
    <script>{{.Bundle}}</script>
  </body>
</html>`))

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/__sigil/agent.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, AgentJS)
	})
	mux.Handle("/__sigil/events", s.hub)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		js, err := s.build(s.entry, s.root)
		if err != nil {
			// Serve the shell with an empty bundle; the agent shows the overlay
			// once the SSE error arrives. Still emit a readable note inline.
			js = "/* build error: " + template.JSEscapeString(err.Error()) + " */"
		}
		w.Header().Set("Content-Type", "text/html")
		_ = shellTmpl.Execute(w, struct{ Title, Bundle template.JS }{
			Title:  template.JS(s.entry),
			Bundle: template.JS(js),
		})
	})
	return mux
}

// Rebuild compiles the dev bundle and broadcasts the result to all browsers.
func (s *Server) Rebuild() {
	js, err := s.build(s.entry, s.root)
	if err != nil {
		s.hub.Broadcast(ErrorMsg(err.Error()))
		return
	}
	s.hub.Broadcast(ReloadMsg(js))
}

// ListenAndServe starts the watcher and serves until the process exits.
func (s *Server) ListenAndServe(addr string) error {
	stop := Watch(s.root, 150*time.Millisecond, s.Rebuild)
	defer stop()
	log.Printf("dev-serving %s on http://localhost%s", s.entry, addr)
	return http.ListenAndServe(addr, s.Handler())
}
