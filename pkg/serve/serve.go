// Package serve is the runtime half of `sigil gen`'s server story:
// the generated Go package's NewPageHandler delegates here. Page()
// compiles a sigil package once at construction — boot is the
// failure point, not the first request — and serves the cached page
// bytes with compiler-derived security headers.
//
// Because the page's inline <script> and <style> are known
// byte-exactly at compile time, the Content-Security-Policy can be
// hash-based: the policy admits exactly this page's inline code and
// nothing else, with no nonce plumbing and no per-request work.
package serve

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/incantery/sigil/pkg/contract"
	"github.com/incantery/sigil/pkg/ir"
	"github.com/incantery/sigil/pkg/lang/loader"
	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/render/html"
)

// responseKey is the private context key under which generated op
// handlers stash the per-request response controls.
type responseKey struct{}

// ResponseControls is an op implementation's handle on the live HTTP
// request and response. A generated op handler places one on the
// request context before calling the impl; ResponseFrom retrieves
// it. It lets an impl read request metadata (headers, cookies) and
// write response metadata (headers, Set-Cookie) without the method
// signature carrying the http.ResponseWriter — symmetric with how the
// authenticated user already flows in on the context.
//
// It is a general request/response-metadata seam, not an auth
// feature: cookies are the first consumer, but rate-limit headers,
// ETag/Cache-Control, request-id propagation, and Origin/Sec-Fetch
// reads (a cheap CSRF check on POST ops) all use the same handle.
//
// For streams the write side is usable only BEFORE the first delta:
// response headers — Set-Cookie included — commit when the stream
// flushes its first chunk. Commands and queries write their body
// after the impl returns, so they have no such constraint; a stream
// that needs to set a cookie must do so before its first send.
type ResponseControls struct {
	w http.ResponseWriter
	r *http.Request
}

// SetCookie appends a Set-Cookie header to the response. The impl
// owns every cookie attribute (HttpOnly, SameSite, Secure, Path,
// MaxAge); the seam bakes in no policy.
func (rc *ResponseControls) SetCookie(c *http.Cookie) { http.SetCookie(rc.w, c) }

// SetHeader sets a response header, replacing any existing values.
func (rc *ResponseControls) SetHeader(key, value string) { rc.w.Header().Set(key, value) }

// AddHeader appends a value to a response header without clobbering
// existing ones.
func (rc *ResponseControls) AddHeader(key, value string) { rc.w.Header().Add(key, value) }

// RequestHeader returns the first value of the named request header,
// or "" when absent.
func (rc *ResponseControls) RequestHeader(key string) string { return rc.r.Header.Get(key) }

// Cookie returns the named request cookie, or http.ErrNoCookie.
func (rc *ResponseControls) Cookie(name string) (*http.Cookie, error) { return rc.r.Cookie(name) }

// WithResponse returns ctx carrying per-request response controls
// over w and r. Generated op handlers call this before invoking the
// impl; tests call it to exercise an impl that uses ResponseFrom
// without standing up a server.
func WithResponse(ctx context.Context, w http.ResponseWriter, r *http.Request) context.Context {
	return context.WithValue(ctx, responseKey{}, &ResponseControls{w: w, r: r})
}

// ResponseFrom returns the response controls a generated op handler
// placed on ctx. Inside a generated command/query/stream impl it is
// always non-nil; it returns nil only when called from a context no
// generated handler produced.
func ResponseFrom(ctx context.Context) *ResponseControls {
	rc, _ := ctx.Value(responseKey{}).(*ResponseControls)
	return rc
}

type pageConfig struct {
	title        string
	dev          bool
	csp          bool
	expectedHash string
}

// PageOption configures Page.
type PageOption func(*pageConfig)

// WithTitle overrides the page <title> (default: the view's name).
func WithTitle(t string) PageOption { return func(c *pageConfig) { c.title = t } }

// WithDev recompiles the package on every request — the
// edit-save-refresh dev loop. The contract check still runs per
// request but reports as a 500 page instead of failing boot.
func WithDev() PageOption { return func(c *pageConfig) { c.dev = true } }

// WithoutCSP disables the Content-Security-Policy header (the other
// security headers stay). Escape hatch for pages embedded behind
// proxies that inject their own inline code.
func WithoutCSP() PageOption { return func(c *pageConfig) { c.csp = false } }

// ExpectContract makes Page verify, at boot, that the compiled
// package's contract hash equals the given one. Generated servers
// pass their stamped ContractHash so a server generated against one
// contract can never serve a client bundle compiled from another —
// the version-skew failure mode of pinning the sigil CLI and the
// sigil library separately.
func ExpectContract(hash string) PageOption {
	return func(c *pageConfig) { c.expectedHash = hash }
}

