package ast

import (
	"fmt"
	"strings"

	"github.com/incantery/mako/core/token"
)

// Dump renders a module as a compact S-expression for structural testing.
// Positions are omitted so tests are robust to source layout.
func Dump(m *Module) string {
	var b strings.Builder
	for _, imp := range m.Imports {
		b.WriteString(dumpImport(imp))
		b.WriteByte('\n')
	}
	for _, d := range m.Decls {
		b.WriteString(dumpDecl(d))
		b.WriteByte('\n')
	}
	return b.String()
}

func dumpImport(i *Import) string {
	s := fmt.Sprintf("(import %q", i.Path)
	if len(i.Names) > 0 {
		s += " (" + strings.Join(i.Names, " ") + ")"
	}
	if i.Alias != "" {
		s += " as " + i.Alias
	}
	return s + ")"
}

func dumpDecl(d Decl) string {
	switch d := d.(type) {
	case *LetDecl:
		head := "let"
		if d.Pub {
			head = "pub-let"
		}
		if d.Rec {
			head += "-rec"
		}
		target := d.Name
		if d.Name == "" {
			target = dumpPat(d.Pat)
		}
		ps := dumpParams(d.Params)
		return fmt.Sprintf("(%s %s%s %s)", head, target, ps, DumpExpr(d.Body))
	case *TypeDecl:
		head := "type"
		if d.Pub {
			head = "pub-type"
		}
		name := d.Name
		if len(d.Params) > 0 {
			name += " " + strings.Join(d.Params, " ")
		}
		if d.Record != nil {
			return fmt.Sprintf("(%s %s (record%s))", head, name, dumpFieldTypes(d.Record))
		}
		var vs []string
		for _, v := range d.Variants {
			if v.Arg != nil {
				vs = append(vs, fmt.Sprintf("(%s %s)", v.Name, dumpType(v.Arg)))
			} else {
				vs = append(vs, v.Name)
			}
		}
		return fmt.Sprintf("(%s %s %s)", head, name, strings.Join(vs, " "))
	default:
		return "(?decl)"
	}
}

func dumpFieldTypes(fs []*FieldType) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(fmt.Sprintf(" (%s %s)", f.Name, dumpType(f.Type)))
	}
	return b.String()
}

