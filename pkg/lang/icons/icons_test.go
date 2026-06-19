package icons

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateGoodSVG(t *testing.T) {
	src := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor">
  <path d="M8 3v10M3 8h10"/>
</svg>`
	a, err := Validate([]byte(src))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if a.ViewBox != "0 0 16 16" {
		t.Errorf("ViewBox = %q", a.ViewBox)
	}
	if !strings.Contains(a.Inner, "<path") {
		t.Errorf("Inner missing path: %q", a.Inner)
	}
}

func TestValidateRejectsHardcodedFill(t *testing.T) {
	src := `<svg viewBox="0 0 16 16">
  <path d="M0 0" fill="#ff0000"/>
</svg>`
	_, err := Validate([]byte(src))
	if err == nil {
		t.Fatal("expected error for hardcoded fill")
	}
	if !strings.Contains(err.Error(), "hardcoded fill") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsHardcodedStroke(t *testing.T) {
	src := `<svg viewBox="0 0 16 16">
  <path d="M0 0" stroke="red"/>
</svg>`
	_, err := Validate([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "hardcoded stroke") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateAllowsCurrentColor(t *testing.T) {
	src := `<svg viewBox="0 0 16 16">
  <path d="M0 0" fill="currentColor"/>
  <path d="M0 0" stroke="currentColor"/>
  <path d="M0 0" fill="none" stroke="none"/>
</svg>`
	if _, err := Validate([]byte(src)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRequiresViewBox(t *testing.T) {
	src := `<svg width="16" height="16"><path d="M0 0"/></svg>`
	_, err := Validate([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "viewBox") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsScript(t *testing.T) {
	src := `<svg viewBox="0 0 16 16"><script>alert(1)</script></svg>`
	_, err := Validate([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "script") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsForeignObject(t *testing.T) {
	src := `<svg viewBox="0 0 16 16"><foreignObject></foreignObject></svg>`
	_, err := Validate([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "foreignObject") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsStyle(t *testing.T) {
	src := `<svg viewBox="0 0 16 16"><style>.x{fill:red}</style></svg>`
	_, err := Validate([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "style") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsExternalUseHref(t *testing.T) {
	src := `<svg viewBox="0 0 16 16"><use href="https://example.com/x.svg"/></svg>`
	_, err := Validate([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "external href") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateAllowsLocalUseHref(t *testing.T) {
	src := `<svg viewBox="0 0 16 16">
  <defs><path id="p" d="M0 0"/></defs>
  <use href="#p"/>
</svg>`
	if _, err := Validate([]byte(src)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestLoadFolder(t *testing.T) {
	root := t.TempDir()
	must := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	good := `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor"><path d="M0 0"/></svg>`
	must("plus.svg", good)
	must("check.svg", good)
	must("arrows/right.svg", good)         // → "arrows.right"
	must("_scratch.svg", "garbage")        // skipped (underscore prefix)
	must("README.md", "notes")             // skipped (not .svg)
	must("screenshot.png", "binary stuff") // skipped (not .svg)

	out, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := out["plus"]; !ok {
		t.Error("missing plus")
	}
	if _, ok := out["check"]; !ok {
		t.Error("missing check")
	}
	if _, ok := out["arrows.right"]; !ok {
		t.Error("missing arrows.right (subfolder scoping)")
	}
	if _, ok := out["_scratch"]; ok {
		t.Error("underscore-prefixed file should be ignored")
	}
	if len(out) != 3 {
		t.Errorf("want 3 icons, got %d: %v", len(out), keys(out))
	}
}

func TestLoadAggregatesValidationErrors(t *testing.T) {
	root := t.TempDir()
	must := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("good.svg", `<svg viewBox="0 0 16 16"><path d="M0 0" fill="currentColor"/></svg>`)
	must("bad-fill.svg", `<svg viewBox="0 0 16 16"><path d="M0 0" fill="#ff0000"/></svg>`)
	must("no-viewbox.svg", `<svg><path d="M0 0"/></svg>`)

	_, err := Load(root)
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bad-fill") || !strings.Contains(msg, "no-viewbox") {
		t.Fatalf("err missing both failures: %v", err)
	}
}

func keys(m map[string]Asset) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
