package parse

import (
	"testing"

	"github.com/incantery/sigil/internal/ast"
)

func TestBinderPositionsRecorded(t *testing.T) {
	// "let inc n = n" — the parameter `n` is at line 1, col 9 (1-based):
	// l1 e2 t3 ' '4 i5 n6 c7 ' '8 n9
	m, err := Module("let inc n = n\n")
	if err != nil {
		t.Fatal(err)
	}
	ld := m.Decls[0].(*ast.LetDecl)
	vp, ok := ld.Params[0].(ast.VarParam)
	if !ok {
		t.Fatalf("param 0 = %T, want ast.VarParam", ld.Params[0])
	}
	if vp.Pos.Line != 1 || vp.Pos.Col != 9 {
		t.Errorf("VarParam pos = %d:%d, want 1:9", vp.Pos.Line, vp.Pos.Col)
	}
}

func TestVariantPositionRecorded(t *testing.T) {
	// "type T = Red | Green" — Red at col 10, Green at col 16.
	m, err := Module("type T = Red | Green\n")
	if err != nil {
		t.Fatal(err)
	}
	td := m.Decls[0].(*ast.TypeDecl)
	if td.Variants[0].Pos.Col != 10 {
		t.Errorf("Red pos col = %d, want 10", td.Variants[0].Pos.Col)
	}
	if td.Variants[1].Pos.Col != 16 {
		t.Errorf("Green pos col = %d, want 16", td.Variants[1].Pos.Col)
	}
}

func TestVarPatPositionRecorded(t *testing.T) {
	// Inner block-let destructures a tuple: "  let (a, b) = p" (line 2, indented 2).
	// Columns: 1,2 indent · l3 e4 t5 ' '6 (7 a8 ,9 ' '10 b11 — so `a` is at 2:8.
	m, err := Module("let main =\n  let (a, b) = p\n  a\n")
	if err != nil {
		t.Fatal(err)
	}
	// Drill to the inner Let's destructuring pattern.
	outer := m.Decls[0].(*ast.LetDecl)
	inner := outer.Body.(*ast.Let)
	tup := inner.Pat.(ast.TuplePat)
	a := tup.Elems[0].(ast.VarPat)
	if a.Pos.Line != 2 || a.Pos.Col != 8 {
		t.Errorf("VarPat a pos = %d:%d, want 2:8", a.Pos.Line, a.Pos.Col)
	}
}
