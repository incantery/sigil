// Package parse builds an ast.Module from sigil core source, per docs/grammar.md.
package parse

import (
	"fmt"
	"strings"

	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/lex"
	"github.com/incantery/sigil/internal/token"
)

// Error is a parse error with source position.
type Error struct {
	Line, Col int
	Msg       string
}

func (e *Error) Error() string { return fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Msg) }

type parser struct {
	toks []token.Token
	pos  int
}

// Module lexes and parses a whole compilation unit.
func Module(src string) (*ast.Module, error) {
	toks, err := lex.Lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	return p.parseModule()
}

// Expr lexes and parses a single expression (used for ${...} interpolation).
func Expr(src string) (ast.Expr, error) {
	toks, err := lex.Lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	p.skipLayout()
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipLayout()
	if p.cur().Kind != token.EOF {
		return nil, p.errf("unexpected %s after interpolation expression", p.cur())
	}
	return e, nil
}

// --- token helpers ---

func (p *parser) cur() token.Token     { return p.toks[p.pos] }
func (p *parser) at(k token.Kind) bool { return p.cur().Kind == k }

func (p *parser) advance() token.Token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) accept(k token.Kind) bool {
	if p.at(k) {
		p.advance()
		return true
	}
	return false
}

func (p *parser) expect(k token.Kind) (token.Token, error) {
	if !p.at(k) {
		return token.Token{}, p.errf("expected %s, got %s", k, p.cur())
	}
	return p.advance(), nil
}

func (p *parser) errf(format string, a ...any) error {
	t := p.cur()
	return &Error{Line: t.Line, Col: t.Col, Msg: fmt.Sprintf(format, a...)}
}

func pos(t token.Token) ast.Pos { return ast.Pos{Line: t.Line, Col: t.Col} }

// skipLayout consumes any run of NEWLINE / INDENT / DEDENT tokens.
func (p *parser) skipLayout() {
	for p.at(token.NEWLINE) || p.at(token.INDENT) || p.at(token.DEDENT) {
		p.advance()
	}
}

// skipSeps consumes statement/declaration separators between top-level items.
func (p *parser) skipSeps() {
	for p.at(token.NEWLINE) || p.at(token.DEDENT) {
		p.advance()
	}
}

// --- module ---

func (p *parser) parseModule() (*ast.Module, error) {
	m := &ast.Module{}
	p.skipSeps()
	for !p.at(token.EOF) {
		if p.at(token.IMPORT) {
			imp, err := p.parseImport()
			if err != nil {
				return nil, err
			}
			m.Imports = append(m.Imports, imp)
		} else {
			d, err := p.parseDecl()
			if err != nil {
				return nil, err
			}
			m.Decls = append(m.Decls, d)
		}
		if !p.at(token.EOF) && !p.at(token.NEWLINE) && !p.at(token.DEDENT) {
			return nil, p.errf("expected newline or end of input, got %s", p.cur())
		}
		p.skipSeps()
	}
	return m, nil
}

func (p *parser) parseImport() (*ast.Import, error) {
	start := p.cur()
	p.advance() // import
	pathTok, err := p.expect(token.STRING)
	if err != nil {
		return nil, err
	}
	path, err := strLitValue(pathTok)
	if err != nil {
		return nil, err
	}
	imp := &ast.Import{Pos: pos(start), Path: path}
	switch {
	case p.accept(token.LPAREN):
		for !p.at(token.RPAREN) {
			// Imported names may be values/functions (lowercase) or
			// types/constructors (uppercase).
			if !p.at(token.IDENT) && !p.at(token.UIDENT) {
				return nil, p.errf("expected imported name, got %s", p.cur())
			}
			imp.Names = append(imp.Names, p.advance().Lit)
			if !p.accept(token.COMMA) {
				break
			}
		}
		if _, err := p.expect(token.RPAREN); err != nil {
			return nil, err
		}
	case p.accept(token.AS):
		alias, err := p.expect(token.UIDENT)
		if err != nil {
			return nil, err
		}
		imp.Alias = alias.Lit
	}
	return imp, nil
}

