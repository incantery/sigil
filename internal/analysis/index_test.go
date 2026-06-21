package analysis

import (
	"testing"

	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/parse"
)

func TestAtFindsSmallestNode(t *testing.T) {
	// "let main = count + 1" on line 1 (1-based). Columns (1-based):
	// l=1 e=2 t=3 ' '=4 m=5..n=8 ' '=9 '='=10 ' '=11 c=12 o=13 u=14 n=15 t=16 ' '=17 +=18 ' '=19 1=20
	m, err := parse.Module("let main = count + 1\n")
	if err != nil {
		t.Fatal(err)
	}
	ix := Index(m)

	// Cursor on "count" (col 14) -> the Var node "count".
	node, _, ok := ix.At(1, 14)
	if !ok {
		t.Fatal("expected a node at the identifier")
	}
	v, isVar := node.(*ast.Var)
	if !isVar || v.Name != "count" {
		t.Errorf("At(1,14) = %T, want *ast.Var{Name:count}", node)
	}

	// Cursor on the literal "1" (col 20) -> the IntLit node.
	lit, _, ok := ix.At(1, 20)
	if !ok {
		t.Fatal("expected a node at the literal")
	}
	if _, isInt := lit.(*ast.IntLit); !isInt {
		t.Errorf("At(1,20) = %T, want *ast.IntLit", lit)
	}

	// Cursor past end of line (col 40) -> nothing.
	if _, _, ok := ix.At(1, 40); ok {
		t.Error("expected no node past end of line")
	}
}

func TestAtPicksInnermost(t *testing.T) {
	// "let main = f (g 1)" — the "1" is at col 17 (1-based).
	m, err := parse.Module("let main = f (g 1)\n")
	if err != nil {
		t.Fatal(err)
	}
	ix := Index(m)
	node, _, ok := ix.At(1, 17)
	if !ok {
		t.Fatal("expected a node at the inner literal")
	}
	if _, isInt := node.(*ast.IntLit); !isInt {
		t.Errorf("At(1,17) = %T, want the innermost *ast.IntLit", node)
	}
}
