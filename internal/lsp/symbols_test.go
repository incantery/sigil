package lsp

import "testing"

func TestDocumentSymbolsKinds(t *testing.T) {
	src := `pub let answer = 42
let add a b = a + b
pub type Color = Red | Green | Blue
type Point = { x: Int, y: Int }
`
	syms := documentSymbols(src)
	byName := map[string]DocumentSymbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	if len(syms) != 4 {
		t.Fatalf("want 4 symbols, got %d: %+v", len(syms), syms)
	}
	if byName["answer"].Kind != SymbolKindVariable {
		t.Errorf("answer should be Variable, got %d", byName["answer"].Kind)
	}
	if byName["add"].Kind != SymbolKindFunction {
		t.Errorf("add should be Function, got %d", byName["add"].Kind)
	}
	if byName["Color"].Kind != SymbolKindEnum {
		t.Errorf("Color should be Enum, got %d", byName["Color"].Kind)
	}
	if byName["Point"].Kind != SymbolKindStruct {
		t.Errorf("Point should be Struct, got %d", byName["Point"].Kind)
	}
	// Range is 0-based; "answer" is on the first line.
	if byName["answer"].Range.Start.Line != 0 {
		t.Errorf("answer line = %d, want 0", byName["answer"].Range.Start.Line)
	}
}

func TestDocumentSymbolsParseErrorEmpty(t *testing.T) {
	if syms := documentSymbols("pub let x = ("); len(syms) != 0 {
		t.Errorf("unparsable source should yield no symbols, got %d", len(syms))
	}
}