// --- declarations ---

func (p *parser) parseDecl() (ast.Decl, error) {
	pub := p.accept(token.PUB)
	switch p.cur().Kind {
	case token.LET:
		return p.parseLetDecl(pub)
	case token.TYPE:
		return p.parseTypeDecl(pub)
	case token.TEST:
		return p.parseTestDecl()
	default:
		return nil, p.errf("expected declaration, got %s", p.cur())
	}
}

func (p *parser) parseLetDecl(pub bool) (ast.Decl, error) {
	start := p.advance() // let
	rec := p.accept(token.REC)
	d := &ast.LetDecl{Pos: pos(start), Pub: pub, Rec: rec}
	if p.at(token.IDENT) {
		nameTok := p.advance()
		d.Name = nameTok.Lit
		d.NamePos = pos(nameTok)
		params, err := p.parseParams()
		if err != nil {
			return nil, err
		}
		d.Params = params
	} else {
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		d.Pat = pat
	}
	if _, err := p.expect(token.EQ); err != nil {
		return nil, err
	}
	body, err := p.parseBlockExpr()
	if err != nil {
		return nil, err
	}
	d.Body = body
	return d, nil
}

func (p *parser) parseTypeDecl(pub bool) (ast.Decl, error) {
	start := p.advance() // type
	name, err := p.expect(token.UIDENT)
	if err != nil {
		return nil, err
	}
	d := &ast.TypeDecl{Pos: pos(start), NamePos: pos(name), Pub: pub, Name: name.Lit}
	for p.at(token.IDENT) {
		d.Params = append(d.Params, p.advance().Lit)
	}
	if _, err := p.expect(token.EQ); err != nil {
		return nil, err
	}
	// Record type: `= { field : T, ... }`
	if p.at(token.LBRACE) {
		fields, err := p.parseRecordFieldTypes()
		if err != nil {
			return nil, err
		}
		d.Record = fields
		return d, nil
	}
	// Variants, possibly in an indented block, separated/prefixed by '|'.
	opened := false
	p.accept(token.NEWLINE)
	if p.accept(token.INDENT) {
		opened = true
	}
	for {
		for p.at(token.NEWLINE) {
			p.advance()
		}
		p.accept(token.PIPE) // optional leading/separating pipe
		ctorTok, err := p.expect(token.UIDENT)
		if err != nil {
			return nil, err
		}
		v := &ast.Variant{Pos: pos(ctorTok), Name: ctorTok.Lit}
		if p.accept(token.OF) {
			t, err := p.parseType()
			if err != nil {
				return nil, err
			}
			v.Arg = t
		}
		d.Variants = append(d.Variants, v)
		// Continue only if another '|' follows (across newlines).
		save := p.pos
		for p.at(token.NEWLINE) {
			p.advance()
		}
		if !p.at(token.PIPE) {
			p.pos = save
			break
		}
	}
	if opened {
		for p.at(token.NEWLINE) {
			p.advance()
		}
		if _, err := p.expect(token.DEDENT); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (p *parser) parseRecordFieldTypes() ([]*ast.FieldType, error) {
	if _, err := p.expect(token.LBRACE); err != nil {
		return nil, err
	}
	var fields []*ast.FieldType
	for !p.at(token.RBRACE) {
		name, err := p.expect(token.IDENT)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.COLON); err != nil {
			return nil, err
		}
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		fields = append(fields, &ast.FieldType{Pos: pos(name), Name: name.Lit, Type: t})
		if !p.accept(token.COMMA) {
			break
		}
	}
	if _, err := p.expect(token.RBRACE); err != nil {
		return nil, err
	}
	return fields, nil
}

// --- types ---

func (p *parser) parseType() (ast.TypeExpr, error) {
	left, err := p.parseTypeApp()
	if err != nil {
		return nil, err
	}
	if p.accept(token.ARROW) {
		right, err := p.parseType()
		if err != nil {
			return nil, err
		}
		return &ast.TyArrow{From: left, To: right}, nil
	}
	return left, nil
}

func (p *parser) parseTypeApp() (ast.TypeExpr, error) {
	if p.at(token.UIDENT) {
		name := p.advance().Lit
		con := &ast.TyCon{Name: name}
		for p.at(token.UIDENT) || p.at(token.IDENT) || p.at(token.LPAREN) || p.at(token.LBRACE) {
			arg, err := p.parseTypeAtom()
			if err != nil {
				return nil, err
			}
			con.Args = append(con.Args, arg)
		}
		return con, nil
	}
	return p.parseTypeAtom()
}

func (p *parser) parseTypeAtom() (ast.TypeExpr, error) {
	switch p.cur().Kind {
	case token.UIDENT:
		return &ast.TyCon{Name: p.advance().Lit}, nil
	case token.IDENT:
		return &ast.TyVar{Name: p.advance().Lit}, nil
	case token.LBRACE:
		fields, err := p.parseRecordFieldTypes()
		if err != nil {
			return nil, err
		}
		return &ast.TyRecord{Fields: fields}, nil
	case token.LPAREN:
		p.advance()
		if p.accept(token.RPAREN) {
			return &ast.TyTuple{}, nil // unit
		}
		first, err := p.parseType()
		if err != nil {
			return nil, err
		}
		if p.at(token.COMMA) {
			elems := []ast.TypeExpr{first}
			for p.accept(token.COMMA) {
				t, err := p.parseType()
				if err != nil {
					return nil, err
				}
				elems = append(elems, t)
			}
			if _, err := p.expect(token.RPAREN); err != nil {
				return nil, err
			}
			return &ast.TyTuple{Elems: elems}, nil
		}
		if _, err := p.expect(token.RPAREN); err != nil {
			return nil, err
		}
		return first, nil
	default:
		return nil, p.errf("expected type, got %s", p.cur())
	}
}

// --- block / statements ---

// parseBlockExpr parses either an inline expression or an INDENT..DEDENT block.
func (p *parser) parseBlockExpr() (ast.Expr, error) {
	if p.accept(token.INDENT) {
		e, err := p.parseStmts()
		if err != nil {
			return nil, err
		}
		for p.at(token.NEWLINE) {
			p.advance()
		}
		if _, err := p.expect(token.DEDENT); err != nil {
			return nil, err
		}
		return e, nil
	}
	return p.parseExpr()
}

// parseStmts parses a run of block-level `let`s ending in a single result expr.
func (p *parser) parseStmts() (ast.Expr, error) {
	if p.at(token.LET) {
		start := p.advance()
		rec := p.accept(token.REC)
		let := &ast.Let{Pos: pos(start), Rec: rec}
		if p.at(token.IDENT) {
			let.Name = p.advance().Lit
			params, err := p.parseParams()
			if err != nil {
				return nil, err
			}
			let.Params = params
		} else {
			pat, err := p.parsePattern()
			if err != nil {
				return nil, err
			}
			let.Pat = pat
		}
		if _, err := p.expect(token.EQ); err != nil {
			return nil, err
		}
		body, err := p.parseBlockExpr()
		if err != nil {
			return nil, err
		}
		let.Body = body
		if !p.accept(token.NEWLINE) {
			return nil, p.errf("expected continuation after let binding, got %s", p.cur())
		}
		rest, err := p.parseStmts()
		if err != nil {
			return nil, err
		}
		let.In = rest
		return let, nil
	}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return e, nil
}

// --- expressions ---

func (p *parser) parseExpr() (ast.Expr, error) {
	return p.parseBinop(0)
}

type opInfo struct {
	bp     int
	rassoc bool
}

var binops = map[token.Kind]opInfo{
	token.PIPEFWD: {1, false},
	token.OROR:    {2, false},
	token.ANDAND:  {3, false},
	token.EQEQ:    {4, false}, token.NEQ: {4, false},
	token.LT: {4, false}, token.GT: {4, false}, token.LE: {4, false}, token.GE: {4, false},
	token.CONCAT: {5, true},
	token.PLUS:   {6, false}, token.MINUS: {6, false},
	token.STAR: {7, false}, token.SLASH: {7, false}, token.PERCENT: {7, false},
}

func (p *parser) parseBinop(minbp int) (ast.Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		info, ok := binops[p.cur().Kind]
		if !ok || info.bp < minbp {
			return left, nil
		}
		opTok := p.advance()
		nextMin := info.bp + 1
		if info.rassoc {
			nextMin = info.bp
		}
		right, err := p.parseBinop(nextMin)
		if err != nil {
			return nil, err
		}
		left = &ast.Binop{Pos: pos(opTok), Op: opTok.Kind, L: left, R: right}
	}
}

