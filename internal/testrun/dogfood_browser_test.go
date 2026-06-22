package testrun

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/sigil/internal/browser"
)

func TestDogfoodBrowser(t *testing.T) {
	// Skip early if Chrome can't launch, so CI without Chrome stays green.
	s, err := browser.New()
	if err != nil {
		t.Skipf("no Chrome: %v", err)
	}
	s.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body><h1 id="t">Counter</h1></body></html>`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	src := fmt.Sprintf(`import "std/browser" (navigate, domText)
import "std/test" (eq)
test "homepage title" {
  navigate %q;
  waitVisible "#t";
  expect (eq (domText "#t") "Counter")
}`, srv.URL)
	// note: waitVisible must be imported too
	src = strings.Replace(src, `(navigate, domText)`, `(navigate, waitVisible, domText)`, 1)
	if err := os.WriteFile(filepath.Join(dir, "h_test.sigil"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	ok, err := Run(&buf, dir, repoRoot)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, buf.String())
	}
	if !ok {
		t.Fatalf("dogfood browser test failed:\n%s", buf.String())
	}
}
