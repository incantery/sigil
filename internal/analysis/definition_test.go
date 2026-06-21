package analysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/incantery/sigil/internal/load"
)

func loadProg(t *testing.T, src string) *load.Program {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(entry, load.Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

func TestDefinitionParam(t *testing.T) {
	// "let inc n = n + 1" — param binder `n` at 1:9; its use at 1:13.
	prog := loadProg(t, "let inc n = n + 1\n")
	loc, ok := Definition(prog, 1, 13)
	if !ok {
		t.Fatal("expected to resolve the parameter use")
	}
	if loc.Range.Start.Line != 1 || loc.Range.Start.Col != 9 {
		t.Errorf("param def = %d:%d, want 1:9", loc.Range.Start.Line, loc.Range.Start.Col)
	}
	if loc.File != prog.Entry.File {
		t.Errorf("File = %q, want entry file", loc.File)
	}
}

func TestDefinitionTopLevel(t *testing.T) {
	// `inc` use on line 2 resolves to its top-level decl on line 1 (col 1).
	prog := loadProg(t, "let inc n = n + 1\nlet main = inc 41\n")
	loc, ok := Definition(prog, 2, 12) // the `inc` in `inc 41`
	if !ok {
		t.Fatal("expected to resolve the top-level use")
	}
	if loc.Range.Start.Line != 1 || loc.Range.Start.Col != 1 {
		t.Errorf("top-level def = %d:%d, want 1:1", loc.Range.Start.Line, loc.Range.Start.Col)
	}
}

func TestDefinitionShadowing(t *testing.T) {
	// A parameter `x` shadows a top-level `x`; the use inside resolves to the param.
	// "let x = 1\nlet f x = x\n" — inside f, `x` (use at 2:11) is the param at 2:7.
	prog := loadProg(t, "let x = 1\nlet f x = x\n")
	loc, ok := Definition(prog, 2, 11)
	if !ok {
		t.Fatal("expected resolution")
	}
	if loc.Range.Start.Line != 2 || loc.Range.Start.Col != 7 {
		t.Errorf("shadowed def = %d:%d, want 2:7 (the param, not top-level x)", loc.Range.Start.Line, loc.Range.Start.Col)
	}
}

func TestDefinitionConstructor(t *testing.T) {
	// "type C = Red | Green\nlet main = Red" — `Red` use at 2:12 -> variant at 1:10.
	prog := loadProg(t, "type C = Red | Green\nlet main = Red\n")
	loc, ok := Definition(prog, 2, 12)
	if !ok {
		t.Fatal("expected to resolve the constructor use")
	}
	if loc.Range.Start.Line != 1 || loc.Range.Start.Col != 10 {
		t.Errorf("ctor def = %d:%d, want 1:10", loc.Range.Start.Line, loc.Range.Start.Col)
	}
}

func TestDefinitionWhitespaceNone(t *testing.T) {
	prog := loadProg(t, "let main = 1\n")
	if _, ok := Definition(prog, 5, 1); ok {
		t.Error("expected no definition off-source")
	}
}

func TestDefinitionLetInScoping(t *testing.T) {
	// let outer x =
	//   let x = x + 1
	//   x
	// The `x` in the inner let's RHS (line 2, col 11) must see the PARAM x
	// (1:11), NOT the inner let — a non-recursive let's RHS does not see the
	// name it binds. The body `x` (line 3, col 3) sees the inner let (2:3),
	// shadowing the param.
	prog := loadProg(t, "let outer x =\n  let x = x + 1\n  x\n")

	rhs, ok := Definition(prog, 2, 11)
	if !ok {
		t.Fatal("expected resolution of the RHS x")
	}
	if rhs.Range.Start.Line != 1 || rhs.Range.Start.Col != 11 {
		t.Errorf("RHS x def = %d:%d, want 1:11 (the param, not the inner let)", rhs.Range.Start.Line, rhs.Range.Start.Col)
	}

	body, ok := Definition(prog, 3, 3)
	if !ok {
		t.Fatal("expected resolution of the body x")
	}
	if body.Range.Start.Line != 2 || body.Range.Start.Col != 3 {
		t.Errorf("body x def = %d:%d, want 2:3 (the inner let)", body.Range.Start.Line, body.Range.Start.Col)
	}
}