func (p *parser) parseUnary() (ast.Expr, error) {
	if p.at(token.MINUS) || p.at(token.BANG) {
		opTok := p.advance()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.Unop{Pos: pos(opTok), Op: opTok.Kind, X: x}, nil
	}
	return p.parseApp()
}

func (p *parser) parseApp() (ast.Expr, error) {
	fn, err := p.parsePostfix()
	if err != nil {
		return nil, err
	}
	for canStartAtom(p.cur().Kind) {
		arg, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		fn = &ast.App{Pos: ast.Pos{}, Fn: fn, Arg: arg}
	}
	return fn, nil
}

func (p *parser) parsePostfix() (ast.Expr, error) {
	e, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	for p.at(token.DOT) {
		p.advance()
		name, err := p.expect(token.IDENT)
		if err != nil {
			return nil, err
		}
		e = &ast.Field{Pos: pos(name), Recv: e, Name: name.Lit}
	}
	return e, nil
}

func canStartAtom(k token.Kind) bool {
	switch k {
	case token.INT, token.FLOAT, token.STRING, token.IDENT, token.UIDENT,
		token.HOLE, token.LPAREN, token.LBRACK, token.LBRACE:
		return true
	}
	return false
}

func (p *parser) parseAtom() (ast.Expr, error) {
	t := p.cur()
	switch t.Kind {
	case token.INT:
		p.advance()
		return &ast.IntLit{Pos: pos(t), Raw: t.Lit}, nil
	case token.FLOAT:
		p.advance()
		return &ast.FloatLit{Pos: pos(t), Raw: t.Lit}, nil
	case token.STRING:
		p.advance()
		return p.buildString(t)
	case token.IDENT, token.HOLE:
		p.advance()
		return &ast.Var{Pos: pos(t), Name: t.Lit}, nil
	case token.UIDENT:
		p.advance()
		return &ast.Ctor{Pos: pos(t), Name: t.Lit}, nil
	case token.FUN:
		return p.parseLambda()
	case token.IF:
		return p.parseIf()
	case token.MATCH:
		return p.parseMatch()
	case token.EFFECT:
		return p.parseEffect()
	case token.LPAREN:
		return p.parseParenExpr()
	case token.LBRACK:
		return p.parseList()
	case token.LBRACE:
		return p.parseRecordLit()
	default:
		return nil, p.errf("expected expression, got %s", t)
	}
}

