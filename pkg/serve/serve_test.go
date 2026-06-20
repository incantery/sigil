package serve

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/sigil/pkg/contract"
)

func writeApp(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sigil.mod"), []byte("module example.com/proj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `fonts google = "Spline Sans"

backend Api =
  url same-origin
  auth none

stream Chat -> prompt : String = String

view App =
  state prompt = ""
  input prompt placeholder="ask"
  text "hello"
`
	if err := os.WriteFile(filepath.Join(dir, "app.sigil"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func appHash(t *testing.T, dir string) string {
	t.Helper()
	doc, err := compile(dir)
	if err != nil {
		t.Fatal(err)
	}
	return contract.FromDoc(doc).Hash()
}

func TestPageServesCompiledApp(t *testing.T) {
	dir := writeApp(t)
	h, err := Page(dir, ExpectContract(appHash(t, dir)))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'none'",
		"script-src 'sha256-",
		"style-src 'sha256-",
		"https://fonts.googleapis.com",
		"font-src https://fonts.gstatic.com",
		"connect-src 'self'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q\nCSP = %s", want, csp)
		}
	}

	// The CSP hashes must match the inline segments AS SERVED — if
	// ComposeDoc ever re-indents or escapes the script/style after
	// the hash is computed (or vice versa), the browser would reject
	// the page. Recompute over the served body and require the token
	// appears in the header.
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, seg := range []struct{ name, open, close string }{
		{"script", "<script>", "</script>"},
		{"style", "<style>", "</style>"},
	} {
		i := strings.Index(string(body), seg.open)
		j := strings.LastIndex(string(body), seg.close)
		if i < 0 || j < 0 || j < i {
			t.Fatalf("served body missing %s element", seg.name)
		}
		inner := string(body)[i+len(seg.open) : j]
		sum := sha256.Sum256([]byte(inner))
		tok := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !strings.Contains(csp, tok) {
			t.Errorf("%s-src hash does not match served bytes\n  want token %s in CSP\n  CSP = %s", seg.name, tok, csp)
		}
	}
}

