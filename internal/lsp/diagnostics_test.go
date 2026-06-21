package lsp

import (
	"fmt"
	"testing"

	"github.com/incantery/sigil/internal/parse"
	"github.com/incantery/sigil/internal/types"
)

func TestDiagnosticsForParseError(t *testing.T) {
	err := fmt.Errorf("/x.sigil: %w", &parse.Error{Line: 3, Col: 5, Msg: "expected expression"})
	ds := diagnosticsFor(err, "line1\nline2\nabcdefgh\n")
	if len(ds) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(ds))
	}
	d := ds[0]
	if d.Range.Start.Line != 2 || d.Range.Start.Character != 4 {
		t.Errorf("start = %d:%d, want 2:4 (0-based)", d.Range.Start.Line, d.Range.Start.Character)
	}
	if d.Range.End.Character != 8 { // end of "abcdefgh"
		t.Errorf("end char = %d, want 8 (end of line 3)", d.Range.End.Character)
	}
	if d.Severity != SeverityError || d.Message != "expected expression" {
		t.Errorf("unexpected severity/message: %d %q", d.Severity, d.Message)
	}
}

func TestDiagnosticsForTypeError(t *testing.T) {
	err := fmt.Errorf("/x.sigil: %w", &types.Error{Line: 1, Col: 1, Msg: "type mismatch: Int vs String"})
	ds := diagnosticsFor(err, "x\n")
	if len(ds) != 1 || ds[0].Range.Start.Line != 0 || ds[0].Message != "type mismatch: Int vs String" {
		t.Fatalf("unexpected type diagnostic: %+v", ds)
	}
}

func TestDiagnosticsForPositionlessError(t *testing.T) {
	err := fmt.Errorf("cannot resolve import \"std/nope\"")
	ds := diagnosticsFor(err, "import \"std/nope\"\n")
	if len(ds) != 1 || ds[0].Range.Start.Line != 0 || ds[0].Range.Start.Character != 0 {
		t.Fatalf("positionless error should map to (0,0): %+v", ds)
	}
}

func TestDiagnosticsForNilError(t *testing.T) {
	if ds := diagnosticsFor(nil, "anything"); len(ds) != 0 {
		t.Errorf("nil error should give no diagnostics, got %d", len(ds))
	}
}
