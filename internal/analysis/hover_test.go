package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/sigil/internal/load"
)

func loadRec(t *testing.T, src string) *load.Program {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(entry, load.Options{Root: dir, Record: true})
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

func TestHoverTopLevelBindingShowsScheme(t *testing.T) {
	// Hover the use of the top-level binding `inc` (line 2, col 12) and
	// expect its generalized scheme.
	prog := loadRec(t, "let inc n = n + 1\nlet main = inc 41\n")
	res, ok := Hover(prog, 2, 12)
	if !ok {
		t.Fatal("expected hover on inc")
	}
	if !strings.Contains(res.Markdown, "inc :") || !strings.Contains(res.Markdown, "Int -> Int") {
		t.Errorf("hover markdown = %q, want inc : Int -> Int", res.Markdown)
	}
}

func TestHoverParamShowsMonomorphicType(t *testing.T) {
	// "let inc n = n + 1" — the USE of the parameter `n` is at line 1, col 13.
	// `n` is a lambda parameter (not a top-level binding), so hover shows its
	// inferred monomorphic type, not a generalized scheme.
	prog := loadRec(t, "let inc n = n + 1\nlet main = inc 41\n")
	res, ok := Hover(prog, 1, 13)
	if !ok {
		t.Fatal("expected hover on the parameter use")
	}
	if !strings.Contains(res.Markdown, "n : Int") {
		t.Errorf("hover markdown = %q, want n : Int", res.Markdown)
	}
}

func TestHoverPastSourceIsNull(t *testing.T) {
	prog := loadRec(t, "let main = 1\n")
	if _, ok := Hover(prog, 5, 1); ok { // a line past the source
		t.Error("expected no hover on empty region")
	}
}
