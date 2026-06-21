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
			kind := SymbolKindEnum
			if d.Record != nil {
				kind = SymbolKindStruct
			}
			syms = append(syms, symbolAt(d.Name, kind, d.Pos))
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