func (p *parser) parseParenExpr() (ast.Expr, error) {
	start := p.advance() // (
	if p.accept(token.RPAREN) {
		return &ast.Unit{Pos: pos(start)}, nil
	}
	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.at(token.COMMA) {
		elems := []ast.Expr{first}
		for p.accept(token.COMMA) {
			if p.at(token.RPAREN) { // trailing comma
				break
			}
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			elems = append(elems, e)
		}
		if _, err := p.expect(token.RPAREN); err != nil {
			return nil, err
		}
		return &ast.Tuple{Pos: pos(start), Elems: elems}, nil
	}
	if _, err := p.expect(token.RPAREN); err != nil {
		return nil, err
	}
	return first, nil
}

func (p *parser) parseList() (ast.Expr, error) {
	start := p.advance() // [
	list := &ast.ListLit{Pos: pos(start)}
	for !p.at(token.RBRACK) {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		list.Elems = append(list.Elems, e)
		if !p.accept(token.COMMA) {
			break
		}
	}
	if _, err := p.expect(token.RBRACK); err != nil {
		return nil, err
	}
	return list, nil
}

func (p *parser) parseRecordLit() (ast.Expr, error) {
	start := p.advance() // {
	rec := &ast.RecordLit{Pos: pos(start)}
	for !p.at(token.RBRACE) {
		name, err := p.expect(token.IDENT)
		if err != nil {
			return nil, err
		}
		fv := &ast.FieldVal{Name: name.Lit}
		if p.accept(token.EQ) {
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			fv.Value = v
		} else {
			fv.Value = &ast.Var{Pos: pos(name), Name: name.Lit} // punned
		}
		rec.Fields = append(rec.Fields, fv)
		if !p.accept(token.COMMA) {
			break
		}
	}
	if _, err := p.expect(token.RBRACE); err != nil {
		return nil, err
	}
	return rec, nil
}

