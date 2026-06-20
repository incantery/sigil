package codegen

import (
	"strings"
	"testing"

	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/lang/parser"
)

// emitSPAFromSource parses + lowers src and runs the production SPA
// emitter (EmitSPA), unlike emitFromSource which exercises the legacy
// codegen.Emit path that does not handle routers or navigate.
func emitSPAFromSource(t *testing.T, src string) string {
	t.Helper()
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return EmitSPA(doc, map[string]string{})
}

// TestPathRouterEmit locks the path-driven router codegen: a `router`
// with no cell, whose routes carry `path` facets, emits a History-API
// route table (match on location.pathname, pushState navigation, popstate
// back/forward) and NOT the legacy hash machinery.
func TestPathRouterEmit(t *testing.T) {
	src := `view App =
  router
    route "home"
      path "/"
      public
      text "home page"
    route "about"
      path "/about"
      public
      text "about page"
`
	js := emitSPAFromSource(t, src)

	for _, want := range []string{
		`path: "/"`,
		`path: "/about"`,
		`location.pathname`,
		`window.__sigilNav =`,
		`history.pushState`,
		`addEventListener('popstate'`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("path router JS missing %q\n---\n%s", want, js)
		}
	}

	// Path-mode must not fall back to the legacy hash router.
	if strings.Contains(js, "location.hash") {
		t.Errorf("path router should not emit hash routing:\n%s", js)
	}
}

// TestPathRouterParams locks param routing: a `:param` segment becomes a
// read-only cell, the matcher extracts it from the pathname, and the route
// seeds the cell (setp) before mounting so the view binds the right value.
func TestPathRouterParams(t *testing.T) {
	src := `view App =
  router
    route "org"
      path "/org/:orgID"
      public
      params
        orgID String
      view
        text "Org: ${orgID}"
`
	js := emitSPAFromSource(t, src)

	for _, want := range []string{
		`path: "/org/:orgID"`,
		`ps[i][0] === ':'`,        // segment-wise param extraction
		`decodeURIComponent`,      // param values are URL-decoded
		`setp: (m) =>`,            // route seeds its params
		`m["orgID"]`,              // pulls orgID out of the match
	} {
		if !strings.Contains(js, want) {
			t.Errorf("param router JS missing %q\n---\n%s", want, js)
		}
	}
}

// TestGuardGateAndRedirect locks the guard runtime: a guarded route runs
// its op on every navigation (inside the async render), redirects to the
// first public route on a falsy/throwing result, and only mounts on admit.
func TestGuardGateAndRedirect(t *testing.T) {
	src := `backend Api =
  url same-origin
  auth cookie
query GetMe = Bool
view App =
  router
    route "login"
      path "/login"
      public
      text "log in"
    route "home"
      path "/"
      guard GetMe
      text "secret"
`
	js := emitSPAFromSource(t, src)

	for _, want := range []string{
		`guard: async () =>`,                          // each route carries a guard
		`if (!(await window.__sigil_ops.GetMe()))`,    // guard runs the op
		`} catch (e) { return false; }`,               // a throw (401) denies
		`if (!(await __r.guard())) { window.__sigilNav("/login"); return; }`, // deny → redirect to the public route
	} {
		if !strings.Contains(js, want) {
			t.Errorf("guard JS missing %q\n---\n%s", want, js)
		}
	}
}

// TestDefaultDeny locks the forcing function: a path route that declares
// neither `public` nor a `guard` is a compile error, so an agent cannot
// silently expose a route by forgetting to gate it.
func TestDefaultDeny(t *testing.T) {
	src := `view App =
  router
    route "home"
      path "/"
      text "ungated"
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = lower.Lower(root)
	if err == nil {
		t.Fatal("expected a default-deny compile error for an ungated path route")
	}
	if !strings.Contains(err.Error(), "public") || !strings.Contains(err.Error(), "guard") {
		t.Errorf("default-deny error should mention public/guard, got: %v", err)
	}
}

// TestPublicAndGuardConflict locks that a route can't be both public and
// guarded — the access posture must be unambiguous.
func TestPublicAndGuardConflict(t *testing.T) {
	src := `backend Api =
  url same-origin
  auth cookie
query GetMe = Bool
view App =
  router
    route "home"
      path "/"
      public
      guard GetMe
      text "x"
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := lower.Lower(root); err == nil {
		t.Fatal("expected an error for a route that is both public and guarded")
	}
}

