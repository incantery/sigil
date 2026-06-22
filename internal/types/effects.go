package types

import (
	"fmt"

	"github.com/incantery/sigil/internal/ast"
)

// effectOps are the intrinsics that perform an effect. They may only be used
// lexically inside an effect { } block. (Reads like __get are pure and omitted.)
var effectOps = map[string]bool{
	"__set":        true,
	"__effect":     true,
	"__mount":      true,
	"__fetch":      true,
	"__pushPath":   true,
	"__onPopState": true,
	// Browser driver actions (Sigil Browser SP1). Gated as effects;
	// __domText is a read and is intentionally omitted.
	"__navigate":    true,
	"__click":       true,
	"__fill":        true,
	"__waitVisible": true,
}

// checkEffects enforces the effect-context discipline: a reference to an effect
// operation is legal only when lexically nested inside at least one effect { }
// block. Building an effect block is pure; the runtime runs it later.
func checkEffects(m *ast.Module) error {
	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			if err := walkEffect(d.Body, 0); err != nil {
				return err
			}
			if err := walkParamDefaults(d.Params); err != nil {
				return err
			}
		case *ast.TestDecl:
			for _, s := range d.Body {
				if err := walkTestStmt(s); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// walkTestStmt checks a test statement at effect-depth 1: a test body is an
// effect context, so effect ops (__set, __effect, ...) are legal inside it.
func walkTestStmt(s ast.TestStmt) error {
	switch s := s.(type) {
	case *ast.TestLet:
		return walkEffect(s.Value, 1)
	case *ast.TestExpect:
		return walkEffect(s.X, 1)
	case *ast.TestRun:
		return walkEffect(s.X, 1)
	}
	return nil
}

func walkParamDefaults(params []ast.Param) error {
	for _, p := range params {
		if rp, ok := p.(ast.RecordParam); ok {
			for _, f := range rp.Fields {
				if f.Default != nil {
					if err := walkEffect(f.Default, 0); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// walkEffect recursively checks e. depth is the number of enclosing effect { }
// blocks; an effect op is allowed only when depth > 0.
func walkEffect(e ast.Expr, depth int) error {
	switch e := e.(type) {
	case *ast.Var:
		if effectOps[e.Name] && depth == 0 {
			return &Error{Line: e.Pos.Line, Col: e.Pos.Col,
				Msg: fmt.Sprintf("effect operation %q used outside an effect { } block", e.Name)}
		}
		return nil
	case *ast.Effect:
		for _, s := range e.Stmts {
			if err := walkEffect(s, depth+1); err != nil {
				return err
			}
		}
		return nil
	case *ast.Interp:
		return walkAll(e.Parts, depth)
	case *ast.Tuple:
		return walkAll(e.Elems, depth)
	case *ast.ListLit:
		return walkAll(e.Elems, depth)
	case *ast.RecordLit:
		for _, f := range e.Fields {
			if err := walkEffect(f.Value, depth); err != nil {
				return err
			}
		}
		return nil
	case *ast.Field:
		return walkEffect(e.Recv, depth)
	case *ast.Lambda:
		if err := walkParamDefaults(e.Params); err != nil {
			return err
		}
		return walkEffect(e.Body, depth)
	case *ast.App:
		if err := walkEffect(e.Fn, depth); err != nil {
			return err
		}
		return walkEffect(e.Arg, depth)
	case *ast.Binop:
		if err := walkEffect(e.L, depth); err != nil {
			return err
		}
		return walkEffect(e.R, depth)
	case *ast.Unop:
		return walkEffect(e.X, depth)
	case *ast.If:
		if err := walkEffect(e.Cond, depth); err != nil {
			return err
		}
		if err := walkEffect(e.Then, depth); err != nil {
			return err
		}
		return walkEffect(e.Else, depth)
	case *ast.Match:
		if err := walkEffect(e.Scrut, depth); err != nil {
			return err
		}
		for _, arm := range e.Arms {
			if arm.Guard != nil {
				if err := walkEffect(arm.Guard, depth); err != nil {
					return err
				}
			}
			if err := walkEffect(arm.Body, depth); err != nil {
				return err
			}
		}
		return nil
	case *ast.Let:
		if err := walkParamDefaults(e.Params); err != nil {
			return err
		}
		if err := walkEffect(e.Body, depth); err != nil {
			return err
		}
		return walkEffect(e.In, depth)
	default:
		// Leaves: IntLit, FloatLit, StrLit, Unit, Ctor — nothing to check.
		return nil
	}
}

func walkAll(es []ast.Expr, depth int) error {
	for _, e := range es {
		if err := walkEffect(e, depth); err != nil {
			return err
		}
	}
	return nil
}