// Page compiles the sigil package at dir and returns a handler
// serving the rendered page at "/" (GET/HEAD).
func Page(dir string, opts ...PageOption) (http.Handler, error) {
	cfg := pageConfig{csp: true}
	for _, o := range opts {
		o(&cfg)
	}

	build := func() (*html.ComposedPage, string, bool, error) {
		doc, err := compile(dir)
		if err != nil {
			return nil, "", false, err
		}
		if cfg.expectedHash != "" {
			got := contract.FromDoc(doc).Hash()
			if got != cfg.expectedHash {
				return nil, "", false, fmt.Errorf(
					"sigil: contract hash mismatch: generated server expects %s, package %s compiles to %s — regenerate with `sigil gen` (run the same pinned sigil version that builds this binary)",
					cfg.expectedHash, dir, got)
			}
		}
		title := cfg.title
		if title == "" {
			title = "Sigil"
			if doc.Name != "" {
				title = doc.Name
			}
		}
		page, err := html.ComposeDoc(title, doc)
		if err != nil {
			return nil, "", false, err
		}
		// A path-driven router resolves routes client-side, so every path
		// must serve the same shell (the browser does the rest). Detect it
		// here and switch the gate to catch-all rather than "/"-only.
		return page, cspFor(doc, page, cfg.csp), docHasPathRouter(doc), nil
	}

	if cfg.dev {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page, csp, catchAll, err := build()
			if err != nil {
				http.Error(w, fmt.Sprintf("sigil: %v", err), http.StatusInternalServerError)
				return
			}
			if !pageRoute(w, r, catchAll) {
				return
			}
			writePage(w, page.Bytes, csp)
		}), nil
	}

	page, csp, catchAll, err := build()
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !pageRoute(w, r, catchAll) {
			return
		}
		writePage(w, page.Bytes, csp)
	}), nil
}

// docHasPathRouter reports whether the document contains a path-driven
// router — a KindRouter with no `active` cell binding (its routes match on
// location.pathname). Such a page must be served on every path.
func docHasPathRouter(doc ir.Document) bool {
	var walk func(n ir.Node) bool
	walk = func(n ir.Node) bool {
		if n.Kind == ir.KindRouter {
			if _, cell := n.Bindings["active"]; !cell {
				return true
			}
		}
		for _, c := range n.Children {
			if walk(c) {
				return true
			}
		}
		return false
	}
	return walk(doc.Root)
}

func compile(dir string) (ir.Document, error) {
	prog, err := loader.Load(dir)
	if err != nil {
		return ir.Document{}, err
	}
	merged, err := prog.Merge()
	if err != nil {
		return ir.Document{}, err
	}
	return lower.Lower(merged)
}

// pageRoute gates the handler to GET/HEAD. Unless catchAll is set (a
// path-driven router resolves routes client-side, so any path serves the
// shell) the path must be exactly "/". Returns false after writing the
// error response.
func pageRoute(w http.ResponseWriter, r *http.Request, catchAll bool) bool {
	if !catchAll && r.URL.Path != "/" {
		http.NotFound(w, r)
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func writePage(w http.ResponseWriter, body []byte, csp string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if csp != "" {
		h.Set("Content-Security-Policy", csp)
	}
	w.Write(body)
}

// cspFor derives the page's Content-Security-Policy from what the
// compiler knows: the exact inline segments (hashed), whether web
// fonts load from Google, and which cross-origin backends ops call.
func cspFor(doc ir.Document, page *html.ComposedPage, enabled bool) string {
	if !enabled {
		return ""
	}
	scriptHash := cspHash(page.InlineScript)
	styleHash := cspHash(page.InlineStyle)

	styleSrc := []string{styleHash}
	fontSrc := []string{}
	for _, f := range doc.Fonts {
		if f.Provider == "google" {
			styleSrc = append(styleSrc, "https://fonts.googleapis.com")
			fontSrc = append(fontSrc, "https://fonts.gstatic.com")
			break
		}
	}

	connectSrc := []string{"'self'"}
	for _, b := range doc.Backends {
		if origin := absOrigin(b.URL); origin != "" {
			connectSrc = append(connectSrc, origin)
		}
	}

	directives := []string{
		"default-src 'none'",
		"script-src " + scriptHash,
		"style-src " + strings.Join(styleSrc, " "),
		"connect-src " + strings.Join(connectSrc, " "),
		"img-src 'self' data:",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
	}
	if len(fontSrc) > 0 {
		directives = append(directives, "font-src "+strings.Join(fontSrc, " "))
	}
	// The iframe primitive needs frame-src, or default-src 'none'
	// blocks it. Collect static iframe origins from the tree; a bound
	// or relative src resolves to 'self' (origin unknown at compile
	// time). Only emit the directive when the page actually frames
	// something — pages without iframes keep the tighter default.
	if fs := frameSources(doc.Root); len(fs) > 0 {
		directives = append(directives, "frame-src "+strings.Join(fs, " "))
	}
	return strings.Join(directives, "; ")
}

// frameSources walks the IR tree and returns the distinct origins
// the page's iframes load, in stable order ('self' first). Empty
// when the page frames nothing.
func frameSources(root ir.Node) []string {
	seen := map[string]bool{}
	var ordered []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			ordered = append(ordered, s)
		}
	}
	var walk func(n ir.Node)
	walk = func(n ir.Node) {
		if n.Kind == ir.KindIFrame {
			if _, bound := n.Bindings["src"]; bound {
				add("'self'") // dynamic src: origin not known until runtime
			} else if s, _ := n.Props["src"].(string); s != "" {
				if origin := absOrigin(s); origin != "" {
					add(origin)
				} else {
					add("'self'") // relative src
				}
			} else {
				add("'self'")
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	// 'self' first for readability.
	if seen["'self'"] {
		rest := ordered[:0:0]
		for _, s := range ordered {
			if s != "'self'" {
				rest = append(rest, s)
			}
		}
		return append([]string{"'self'"}, rest...)
	}
	return ordered
}

func cspHash(segment string) string {
	sum := sha256.Sum256([]byte(segment))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// absOrigin returns scheme://host for absolute http(s) URLs, ""
// otherwise (same-origin and relative backends are covered by
// 'self').
func absOrigin(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