// parseEffect parses `effect { e1; e2; ... }`. Layout is suspended inside the
// braces; statements are separated by ';' with an optional trailing ';'.
func (p *parser) parseEffect() (ast.Expr, error) {
	start := p.advance() // effect
	if _, err := p.expect(token.LBRACE); err != nil {
		return nil, err
	}
	eff := &ast.Effect{Pos: pos(start)}
	for !p.at(token.RBRACE) {
		s, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		eff.Stmts = append(eff.Stmts, s)
		if !p.accept(token.SEMI) {
			break
		}
	}
	if _, err := p.expect(token.RBRACE); err != nil {
		return nil, err
	}
	return eff, nil
}

// parseTestDecl parses `test "name" { stmt; stmt; ... }`. Like effect { },
// layout is suspended inside the braces, so statements are ';'-separated with an
// optional trailing ';'.
func (p *parser) parseTestDecl() (ast.Decl, error) {
	start := p.advance() // test
	nameTok, err := p.expect(token.STRING)
	if err != nil {
		return nil, err
	}
	name, err := strLitValue(nameTok)
	if err != nil {
		return nil, err
	}
	td := &ast.TestDecl{Pos: pos(start), NamePos: pos(nameTok), Name: name}
	if _, err := p.expect(token.LBRACE); err != nil {
		return nil, err
	}
	for !p.at(token.RBRACE) {
		s, err := p.parseTestStmt()
		if err != nil {
			return nil, err
		}
		td.Body = append(td.Body, s)
		if !p.accept(token.SEMI) {
			break
		}
	}
	if _, err := p.expect(token.RBRACE); err != nil {
		return nil, err
	}
	return td, nil
}

func (p *parser) parseTestStmt() (ast.TestStmt, error) {
	switch {
	case p.at(token.EXPECT):
		start := p.advance() // expect
		x, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.TestExpect{Pos: pos(start), X: x}, nil
	case p.at(token.LET):
		start := p.advance() // let
		nameTok, err := p.expect(token.IDENT)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.EQ); err != nil {
			return nil, err
		}
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.TestLet{Pos: pos(start), Name: nameTok.Lit, Value: v}, nil
	default:
		x, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.TestRun{X: x}, nil
	}
}

func (p *parser) parseLambda() (ast.Expr, error) {
	start := p.advance() // fun
	params, err := p.parseParams()
	if err != nil {
		return nil, err
	}
	if len(params) == 0 {
		return nil, p.errf("lambda needs at least one parameter")
	}
	if _, err := p.expect(token.ARROW); err != nil {
		return nil, err
	}
	body, err := p.parseBlockExpr()
	if err != nil {
		return nil, err
	}
	return &ast.Lambda{Pos: pos(start), Params: params, Body: body}, nil
}

func (p *parser) parseIf() (ast.Expr, error) {
	start := p.advance() // if
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.THEN); err != nil {
		return nil, err
	}
	thn, err := p.parseBlockExpr()
	if err != nil {
		return nil, err
	}
	// A block-form then-branch ends with a NEWLINE (synthesized by the dedent
	// back to the `if`'s level); skip it so `else` may begin on the next line.
	for p.at(token.NEWLINE) {
		p.advance()
	}
	if _, err := p.expect(token.ELSE); err != nil {
		return nil, err
	}
	els, err := p.parseBlockExpr()
	if err != nil {
		return nil, err
	}
	return &ast.If{Pos: pos(start), Cond: cond, Then: thn, Else: els}, nil
}

