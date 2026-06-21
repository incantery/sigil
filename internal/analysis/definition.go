package analysis

import (
	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/load"
)

// Location is a definition site: an absolute source File plus a 1-based Range
// spanning the bound name.
type Location struct {
	File  string
	Range Range
}

// Definition resolves the use of a name under (line, col) in prog's entry module
// to its binder. Resolution order for a variable: local scope (innermost-first),
// then same-file top-level, then imported (see resolveImported). For a
// constructor: same-file variant, then imported. ok == false when the cursor is
// not on a Var/Ctor use or the name cannot be resolved.
func Definition(prog *load.Program, line, col int) (Location, bool) {
	if prog == nil || prog.Entry == nil {
		return Location{}, false
	}
	m := prog.Entry.AST
	node, _, ok := Index(m).At(line, col)
	if !ok {
		return Location{}, false
	}
	switch n := node.(type) {
	case *ast.Var:
		if p, ok := resolveLocal(m, n, n.Name); ok {
			return loc(prog.Entry.File, p, n.Name), true
		}
		if p, ok := topLevelLet(m, n.Name); ok {
			return loc(prog.Entry.File, p, n.Name), true
		}
		if l, ok := resolveImported(prog, n.Name, false); ok {
			return l, true
		}
	case *ast.Ctor:
		if p, ok := variantInModule(m, n.Name); ok {
			return loc(prog.Entry.File, p, n.Name), true
		}
		if l, ok := resolveImported(prog, n.Name, true); ok {
			return l, true
		}
	}
	return Location{}, false
}

type binder struct {
	name string
	pos  ast.Pos
}

func loc(file string, p ast.Pos, name string) Location {
	return Location{File: file, Range: Range{
		Start: p,
		End:   ast.Pos{Line: p.Line, Col: p.Col + len(name)},
	}}
}

// resolveLocal finds the innermost binder of name that is in scope at the target
// node, walking lexical scope from each top-level decl body.
func resolveLocal(m *ast.Module, target ast.Expr, name string) (ast.Pos, bool) {
	var result ast.Pos
	var found bool
	var search func(e ast.Expr, scope []binder) bool // true once target is located
	search = func(e ast.Expr, scope []binder) bool {
		if e == nil {
			return false
		}
		if e == target {
			for i := len(scope) - 1; i >= 0; i-- {
				if scope[i].name == name {
					result, found = scope[i].pos, true
					return true
				}
			}
			return true // target located; name not bound locally
		}
		switch e := e.(type) {
		case *ast.Lambda:
			return search(e.Body, extend(scope, paramBinders(e.Params)...))
		case *ast.Let:
			body := extend(scope, paramBinders(e.Params)...)
			if e.Rec {
				body = extend(body, letSelf(e)...)
			}
			if search(e.Body, body) {
				return true
			}
			return search(e.In, extend(scope, letSelf(e)...))
		case *ast.Match:
			if search(e.Scrut, scope) {
				return true
			}
			for _, arm := range e.Arms {
				armScope := extend(scope, patBinders(arm.Pat)...)
				if arm.Guard != nil && search(arm.Guard, armScope) {
					return true
				}
				if search(arm.Body, armScope) {
					return true
				}
			}
			return false
		default:
			for _, ch := range children(e) {
				if search(ch, scope) {
					return true
				}
			}
			return false
		}
	}
	for _, d := range m.Decls {
		ld, ok := d.(*ast.LetDecl)
		if !ok || ld.Body == nil {
			continue
		}
		scope := paramBinders(ld.Params)
		if ld.Rec && ld.Name != "" {
			scope = extend(scope, binder{ld.Name, ld.Pos})
		}
		if search(ld.Body, scope) {
			return result, found
		}
	}
	return ast.Pos{}, false
}

// extend returns a fresh scope = base ++ more (never aliases base's backing array).
func extend(base []binder, more ...binder) []binder {
	out := make([]binder, 0, len(base)+len(more))
	out = append(out, base...)
	out = append(out, more...)
	return out
}

// letSelf is the binder(s) a Let introduces for its `in` body.
func letSelf(e *ast.Let) []binder {
	if e.Name != "" {
		return []binder{{e.Name, e.Pos}}
	}
	return patBinders(e.Pat)
}

func paramBinders(params []ast.Param) []binder {
	var bs []binder
	for _, p := range params {
		switch p := p.(type) {
		case ast.VarParam:
			bs = append(bs, binder{p.Name, p.Pos})
		case ast.PatParam:
			bs = append(bs, patBinders(p.Pat)...)
		case ast.RecordParam:
			for _, f := range p.Fields {
				bs = append(bs, binder{f.Name, f.Pos})
			}
		}
	}
	return bs
}

func patBinders(pat ast.Pattern) []binder {
	switch p := pat.(type) {
	case ast.VarPat:
		return []binder{{p.Name, p.Pos}}
	case ast.CtorPat:
		var bs []binder
		for _, a := range p.Args {
			bs = append(bs, patBinders(a)...)
		}
		return bs
	case ast.TuplePat:
		var bs []binder
		for _, e := range p.Elems {
			bs = append(bs, patBinders(e)...)
		}
		return bs
	case ast.ListPat:
		var bs []binder
		for _, e := range p.Elems {
			bs = append(bs, patBinders(e)...)
		}
		return bs
	case ast.RecordPat:
		var bs []binder
		for _, f := range p.Fields {
			if f.Pat == nil {
				bs = append(bs, binder{f.Name, f.Pos}) // pun: binds the field name
			} else {
				bs = append(bs, patBinders(f.Pat)...)
			}
		}
		return bs
	}
	return nil
}

// topLevelLet finds a top-level value binding named name.
func topLevelLet(m *ast.Module, name string) (ast.Pos, bool) {
	for _, d := range m.Decls {
		if ld, ok := d.(*ast.LetDecl); ok && ld.Name == name {
			return ld.Pos, true
		}
	}
	return ast.Pos{}, false
}

// variantInModule finds a constructor named name among the module's type decls.
func variantInModule(m *ast.Module, name string) (ast.Pos, bool) {
	for _, d := range m.Decls {
		if td, ok := d.(*ast.TypeDecl); ok {
			for _, v := range td.Variants {
				if v.Name == name {
					return v.Pos, true
				}
			}
		}
	}
	return ast.Pos{}, false
}

// resolveImported is implemented in Task 4; in-file resolution returns false here.
func resolveImported(prog *load.Program, name string, isCtor bool) (Location, bool) {
	return Location{}, false
}
