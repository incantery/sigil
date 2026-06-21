package parse

import (
	"testing"

	"github.com/incantery/sigil/internal/ast"
)

func TestFieldTypePositionRecorded(t *testing.T) {
	// "type Point = { x: Int, y: Int }" — field `x` is at col 16, `y` at col 24 (1-based):
	// t1 y2 p3 e4 ' '5 P6 o7 i8 n9 t10 ' '11 =12 ' '13 {14 ' '15 x16 ... ,22 ' '23 y24
	m, err := Module("type Point = { x: Int, y: Int }\n")
	if err != nil {
		t.Fatal(err)
	}
	td := m.Decls[0].(*ast.TypeDecl)
	if len(td.Record) != 2 {
		t.Fatalf("got %d record fields, want 2", len(td.Record))
	}
	if td.Record[0].Pos.Line != 1 || td.Record[0].Pos.Col != 16 {
		t.Errorf("field x pos = %d:%d, want 1:16", td.Record[0].Pos.Line, td.Record[0].Pos.Col)
	}
	if td.Record[1].Pos.Col != 24 {
		t.Errorf("field y pos col = %d, want 24", td.Record[1].Pos.Col)
	}
}
