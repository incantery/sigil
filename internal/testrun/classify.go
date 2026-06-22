package testrun

import (
	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/browser"
	"github.com/incantery/sigil/internal/load"
)

// isBrowserProgram reports whether any module in the program's dependency
// closure references a browser intrinsic. Per-file routing: if so, the whole
// file runs in the browser.
func isBrowserProgram(prog *load.Program) bool {
	for _, m := range prog.Modules {
		if moduleUsesBrowser(m.AST) {
			return true
		}
	}
	return false
}

func moduleUsesBrowser(m *ast.Module) bool {
	found := false
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		if found || e == nil {
			return
		}
		if v, ok := e.(*ast.Var); ok && browser.IsBrowserIntrinsic(v.Name) {
			found = true
			return
		}
		for _, ch := range exprChildren(e) {
			walk(ch)
		}
	}
	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			walk(d.Body)
		case *ast.TestDecl:
			for _, s := range d.Body {
				switch s := s.(type) {
				case *ast.TestLet:
					walk(s.Value)
				case *ast.TestExpect:
					walk(s.X)
				case *ast.TestRun:
					walk(s.X)
				}
			}
		}
	}
	return found
}

// exprChildren mirrors internal/analysis children() — the sub-expressions of a
// node (no patterns/names).
func exprChildren(e ast.Expr) []ast.Expr {
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
