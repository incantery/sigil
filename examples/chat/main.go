// Mock streaming backend + page server for the Sigil chat demo —
// running entirely on the `sigil gen` seam:
//
//   - the contract (stream Chat, stream Broken) lives in chat.mako
//   - sigil.gen.yaml (repo root) generates examples/chat/gen — the
//     typed service interface, per-channel stream emitters, and the
//     HTTP handlers owning framing + error mapping
//   - this file only implements the interface and mounts the two
//     generated handlers; there is no hand-written wire code left
//
// Start it with `go run ./examples/chat`, then open
// http://localhost:8090. Pass -dev for the edit-save-refresh loop
// (recompile per request); the default serves boot-compiled bytes
// with the compiler-derived CSP.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	chatapi "github.com/incantery/mako/examples/chat/gen"
	"github.com/incantery/mako/pkg/serve"
)

// api implements the generated chatapi.Api interface — the mock
// "model": it drips a canned thinking channel, then the answer.
type api struct{}

func (api) Chat(ctx context.Context, req chatapi.ChatRequest, stream *chatapi.ChatStream) error {
	prompt := req.Prompt
	if prompt == "" {
		prompt = "(nothing)"
	}
	thinking := "Let me think about \"" + prompt + "\"… weighing the question carefully."
	answer := "You asked: " + prompt + ". Here is the streamed answer from a mock Sigil backend."

	if err := drip(ctx, thinking, stream.Thinking); err != nil {
		return err
	}
	return drip(ctx, answer, stream.Answer)
}

// Broken always fails before its first delta — the regression
// surface for the stream error path: the generated handler maps it
// to a 502, which the client surfaces via Broken.failed /
// Broken.error instead of losing it in an un-awaited rejection.
func (api) Broken(ctx context.Context, req chatapi.BrokenRequest, stream *chatapi.BrokenStream) error {
	return errors.New("model backend exploded")
}

// CheckHealth returns a discriminated union: the generated handler
// marshals the variant the server builds as `{tag, value}`, and the
// client `match`es it. Here we always report healthy with a Stats
// payload — swap in NewHealthDown("…") or NewHealthUnknown() and the
// client renders the matching arm with no frontend change.
func (api) CheckHealth(ctx context.Context, req chatapi.CheckHealthRequest) (chatapi.Health, error) {
	return chatapi.NewHealthOk(chatapi.Stats{Uptime: 8675, Region: "iad"}), nil
}

// drip emits a sentence one whitespace token at a time, flushing
// each so the browser's ReadableStream reader sees them arrive
// incrementally. Each token after the first carries its leading
// space so the concatenated client-side text matches the original.
func drip(ctx context.Context, sentence string, send func(string) error) error {
	for i, tok := range strings.Fields(sentence) {
		if i > 0 {
			tok = " " + tok
		}
		if err := send(tok); err != nil {
			return nil // client went away; nothing to report
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(35 * time.Millisecond):
		}
	}
	return nil
}

func main() {
	var dir, addr string
	var dev bool
	flag.StringVar(&dir, "dir", "examples/chat", "sigil package directory")
	flag.StringVar(&addr, "addr", ":8090", "HTTP listen address")
	flag.BoolVar(&dev, "dev", false, "recompile the page per request")
	flag.Parse()

	var pageOpts []serve.PageOption
	pageOpts = append(pageOpts, serve.WithTitle("Sigil Chat"))
	if dev {
		pageOpts = append(pageOpts, serve.WithDev())
	}
	page, err := chatapi.NewPageHandler(dir, pageOpts...)
	if err != nil {
		log.Fatalf("page: %v", err) // boot-time failure: bad source or contract skew
	}

	ops := chatapi.NewApiHandler(api{})
	mux := http.NewServeMux()
	mux.Handle("/stream/", ops)
	mux.Handle("/query/", ops) // CheckHealth (union return) routes here
	mux.Handle("/", page)

	log.Printf("sigil chat listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