func dumpType(t TypeExpr) string {
	switch t := t.(type) {
	case *TyVar:
		return t.Name
	case *TyCon:
		if len(t.Args) == 0 {
			return t.Name
		}
		parts := []string{t.Name}
		for _, a := range t.Args {
			parts = append(parts, dumpType(a))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case *TyArrow:
		return fmt.Sprintf("(-> %s %s)", dumpType(t.From), dumpType(t.To))
	case *TyTuple:
		if len(t.Elems) == 0 {
			return "Unit"
		}
		parts := []string{"tuple"}
		for _, e := range t.Elems {
			parts = append(parts, dumpType(e))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case *TyRecord:
		return "(record-ty" + dumpFieldTypes(t.Fields) + ")"
	default:
		return "?ty"
	}
}

func dumpParams(ps []Param) string {
	if len(ps) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range ps {
		b.WriteByte(' ')
		b.WriteString(dumpParam(p))
	}
	return b.String()
}

func dumpParam(p Param) string {
	switch p := p.(type) {
	case VarParam:
		return p.Name
	case WildParam:
		return "_"
	case PatParam:
		return dumpPat(p.Pat)
	case RecordParam:
		var fs []string
		for _, f := range p.Fields {
			if f.Default != nil {
				fs = append(fs, fmt.Sprintf("%s=%s", f.Name, DumpExpr(f.Default)))
			} else {
				fs = append(fs, f.Name)
			}
		}
		return "{" + strings.Join(fs, " ") + "}"
	default:
		return "?param"
	}
}

func dumpPat(p Pattern) string {
	switch p := p.(type) {
	case VarPat:
		return p.Name
	case WildPat:
		return "_"
	case CtorPat:
		if len(p.Args) == 0 {
			return p.Name
		}
		parts := []string{p.Name}
		for _, a := range p.Args {
			parts = append(parts, dumpPat(a))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case TuplePat:
		if len(p.Elems) == 0 {
			return "unit"
		}
		parts := []string{"tuple"}
		for _, e := range p.Elems {
			parts = append(parts, dumpPat(e))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case ListPat:
		parts := []string{"list"}
		for _, e := range p.Elems {
			parts = append(parts, dumpPat(e))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case RecordPat:
		var fs []string
		for _, f := range p.Fields {
			if f.Pat != nil {
				fs = append(fs, fmt.Sprintf("%s=%s", f.Name, dumpPat(f.Pat)))
			} else {
				fs = append(fs, f.Name)
			}
		}
		return "{" + strings.Join(fs, " ") + "}"
	case IntPat:
		return p.Raw
	case FloatPat:
		return p.Raw
	case StrPat:
		return fmt.Sprintf("%q", p.Value)
	default:
		return "?pat"
	}
}

// DumpExpr renders a single expression as an S-expression.
func DumpExpr(e Expr) string {
	switch e := e.(type) {
	case *IntLit:
		return e.Raw
	case *FloatLit:
		return e.Raw
	case *StrLit:
		return fmt.Sprintf("%q", e.Value)
	case *Interp:
		parts := []string{"interp"}
		for _, pt := range e.Parts {
			parts = append(parts, DumpExpr(pt))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case *Var:
		return e.Name
	case *Ctor:
		return e.Name
	case *Unit:
		return "unit"
	case *Tuple:
		parts := []string{"tuple"}
		for _, el := range e.Elems {
			parts = append(parts, DumpExpr(el))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case *ListLit:
		parts := []string{"list"}
		for _, el := range e.Elems {
			parts = append(parts, DumpExpr(el))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case *RecordLit:
		var fs []string
		for _, f := range e.Fields {
			fs = append(fs, fmt.Sprintf("%s=%s", f.Name, DumpExpr(f.Value)))
		}
		return "{" + strings.Join(fs, " ") + "}"
	case *Lambda:
		return fmt.Sprintf("(fun%s %s)", dumpParams(e.Params), DumpExpr(e.Body))
	case *App:
		return fmt.Sprintf("(app %s %s)", DumpExpr(e.Fn), DumpExpr(e.Arg))
	case *Field:
		return fmt.Sprintf("(. %s %s)", DumpExpr(e.Recv), e.Name)
	case *Binop:
		return fmt.Sprintf("(%s %s %s)", token.Kind(e.Op), DumpExpr(e.L), DumpExpr(e.R))
	case *Unop:
		return fmt.Sprintf("(u%s %s)", token.Kind(e.Op), DumpExpr(e.X))
	case *If:
		return fmt.Sprintf("(if %s %s %s)", DumpExpr(e.Cond), DumpExpr(e.Then), DumpExpr(e.Else))
	case *Match:
		parts := []string{"match", DumpExpr(e.Scrut)}
		for _, a := range e.Arms {
			if a.Guard != nil {
				parts = append(parts, fmt.Sprintf("(%s if %s -> %s)", dumpPat(a.Pat), DumpExpr(a.Guard), DumpExpr(a.Body)))
			} else {
				parts = append(parts, fmt.Sprintf("(%s -> %s)", dumpPat(a.Pat), DumpExpr(a.Body)))
			}
		}
		return "(" + strings.Join(parts, " ") + ")"
	case *Let:
		target := e.Name
		if e.Name == "" {
			target = dumpPat(e.Pat)
		}
		kw := "let"
		if e.Rec {
			kw = "let-rec"
		}
		return fmt.Sprintf("(%s %s%s %s %s)", kw, target, dumpParams(e.Params), DumpExpr(e.Body), DumpExpr(e.In))
	case *Effect:
		parts := []string{"effect"}
		for _, s := range e.Stmts {
			parts = append(parts, DumpExpr(s))
		}
		return "(" + strings.Join(parts, " ") + ")"
	default:
		return "?expr"
	}
}