func TestPageRoutes(t *testing.T) {
	dir := writeApp(t)
	h, err := Page(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("GET /nope = %d, want 404", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", nil)
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != 405 {
		t.Errorf("POST / = %d, want 405", res2.StatusCode)
	}
}

// writePathApp writes an app whose view is a path-driven router. Such a
// page resolves routes client-side, so the server must serve the shell on
// every path (catch-all), not just "/".
func writePathApp(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sigil.mod"), []byte("module example.com/proj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `view App =
  router
    route "home"
      path "/"
      public
      text "home"
    route "about"
      path "/about"
      public
      text "about page"
`
	if err := os.WriteFile(filepath.Join(dir, "app.sigil"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestPathRouterGroupServes exercises the full compile→render→serve path on
// a grouped, guarded path router — catching any renderer gap on KindGroup
// that the codegen-only tests miss.
func TestPathRouterGroupServes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sigil.mod"), []byte("module example.com/proj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
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
`
	if err := os.WriteFile(filepath.Join(dir, "app.sigil"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := Page(dir)
	if err != nil {
		t.Fatalf("Page (grouped router): %v", err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("GET / on grouped router = %d, want 200", res.StatusCode)
	}
}

// TestPathRouterServesCatchAll locks that a page with a path-driven router
// serves its shell on a deep link (GET /about → 200), so a hard refresh or
// shared URL reaches the SPA, while the GET/HEAD method gate still applies.
func TestPathRouterServesCatchAll(t *testing.T) {
	dir := writePathApp(t)
	h, err := Page(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/about")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("GET /about = %d, want 200 (catch-all shell)", res.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/about", nil)
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != 405 {
		t.Errorf("POST /about = %d, want 405 (method gate still applies)", res2.StatusCode)
	}
}

func TestPageContractMismatchFailsBoot(t *testing.T) {
	dir := writeApp(t)
	_, err := Page(dir, ExpectContract("c1:deadbeef"))
	if err == nil {
		t.Fatal("expected boot failure on contract hash mismatch")
	}
	if !strings.Contains(err.Error(), "contract hash mismatch") || !strings.Contains(err.Error(), "sigil gen") {
		t.Errorf("err = %v", err)
	}
}

func TestPageBrokenSourceFailsBoot(t *testing.T) {
	dir := writeApp(t)
	if err := os.WriteFile(filepath.Join(dir, "broken.sigil"), []byte("view Broken =\n  nosuchthing 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Page(dir); err == nil {
		t.Fatal("expected boot failure on broken source")
	}
}

// TestPageDevModeContractDrift: in dev mode the contract-hash check
// runs per request, not just at boot, so editing the package into a
// different contract shows a 500 instead of silently serving a page
// whose client bundle no longer matches the (generated) server. This
// is the exact WithDev + ExpectContract combination generated
// NewPageHandler uses under -dev.
func TestPageDevModeContractDrift(t *testing.T) {
	dir := writeApp(t)
	h, err := Page(dir, WithDev(), ExpectContract(appHash(t, dir)))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("baseline request = %d", res.StatusCode)
	}

	// Add an op: the contract changes, so its hash no longer matches
	// the one the handler was constructed with.
	if err := os.WriteFile(filepath.Join(dir, "extra.sigil"),
		[]byte("query Added = Bool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res2, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != 500 {
		t.Errorf("drifted dev request = %d, want 500", res2.StatusCode)
	}
	if body, _ := io.ReadAll(res2.Body); !strings.Contains(string(body), "contract hash mismatch") {
		t.Errorf("body = %q, want contract-hash-mismatch message", body)
	}
}

func TestPageDevModeRecompiles(t *testing.T) {
	dir := writeApp(t)
	h, err := Page(dir, WithDev())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("first request = %d", res.StatusCode)
	}

	// Break the source: dev mode reports per request instead of
	// crashing the process.
	if err := os.WriteFile(filepath.Join(dir, "app.sigil"), []byte("view App =\n  nosuchthing 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res2, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != 500 {
		t.Errorf("broken dev request = %d, want 500", res2.StatusCode)
	}
}

func TestWithoutCSP(t *testing.T) {
	dir := writeApp(t)
	h, err := Page(dir, WithoutCSP())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := res.Header.Get("Content-Security-Policy"); got != "" {
		t.Errorf("CSP should be absent, got %q", got)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff must survive WithoutCSP")
	}
}

// TestResponseControls covers the request/response-metadata seam
// (sigil-requests REQUEST 13): WithResponse stashes controls on the
// context, ResponseFrom retrieves them, and the read/write surface
// reaches the underlying request and response.
func TestResponseControls(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/command/Login", nil)
	r.Header.Set("Origin", "https://app.example")
	r.AddCookie(&http.Cookie{Name: "iris-session", Value: "abc.def"})

	ctx := WithResponse(r.Context(), rec, r)
	rc := ResponseFrom(ctx)
	if rc == nil {
		t.Fatal("ResponseFrom returned nil inside a WithResponse context")
	}

	// Reads see request metadata.
	if got := rc.RequestHeader("Origin"); got != "https://app.example" {
		t.Errorf("RequestHeader(Origin) = %q", got)
	}
	c, err := rc.Cookie("iris-session")
	if err != nil || c.Value != "abc.def" {
		t.Errorf("Cookie(iris-session) = %v, %v", c, err)
	}

	// Writes land on the response.
	rc.SetCookie(&http.Cookie{Name: "iris-session", Value: "new", HttpOnly: true, Path: "/"})
	rc.SetHeader("X-Single", "one")
	rc.AddHeader("X-Multi", "a")
	rc.AddHeader("X-Multi", "b")

	if sc := rec.Header().Get("Set-Cookie"); !strings.Contains(sc, "iris-session=new") || !strings.Contains(sc, "HttpOnly") {
		t.Errorf("Set-Cookie = %q", sc)
	}
	if got := rec.Header().Get("X-Single"); got != "one" {
		t.Errorf("X-Single = %q", got)
	}
	if got := rec.Header().Values("X-Multi"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("X-Multi = %v", got)
	}
}

// TestResponseFromAbsent: outside a generated handler / WithResponse
// context, ResponseFrom returns nil (documented escape valve, not a
// panic).
func TestResponseFromAbsent(t *testing.T) {
	if rc := ResponseFrom(context.Background()); rc != nil {
		t.Errorf("ResponseFrom(background) = %v, want nil", rc)
	}
}
