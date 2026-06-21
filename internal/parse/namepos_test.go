package parse

import (
	"testing"

	"github.com/incantery/sigil/internal/ast"
)

func TestLetAndTypeNamePos(t *testing.T) {
	// "let inc n = n"  — name `inc` at 1:5
	// "type Color = Red" — name `Color` at 2:6, ctor `Red` at 2:14
	m, err := Module("let inc n = n\ntype Color = Red\n")
	if err != nil {
		t.Fatal(err)
	}
	ld := m.Decls[0].(*ast.LetDecl)
	if ld.NamePos.Line != 1 || ld.NamePos.Col != 5 {
		t.Errorf("LetDecl NamePos = %d:%d, want 1:5", ld.NamePos.Line, ld.NamePos.Col)
	}
	td := m.Decls[1].(*ast.TypeDecl)
	if td.NamePos.Line != 2 || td.NamePos.Col != 6 {
		t.Errorf("TypeDecl NamePos = %d:%d, want 2:6", td.NamePos.Line, td.NamePos.Col)
	}
}

func TestCtorPatPos(t *testing.T) {
	// "let f c = match c with | Some y -> y" — Some (CtorPat) at 1:26
	// l1 e2 t3 ' '4 f5 ' '6 c7 ' '8 =9 ' '10 m11 a12 t13 c14 h15 ' '16 c17 ' '18 w19 i20 t21 h22 ' '23 |24 ' '25 S26
	m, err := Module("let f c = match c with | Some y -> y\n")
	if err != nil {
		t.Fatal(err)
	}
	ld := m.Decls[0].(*ast.LetDecl)
	mt := ld.Body.(*ast.Match)
	cp := mt.Arms[0].Pat.(ast.CtorPat)
	if cp.Pos.Line != 1 || cp.Pos.Col != 26 {
		t.Errorf("CtorPat Pos = %d:%d, want 1:26", cp.Pos.Line, cp.Pos.Col)
	}
}

func TestNullaryCtorPatPos(t *testing.T) {
	// "let f x = match x with | Some None -> 0" — the inner `None` is a nullary
	// constructor pattern parsed via parsePatternAtom; it sits at 1:31.
	// |24 ' '25 S26 o27 m28 e29 ' '30 N31
	m, err := Module("let f x = match x with | Some None -> 0\n")
	if err != nil {
		t.Fatal(err)
	}
	ld := m.Decls[0].(*ast.LetDecl)
	mt := ld.Body.(*ast.Match)
	some := mt.Arms[0].Pat.(ast.CtorPat)
	none := some.Args[0].(ast.CtorPat)
	if none.Pos.Line != 1 || none.Pos.Col != 31 {
		t.Errorf("nullary CtorPat None pos = %d:%d, want 1:31", none.Pos.Line, none.Pos.Col)
	}
}
