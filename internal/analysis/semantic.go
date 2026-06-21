package analysis

import "github.com/incantery/sigil/internal/ast"

// Role is a semantic-token classification for an identifier. The numeric values
// are also the legend indices for those roles (see SemanticTokens).
type Role int

const (
	RoleType       Role = iota // 0
	RoleEnumMember             // 1
	RoleFunction               // 2
	RoleParameter              // 3
	RoleVariable               // 4
	RoleProperty               // 5
)

// collectFunctionNames returns the set of names bound to a function (a let with
// parameters) anywhere in the module — used to color lowercase *uses*.
func collectFunctionNames(m *ast.Module) map[string]bool {
	fns := map[string]bool{}
	var visit func(e ast.Expr)
	visit = func(e ast.Expr) {
		switch e := e.(type) {
		case *ast.Let:
			if e.Name != "" && len(e.Params) > 0 {
				fns[e.Name] = true
			}
			visit(e.Body)
			visit(e.In)
		case *ast.Lambda:
			visit(e.Body)
		default:
			for _, ch := range children(e) {
				visit(ch)
			}
		}
	}
	for _, d := range m.Decls {
		if ld, ok := d.(*ast.LetDecl); ok {
			if ld.Name != "" && len(ld.Params) > 0 {
				fns[ld.Name] = true
			}
			if ld.Body != nil {
				visit(ld.Body)
			}
		}
	}
	return fns
}

// SemanticRoles returns position → role for every identifier occurrence the
// structure classifies. Identifiers not present here fall back to the lexical
// default in SemanticTokens. No type information is used.
func SemanticRoles(m *ast.Module) map[ast.Pos]Role {
	fns := collectFunctionNames(m)
	roles := map[ast.Pos]Role{}
	put := func(p ast.Pos, r Role) {
		if p.Line != 0 { // skip zero positions (unset)
			roles[p] = r
		}
	}

	var visitExpr func(e ast.Expr)
	var visitPat func(p ast.Pattern)

	visitParams := func(params []ast.Param) {
		for _, p := range params {
			switch p := p.(type) {
			case ast.VarParam:
				put(p.Pos, RoleParameter)
			case ast.PatParam:
				visitPat(p.Pat)
			case ast.RecordParam:
				for _, f := range p.Fields {
					put(f.Pos, RoleParameter)
				}
			}
		}
	}

	visitPat = func(p ast.Pattern) {
		switch p := p.(type) {
		case ast.VarPat:
			put(p.Pos, RoleVariable)
		case ast.CtorPat:
			put(p.Pos, RoleEnumMember)
			for _, a := range p.Args {
				visitPat(a)
			}
		case ast.TuplePat:
			for _, e := range p.Elems {
				visitPat(e)
			}
		case ast.ListPat:
			for _, e := range p.Elems {
				visitPat(e)
			}
		case ast.RecordPat:
			for _, f := range p.Fields {
				if f.Pat == nil {
					put(f.Pos, RoleVariable)
				} else {
					visitPat(f.Pat)
				}
			}
		}
	}

	visitExpr = func(e ast.Expr) {
		switch e := e.(type) {
		case *ast.Var:
			if fns[e.Name] {
				put(e.Pos, RoleFunction)
			} else {
				put(e.Pos, RoleVariable)
			}
		case *ast.Ctor:
			put(e.Pos, RoleEnumMember)
		case *ast.Field:
			put(e.Pos, RoleProperty) // Field.Pos is the field name position
			visitExpr(e.Recv)
		case *ast.Lambda:
			visitParams(e.Params)
			visitExpr(e.Body)
		case *ast.Let:
			// block-let name uses the lexical fallback (no NamePos); bind params.
			visitParams(e.Params)
			if e.Name == "" {
				visitPat(e.Pat)
			}
			visitExpr(e.Body)
			visitExpr(e.In)
		case *ast.Match:
			visitExpr(e.Scrut)
			for _, arm := range e.Arms {
				visitPat(arm.Pat)
				if arm.Guard != nil {
					visitExpr(arm.Guard)
				}
				visitExpr(arm.Body)
			}
		default:
			for _, ch := range children(e) {
				visitExpr(ch)
			}
		}
	}

	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			if d.Name != "" {
				if len(d.Params) > 0 {
					put(d.NamePos, RoleFunction)
				} else {
					put(d.NamePos, RoleVariable)
				}
			} else {
				visitPat(d.Pat)
			}
			visitParams(d.Params)
			if d.Body != nil {
				visitExpr(d.Body)
			}
		case *ast.TypeDecl:
			put(d.NamePos, RoleType)
			for _, v := range d.Variants {
				put(v.Pos, RoleEnumMember)
			}
			for _, f := range d.Record {
				put(f.Pos, RoleProperty)
			}
		}
	}
	return roles
}