func (p *parser) parseMatch() (ast.Expr, error) {
	start := p.advance() // match
	scrut, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(token.WITH); err != nil {
		return nil, err
	}
	m := &ast.Match{Pos: pos(start), Scrut: scrut}
	opened := false
	p.accept(token.NEWLINE)
	if p.accept(token.INDENT) {
		opened = true
	}
	for {
		for p.at(token.NEWLINE) {
			p.advance()
		}
		if !p.at(token.PIPE) {
			break
		}
		p.advance() // |
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		arm := &ast.Arm{Pat: pat}
		if p.accept(token.IF) {
			g, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			arm.Guard = g
		}
		if _, err := p.expect(token.ARROW); err != nil {
			return nil, err
		}
		body, err := p.parseBlockExpr()
		if err != nil {
			return nil, err
		}
		arm.Body = body
		m.Arms = append(m.Arms, arm)
	}
	if len(m.Arms) == 0 {
		return nil, p.errf("match needs at least one arm")
	}
	if opened {
		if _, err := p.expect(token.DEDENT); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// --- params ---

func (p *parser) parseParams() ([]ast.Param, error) {
	var params []ast.Param
	for {
		switch p.cur().Kind {
		case token.IDENT:
			vt := p.advance()
			params = append(params, ast.VarParam{Pos: pos(vt), Name: vt.Lit})
		case token.UNDERSCORE:
			p.advance()
			params = append(params, ast.WildParam{})
		case token.LPAREN:
			p.advance()
			if p.accept(token.RPAREN) {
				params = append(params, ast.PatParam{Pat: ast.TuplePat{}}) // unit
				continue
			}
			pat, err := p.parseParenPatternRest()
			if err != nil {
				return nil, err
			}
			params = append(params, ast.PatParam{Pat: pat})
		case token.LBRACE:
			rp, err := p.parseRecordParam()
			if err != nil {
				return nil, err
			}
			params = append(params, rp)
		default:
			return params, nil
		}
	}
}

func (p *parser) parseRecordParam() (ast.Param, error) {
	p.advance() // {
	rp := ast.RecordParam{}
	for !p.at(token.RBRACE) {
		name, err := p.expect(token.IDENT)
		if err != nil {
			return nil, err
		}
		f := &ast.RecordParamField{Pos: pos(name), Name: name.Lit}
		if p.accept(token.EQ) {
			def, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			f.Default = def
		}
		rp.Fields = append(rp.Fields, f)
		if !p.accept(token.COMMA) {
			break
		}
	}
	if _, err := p.expect(token.RBRACE); err != nil {
		return nil, err
	}
	return rp, nil
}

// --- patterns ---

func (p *parser) parsePattern() (ast.Pattern, error) {
	if p.at(token.UIDENT) {
		nameTok := p.advance()
		cp := ast.CtorPat{Pos: pos(nameTok), Name: nameTok.Lit}
		for canStartPatternAtom(p.cur().Kind) {
			arg, err := p.parsePatternAtom()
			if err != nil {
				return nil, err
			}
			cp.Args = append(cp.Args, arg)
		}
		return cp, nil
	}
	return p.parsePatternAtom()
}

func canStartPatternAtom(k token.Kind) bool {
	switch k {
	case token.IDENT, token.UNDERSCORE, token.UIDENT, token.INT, token.FLOAT,
		token.STRING, token.LPAREN, token.LBRACK, token.LBRACE:
		return true
	}
	return false
}

func (p *parser) parsePatternAtom() (ast.Pattern, error) {
	t := p.cur()
	switch t.Kind {
	case token.IDENT:
		p.advance()
		return ast.VarPat{Pos: pos(t), Name: t.Lit}, nil
	case token.UNDERSCORE:
		p.advance()
		return ast.WildPat{}, nil
	case token.UIDENT:
		p.advance()
		return ast.CtorPat{Pos: pos(t), Name: t.Lit}, nil // nullary
	case token.INT:
		p.advance()
		return ast.IntPat{Raw: t.Lit}, nil
	case token.FLOAT:
		p.advance()
		return ast.FloatPat{Raw: t.Lit}, nil
	case token.STRING:
		p.advance()
		s, err := strLitValue(t)
		if err != nil {
			return nil, err
		}
		return ast.StrPat{Value: s}, nil
	case token.LPAREN:
		p.advance()
		if p.accept(token.RPAREN) {
			return ast.TuplePat{}, nil // unit
		}
		return p.parseParenPatternRest()
	case token.LBRACK:
		return p.parseListPattern()
	case token.LBRACE:
		return p.parseRecordPattern()
	default:
		return nil, p.errf("expected pattern, got %s", t)
	}
}

// parseParenPatternRest parses the rest of a '(' pattern after the '(' (and a
// non-')' lookahead) has been consumed: a group or a tuple.
func (p *parser) parseParenPatternRest() (ast.Pattern, error) {
	first, err := p.parsePattern()
	if err != nil {
		return nil, err
	}
	if p.at(token.COMMA) {
		elems := []ast.Pattern{first}
		for p.accept(token.COMMA) {
			if p.at(token.RPAREN) {
				break
			}
			e, err := p.parsePattern()
			if err != nil {
				return nil, err
			}
			elems = append(elems, e)
		}
		if _, err := p.expect(token.RPAREN); err != nil {
			return nil, err
		}
		return ast.TuplePat{Elems: elems}, nil
	}
	if _, err := p.expect(token.RPAREN); err != nil {
		return nil, err
	}
	return first, nil
}

func (p *parser) parseListPattern() (ast.Pattern, error) {
	p.advance() // [
	lp := ast.ListPat{}
	for !p.at(token.RBRACK) {
		e, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		lp.Elems = append(lp.Elems, e)
		if !p.accept(token.COMMA) {
			break
		}
	}
	if _, err := p.expect(token.RBRACK); err != nil {
		return nil, err
	}
	return lp, nil
}

func (p *parser) parseRecordPattern() (ast.Pattern, error) {
	p.advance() // {
	rp := ast.RecordPat{}
	for !p.at(token.RBRACE) {
		name, err := p.expect(token.IDENT)
		if err != nil {
			return nil, err
		}
		f := &ast.PatField{Pos: pos(name), Name: name.Lit}
		if p.accept(token.EQ) {
			sub, err := p.parsePattern()
			if err != nil {
				return nil, err
			}
			f.Pat = sub
		}
		rp.Fields = append(rp.Fields, f)
		if !p.accept(token.COMMA) {
			break
		}
	}
	if _, err := p.expect(token.RBRACE); err != nil {
		return nil, err
	}
	return rp, nil
}

// --- strings ---

func (p *parser) buildString(t token.Token) (ast.Expr, error) {
	hasExpr := false
	for _, s := range t.Segments {
		if s.IsExpr {
			hasExpr = true
		}
	}
	if !hasExpr {
		var b strings.Builder
		for _, s := range t.Segments {
			b.WriteString(s.Lit)
		}
		return &ast.StrLit{Pos: pos(t), Value: b.String()}, nil
	}
	interp := &ast.Interp{Pos: pos(t)}
	for _, s := range t.Segments {
		if s.IsExpr {
			e, err := Expr(s.Expr)
			if err != nil {
				return nil, err
			}
			interp.Parts = append(interp.Parts, e)
		} else {
			interp.Parts = append(interp.Parts, &ast.StrLit{Pos: pos(t), Value: s.Lit})
		}
	}
	return interp, nil
}

func strLitValue(t token.Token) (string, error) {
	var b strings.Builder
	for _, s := range t.Segments {
		if s.IsExpr {
			return "", &Error{Line: t.Line, Col: t.Col, Msg: "interpolation not allowed here"}
		}
		b.WriteString(s.Lit)
	}
	return b.String(), nil
}
