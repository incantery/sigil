package lsp

import (
	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/parse"
)

// documentSymbols returns a flat list of top-level symbols for the source.
// Symbols need only a parse (no imports, no type info); a parse failure yields
// an empty list (the parse error is already reported as a diagnostic).
func documentSymbols(text string) []DocumentSymbol {
	m, err := parse.Module(text)
	if err != nil {
		return []DocumentSymbol{}
	}
	syms := []DocumentSymbol{}
	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			if d.Name == "" {
				continue // destructuring `let (a,b) = ...`: no single name
			}
			kind := SymbolKindVariable
			if len(d.Params) > 0 {
				kind = SymbolKindFunction
			}
			syms = append(syms, symbolAt(d.Name, kind, d.Pos))
		case *ast.TypeDecl:
			syms = append(syms, typeSymbol(d))
		}
	}
	return syms
}

// symbolAt builds a DocumentSymbol whose range spans the name at pos (1-based
// pos -> 0-based LSP range).
func symbolAt(name string, kind int, pos ast.Pos) DocumentSymbol {
	start := Position{Line: pos.Line - 1, Character: pos.Col - 1}
	end := Position{Line: pos.Line - 1, Character: pos.Col - 1 + len(name)}
	r := Range{Start: start, End: end}
	return DocumentSymbol{Name: name, Kind: kind, Range: r, SelectionRange: r}
}

// typeSymbol builds a hierarchical symbol for a type declaration: an Enum with
// EnumMember children (ADT constructors) or a Struct with Field children (record
// fields). The parent range is expanded to contain its children (LSP requires
// child ranges to lie within the parent range); selectionRange stays the name.
func typeSymbol(d *ast.TypeDecl) DocumentSymbol {
	var children []DocumentSymbol
	kind := SymbolKindEnum
	if d.Record != nil {
		kind = SymbolKindStruct
		for _, f := range d.Record {
			children = append(children, symbolAt(f.Name, SymbolKindField, f.Pos))
		}
	} else {
		for _, v := range d.Variants {
			children = append(children, symbolAt(v.Name, SymbolKindEnumMember, v.Pos))
		}
	}
	sym := symbolAt(d.Name, kind, d.Pos) // range == selectionRange == name span
	if len(children) > 0 {
		sym.Children = children
		end := sym.Range.End
		for _, c := range children {
			if posBeforeSym(end, c.Range.End) {
				end = c.Range.End
			}
		}
		sym.Range = Range{Start: sym.Range.Start, End: end} // contains all children
	}
	return sym
}

// posBeforeSym reports whether a is strictly before b (0-based LSP positions).
func posBeforeSym(a, b Position) bool {
	return a.Line < b.Line || (a.Line == b.Line && a.Character < b.Character)
}
