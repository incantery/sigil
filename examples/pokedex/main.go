// Pokédex — a small end-to-end demo of the Sigil ops loop.
//
//   pokedex.sigil    — types + queries + commands + view, authored in Sigil
//   api/api.go       — generated from pokedex.sigil via `sigil gen go`
//   this file        — the hand-rolled server: implements the API
//                      interface, mounts the routes, serves the
//                      Sigil-compiled HTML at /
//
// Run from the repo root:
//
//	go run ./examples/pokedex
//
// Then open http://localhost:8080 in a browser. Click a button to
// drive a real HTTP round-trip; the cell value updates from the
// server response.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/incantery/sigil/examples/pokedex/api"
	"github.com/incantery/sigil/pkg/lang/loader"
	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/render/html"
)

// server is a tiny in-memory backend. Real apps replace this with a
// database / domain layer; the API surface is the same shape either
// way because the Sigil-declared contract is the contract.
type server struct {
	mu       sync.Mutex
	teamSize int64
	active   string
}

func (s *server) Ping(_ context.Context, _ api.PingArgs) (bool, error) {
	return true, nil
}

func (s *server) GetTeamSize(_ context.Context, _ api.GetTeamSizeArgs) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.teamSize, nil
}

func (s *server) GetActiveName(_ context.Context, _ api.GetActiveNameArgs) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active, nil
}

// Train mutates state and returns success. The view fires it
// statement-style — the result is discarded but the side effect
// (team size goes up by one) is observable via "fetch team size".
func (s *server) Train(_ context.Context, _ api.TrainArgs) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teamSize++
	log.Printf("trained: team size = %d", s.teamSize)
	return true, nil
}

// CatchPokemon is fired from a for-row handler — `button "catch" on
// click { CatchPokemon(slot.id) }`. The arg comes from the row's
// per-row cell (slot.id) via the L36 row-handler op_call path.
func (s *server) CatchPokemon(_ context.Context, args api.CatchPokemonArgs) (bool, error) {
	log.Printf("caught: slot id = %d", args.Id)
	return true, nil
}

// SetMood exists only to demonstrate L37 validator codegen — sum
// arguments are checked against the closed variant set before this
// method is invoked. Try posting `{"mood":"frire"}` to see a 400
// instead of this handler being called with a garbage value.
func (s *server) SetMood(_ context.Context, args api.SetMoodArgs) (bool, error) {
	log.Printf("set mood: %s", args.Mood)
	return true, nil
}

// GetSlot returns a typed Slot record. The view's `featured = GetSlot(1)`
// triggers L39 record-spread: the JSON response is split into the
// inflated `featured.id`, `featured.name`, `featured.hp` leaf cells.
func (s *server) GetSlot(_ context.Context, args api.GetSlotArgs) (api.Slot, error) {
	return api.Slot{
		Id:   args.Id,
		Name: "Pikachu",
		Hp:   100,
	}, nil
}

func main() {
	var sigilPath, addr string
	flag.StringVar(&sigilPath, "sigil", "examples/pokedex/pokedex.sigil",
		"path to the Sigil source file")
	flag.StringVar(&addr, "addr", ":8080", "HTTP listen address")
	flag.Parse()

	absPath, err := filepath.Abs(sigilPath)
	if err != nil {
		log.Fatalf("resolve sigil path: %v", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		log.Fatalf("sigil file not found: %v (run from the repo root or pass --sigil)", err)
	}

	s := &server{teamSize: 3, active: "Pikachu"}
	mux := http.NewServeMux()
	api.Mount(s, mux)

	// Compile + render on every request so edit-save-refresh works
	// without restarting. Cheap for a demo file this size; production
	// builds (L36+) would cache the compiled bundle.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// L52: compile through the loader so imports resolve. The
		// loader walks up from absPath to find sigil.mod, parses
		// every .sigil file in this package + its transitively
		// imported packages, merges them, and lowers as one program.
		prog, err := loader.Load(absPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		merged, err := prog.Merge()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		doc, err := lower.Lower(merged)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		title := "Pokédex"
		if doc.Name != "" {
			title = "Pokédex — " + doc.Name
		}
		if err := html.WriteDoc(w, title, doc); err != nil {
			log.Printf("write: %v", err)
		}
	})

	log.Printf("pokédex listening on http://localhost%s — open the URL in a browser", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
