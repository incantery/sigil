package analysis

import (
	"strings"

	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/lex"
	"github.com/incantery/sigil/internal/parse"
	"github.com/incantery/sigil/internal/token"
)

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

// Legend indices for non-role token types (roles 0..5 are their own indices).
const (
	legendKeyword  = 6
	legendOperator = 7
	legendNumber   = 8
	legendString   = 9
)

// SemanticTokens returns the LSP semanticTokens/full `data` array for the source:
// five ints per emitted token (deltaLine, deltaStartChar, length, tokenType,
// modifiers=0). A parse or lex error yields an empty slice.
func SemanticTokens(text string) []uint {
	m, err := parse.Module(text)
	if err != nil {
		return []uint{}
	}
	toks, err := lex.Lex(text)
	if err != nil {
		return []uint{}
	}
	roles := SemanticRoles(m)
	fns := collectFunctionNames(m)
	lines := strings.Split(text, "\n")

	data := []uint{}
	prevLine, prevCol := 0, 0
	for _, t := range toks {
		typ, ok := semanticType(t, roles, fns)
		if !ok {
			continue
		}
		line := t.Line - 1 // 0-based
		col := t.Col - 1
		length := tokenLength(t, lines)
		dLine := line - prevLine
		dCol := col
		if dLine == 0 {
			dCol = col - prevCol
		}
		data = append(data, uint(dLine), uint(dCol), uint(length), uint(typ), 0)
		prevLine, prevCol = line, col
	}
	return data
}

// semanticType maps a token to its legend index, or ok=false to skip it.
func semanticType(t token.Token, roles map[ast.Pos]Role, fns map[string]bool) (int, bool) {
	switch t.Kind {
	case token.IDENT, token.UIDENT, token.HOLE:
		if r, ok := roles[ast.Pos{Line: t.Line, Col: t.Col}]; ok {
			return int(r), true
		}
		if t.Kind == token.UIDENT {
			return int(RoleType), true
		}
		if fns[t.Lit] {
			return int(RoleFunction), true
		}
		return int(RoleVariable), true
	case token.INT, token.FLOAT:
		return legendNumber, true
	case token.STRING:
		return legendString, true
	}
	if t.Kind >= token.LET && t.Kind <= token.EXPECT {
		return legendKeyword, true
	}
	if (t.Kind >= token.PIPEFWD && t.Kind <= token.BANG) ||
		t.Kind == token.EQ || t.Kind == token.ARROW || t.Kind == token.PIPE {
		return legendOperator, true
	}
	return 0, false // layout, punctuation, EOF, UNDERSCORE
}

// tokenLength returns a token's source length in characters.
func tokenLength(t token.Token, lines []string) int {
	if t.Kind == token.STRING {
		return stringLength(lines, t.Line, t.Col)
	}
	if t.Lit != "" {
		return len([]rune(t.Lit))
	}
	return len([]rune(t.Kind.String())) // keywords/operators: canonical spelling
}

// stringLength measures a single-line string literal (sigil forbids raw newlines
// in strings) from its opening quote at 1-based (line,col), counting both quotes
// and honoring backslash escapes.
func stringLength(lines []string, line, col int) int {
	if line-1 < 0 || line-1 >= len(lines) {
		return 2
	}
	rs := []rune(lines[line-1])
	i := col - 1 // index of the opening quote
	if i < 0 || i >= len(rs) || rs[i] != '"' {
		return 2
	}
	n := 1 // opening quote
	for j := i + 1; j < len(rs); j++ {
		n++
		if rs[j] == '\\' && j+1 < len(rs) {
			j++
			n++
			continue
		}
		if rs[j] == '"' {
			break // closing quote counted
		}
	}
	return n
}
