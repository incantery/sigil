// Package analysis provides position-based queries over a parsed sigil module
// (used by the LSP for hover, and later go-to-definition / semantic tokens).
// AST nodes carry only a start position, so node extents are computed
// structurally: a node spans from its start to the furthest end of its
// descendants; a leaf ends at start + len(its source text).
package analysis

import "github.com/incantery/sigil/internal/ast"

// Range is a 1-based source span.
type Range struct{ Start, End ast.Pos }

// NodeIndex maps source positions to the expression nodes that contain them.
type NodeIndex struct {
	entries []entry
}

type entry struct {
	node  ast.Expr
	start ast.Pos
	end   ast.Pos
}

// Index builds the position→node index for a module's expression trees.
func Index(m *ast.Module) *NodeIndex {
	ix := &NodeIndex{}
	for _, d := range m.Decls {
		if ld, ok := d.(*ast.LetDecl); ok && ld.Body != nil {
			ix.build(ld.Body)
		}
	}
	return ix
}

// build records e and its descendants, returning e's end position.
func (ix *NodeIndex) build(e ast.Expr) ast.Pos {
	start := posOf(e)
	end := leafEnd(e, start)
	for _, ch := range children(e) {
		if ch == nil {
			continue
		}
		ce := ix.build(ch)
		if enc(ce) > enc(end) {
			end = ce
		}
	}
	ix.entries = append(ix.entries, entry{node: e, start: start, end: end})
	return end
}

// At returns the smallest expression node whose extent contains (line, col).
func (ix *NodeIndex) At(line, col int) (ast.Expr, Range, bool) {
	p := ast.Pos{Line: line, Col: col}
	var best *entry
	for i := range ix.entries {
		en := &ix.entries[i]
		if enc(en.start) <= enc(p) && enc(p) <= enc(en.end) {
			if best == nil || span(en) < span(best) {
				best = en
			}
		}
	}
	if best == nil {
		return nil, Range{}, false
	}
	return best.node, Range{Start: best.start, End: best.end}, true
}

const colBits = 1 << 20

func enc(p ast.Pos) int { return p.Line*colBits + p.Col }
func span(e *entry) int { return enc(e.end) - enc(e.start) }

// posOf returns a node's start position.
func posOf(e ast.Expr) ast.Pos {
	switch e := e.(type) {
	case *ast.IntLit:
		return e.Pos
	case *ast.FloatLit:
		return e.Pos
	case *ast.StrLit:
		return e.Pos
	case *ast.Interp:
		return e.Pos
	case *ast.Var:
		return e.Pos
	case *ast.Ctor:
		return e.Pos
	case *ast.Unit:
		return e.Pos
	case *ast.Tuple:
		return e.Pos
	case *ast.ListLit:
		return e.Pos
	case *ast.RecordLit:
		return e.Pos
	case *ast.Lambda:
		return e.Pos
	case *ast.App:
		return e.Pos
	case *ast.Field:
		return e.Pos
	case *ast.Binop:
		return e.Pos
	case *ast.If:
		return e.Pos
	case *ast.Match:
		return e.Pos
	case *ast.Let:
		return e.Pos
	case *ast.Effect:
		return e.Pos
	case *ast.Unop:
		return e.Pos
	}
	return ast.Pos{}
}

// leafEnd returns the end position of a leaf node (start advanced by its source
// length); for composite nodes it returns start (the real end comes from
// children).
func leafEnd(e ast.Expr, start ast.Pos) ast.Pos {
	adv := func(n int) ast.Pos { return ast.Pos{Line: start.Line, Col: start.Col + n} }
	switch e := e.(type) {
	case *ast.Var:
		return adv(len(e.Name))
	case *ast.Ctor:
		return adv(len(e.Name))
	case *ast.IntLit:
		return adv(len(e.Raw))
	case *ast.FloatLit:
		return adv(len(e.Raw))
	case *ast.StrLit:
		return adv(len(e.Value) + 2) // approximate: include the quotes
	case *ast.Unit:
		return adv(2) // "()"
	}
	return start
}

// children returns the sub-expressions of a node (skips patterns and names).
func children(e ast.Expr) []ast.Expr {
	switch e := e.(type) {
	case *ast.Interp:
		return e.Parts
	case *ast.Tuple:
		return e.Elems
	case *ast.ListLit:
		return e.Elems
	case *ast.RecordLit:
		out := make([]ast.Expr, 0, len(e.Fields))
		for _, f := range e.Fields {
			out = append(out, f.Value)
		}
		return out
	case *ast.Lambda:
		return []ast.Expr{e.Body}
	case *ast.App:
		return []ast.Expr{e.Fn, e.Arg}
	case *ast.Field:
		return []ast.Expr{e.Recv}
	case *ast.Binop:
		return []ast.Expr{e.L, e.R}
	case *ast.Unop:
		return []ast.Expr{e.X}
	case *ast.If:
		return []ast.Expr{e.Cond, e.Then, e.Else}
	case *ast.Match:
		out := []ast.Expr{e.Scrut}
		for _, a := range e.Arms {
			if a.Guard != nil {
				out = append(out, a.Guard)
			}
			out = append(out, a.Body)
		}
		return out
	case *ast.Let:
		return []ast.Expr{e.Body, e.In}
	case *ast.Effect:
		return e.Stmts
	}
	return nil
}
