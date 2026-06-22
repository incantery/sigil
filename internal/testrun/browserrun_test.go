package testrun

import (
	"path/filepath"
	"testing"
)

// stubDriver implements the browser primitives without a real browser.
type stubDriver struct{ text map[string]string }

func (d *stubDriver) Navigate(string) error             { return nil }
func (d *stubDriver) Click(string) error                { return nil }
func (d *stubDriver) Fill(string, string) error         { return nil }
func (d *stubDriver) WaitVisible(string) error          { return nil }
func (d *stubDriver) DomText(sel string) (string, error) { return d.text[sel], nil }

func TestRunFileBrowserWithStub(t *testing.T) {
	src := `import "std/browser" (navigate, domText)
import "std/test" (eq)
test "reads dom" {
  navigate "http://x";
  expect (eq (domText "#h") "hi")
}`
	dir := writeTests(t, map[string]string{"d_test.sigil": src})
	d := &stubDriver{text: map[string]string{"#h": "hi"}}
	results, err := runFileBrowserWith(filepath.Join(dir, "d_test.sigil"), repoRoot, d)
	if err != nil {
		t.Fatalf("runFileBrowserWith: %v", err)
	}
	if len(results) != 1 || results[0].Name != "reads dom" {
		t.Fatalf("got %+v, want one test 'reads dom'", results)
	}
	if results[0].Error != "" || !allExpectsPass(results[0].Expects) {
		t.Errorf("expected pass, got error=%q expects=%+v", results[0].Error, results[0].Expects)
	}

	// A mismatch must surface as a failing expect (blocking primitive returned a value).
	d2 := &stubDriver{text: map[string]string{"#h": "bye"}}
	r2, _ := runFileBrowserWith(filepath.Join(dir, "d_test.sigil"), repoRoot, d2)
	if allExpectsPass(r2[0].Expects) {
		t.Error("expected the eq to fail when domText differs")
	}
}