// TestAuthLeakCrossCheck locks cross-check (B): a `public` route that
// calls an `auth cookie` op in its subtree is a compile error — you can't
// expose a session operation behind an open route. The same op behind a
// guard compiles.
func TestAuthLeakCrossCheck(t *testing.T) {
	leaky := `backend Api =
  url same-origin
  auth cookie
command Logout = Bool
view App =
  router
    route "home"
      path "/"
      public
      button "out" on click { Logout() }
`
	root, err := parser.Parse(leaky)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = lower.Lower(root)
	if err == nil {
		t.Fatal("expected an auth-leak error for a public route calling an auth-cookie op")
	}
	if !strings.Contains(err.Error(), "Logout") || !strings.Contains(err.Error(), "public") {
		t.Errorf("auth-leak error should name the op and `public`, got: %v", err)
	}

	// Same op, but gated — must compile.
	gated := `backend Api =
  url same-origin
  auth cookie
command Logout = Bool
query GetMe = Bool
view App =
  router
    route "home"
      path "/"
      guard GetMe
      button "out" on click { Logout() }
`
	root2, err := parser.Parse(gated)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := lower.Lower(root2); err != nil {
		t.Fatalf("guarded route calling an auth op should compile, got: %v", err)
	}
}

// TestPublicWithNonAuthOpOK guards against false positives: a public route
// may call an op on a `none`-auth backend.
func TestPublicWithNonAuthOpOK(t *testing.T) {
	src := `backend Api =
  url same-origin
  auth none
command Ping = Bool
view App =
  router
    route "home"
      path "/"
      public
      button "p" on click { Ping() }
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := lower.Lower(root); err != nil {
		t.Fatalf("public route calling a non-auth op should compile, got: %v", err)
	}
}

// TestGroupGuardInheritance locks `group |> guard ...`: every member route
// inherits the group's guard (satisfying default-deny without its own
// public/guard), the routes flatten into the router table, and a denial
// redirects to the public route.
func TestGroupGuardInheritance(t *testing.T) {
	src := `backend Api =
  url same-origin
  auth cookie
query GetMe = Bool
view App =
  router
    route "login"
      path "/login"
      public
      text "log in"
    group |> guard GetMe
      route "home"
        path "/"
        text "home"
      route "settings"
        path "/settings"
        text "settings"
`
	js := emitSPAFromSource(t, src)

	gates := strings.Count(js, "if (!(await window.__sigil_ops.GetMe())) return false;")
	if gates < 2 {
		t.Errorf("both grouped routes should inherit the GetMe guard, found %d gates\n%s", gates, js)
	}
	if !strings.Contains(js, `window.__sigilNav("/login")`) {
		t.Errorf("group-guard denial should redirect to the public /login route\n%s", js)
	}
	if !strings.Contains(js, `path: "/"`) || !strings.Contains(js, `path: "/settings"`) {
		t.Errorf("grouped routes should flatten into the route table\n%s", js)
	}
}

// TestGroupGuardResolvesPerRouteParam locks the deferred-resolution design:
// a group guard naming a `:param` (`memberOf orgID`) resolves against each
// member route's own minted param cell.
func TestGroupGuardResolvesPerRouteParam(t *testing.T) {
	src := `backend Api =
  url same-origin
  auth cookie
query memberOf -> orgID : String = Bool
view App =
  router
    route "login"
      path "/login"
      public
      text "in"
    group |> guard memberOf orgID
      route "dash"
        path "/org/:orgID/dashboard"
        params
          orgID String
        view
          text "dash ${orgID}"
`
	js := emitSPAFromSource(t, src)
	// The guard call passes the orgID param cell (a cN var), not a literal.
	if !strings.Contains(js, "window.__sigil_ops.memberOf(c") {
		t.Errorf("group guard should resolve orgID to its param cell\n%s", js)
	}
}

// TestNavigateRoutesThroughSpaNav locks that the `navigate` action prefers
// the mounted path router (client-side) and only falls back to a full page
// load when none is present.
func TestNavigateRoutesThroughSpaNav(t *testing.T) {
	src := `view App =
  stack
    button "go" on click { navigate "/about" }
`
	js := emitSPAFromSource(t, src)
	if !strings.Contains(js, "window.__sigilNav") {
		t.Errorf("navigate should route through __sigilNav:\n%s", js)
	}
	if !strings.Contains(js, "window.location.assign") {
		t.Errorf("navigate should retain a full-load fallback:\n%s", js)
	}
}

// TestLegacyCellRouterStillEmits guards backward compatibility: a `router`
// bound to a cell keeps emitting the hash-based view-swapper.
func TestLegacyCellRouterStillEmits(t *testing.T) {
	src := `view App =
  state page = "home"
  router page
    route "home"
      text "home page"
    route "about"
      text "about page"
`
	js := emitSPAFromSource(t, src)
	if !strings.Contains(js, "location.hash") {
		t.Errorf("legacy cell router should still emit hash routing:\n%s", js)
	}
	if strings.Contains(js, "window.__sigilNav") {
		t.Errorf("legacy cell router should not emit path-mode nav:\n%s", js)
	}
}
