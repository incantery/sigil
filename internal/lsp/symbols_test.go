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

func TestDocumentSymbolsHierarchical(t *testing.T) {
	src := `pub type Color = Red | Green | Blue
type Point = { x: Int, y: Int }
let f x = x
`
	syms := documentSymbols(src)
	if len(syms) != 3 {
		t.Fatalf("want 3 top-level symbols, got %d", len(syms))
	}
	byName := map[string]DocumentSymbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}

	// ADT: Color is an Enum with three EnumMember children.
	color := byName["Color"]
	if color.Kind != SymbolKindEnum {
		t.Errorf("Color kind = %d, want Enum(%d)", color.Kind, SymbolKindEnum)
	}
	if len(color.Children) != 3 {
		t.Fatalf("Color has %d children, want 3", len(color.Children))
	}
	wantCtors := []string{"Red", "Green", "Blue"}
	for i, c := range color.Children {
		if c.Name != wantCtors[i] || c.Kind != SymbolKindEnumMember {
			t.Errorf("child %d = %q kind %d, want %q EnumMember(%d)", i, c.Name, c.Kind, wantCtors[i], SymbolKindEnumMember)
		}
	}
	// Containment: each child's range is inside Color's range. Uses the package
	// helper posBeforeSym defined in symbols.go (Step 3/4).
	for _, c := range color.Children {
		if posBeforeSym(c.Range.Start, color.Range.Start) || posBeforeSym(color.Range.End, c.Range.End) {
			t.Errorf("child %q range %+v not contained in parent %+v", c.Name, c.Range, color.Range)
		}
	}

	// selectionRange stays the name span; range was expanded past it to contain
	// the members (so selectionRange.End is strictly before the expanded range.End).
	if got := color.SelectionRange.End.Character - color.SelectionRange.Start.Character; got != len("Color") {
		t.Errorf("Color selectionRange spans %d chars, want %d (the name)", got, len("Color"))
	}
	if !posBeforeSym(color.SelectionRange.End, color.Range.End) {
		t.Error("Color range.End should extend past selectionRange.End to contain its children")
	}

	// Record: Point is a Struct with two Field children.
	point := byName["Point"]
	if point.Kind != SymbolKindStruct {
		t.Errorf("Point kind = %d, want Struct(%d)", point.Kind, SymbolKindStruct)
	}
	if len(point.Children) != 2 {
		t.Fatalf("Point has %d children, want 2", len(point.Children))
	}
	if point.Children[0].Name != "x" || point.Children[0].Kind != SymbolKindField {
		t.Errorf("Point child[0] = %q kind %d, want x Field", point.Children[0].Name, point.Children[0].Kind)
	}
	if point.Children[1].Name != "y" || point.Children[1].Kind != SymbolKindField {
		t.Errorf("Point child[1] = %q kind %d, want y Field", point.Children[1].Name, point.Children[1].Kind)
	}

	// Function stays a leaf (no children).
	if f := byName["f"]; f.Kind != SymbolKindFunction || len(f.Children) != 0 {
		t.Errorf("f = kind %d, %d children; want Function(%d) leaf", f.Kind, len(f.Children), SymbolKindFunction)
	}
}
