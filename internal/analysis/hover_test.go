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

func TestHoverLocalIdentifier(t *testing.T) {
	// line 2 col 16 is the use of `n` inside `n + 1`.
	prog := loadRec(t, "let inc n = n + 1\nlet main = inc 41\n")
	// Hover the use of `inc` (line 2, col 12) -> generalized scheme.
	res, ok := Hover(prog, 2, 12)
	if !ok {
		t.Fatal("expected hover on inc")
	}
	if !strings.Contains(res.Markdown, "inc :") || !strings.Contains(res.Markdown, "Int -> Int") {
		t.Errorf("hover markdown = %q, want inc : Int -> Int", res.Markdown)
	}
}

func TestHoverWhitespaceIsNull(t *testing.T) {
	prog := loadRec(t, "let main = 1\n")
	if _, ok := Hover(prog, 5, 1); ok { // a line past the source
		t.Error("expected no hover on empty region")
	}
}
