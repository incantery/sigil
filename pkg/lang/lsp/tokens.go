package lsp

import (
	"regexp"
	"sort"
	"strings"

	"github.com/incantery/sigil/pkg/lang/ast"
	"github.com/incantery/sigil/pkg/lang/lower"
)

// Semantic tokens are produced from the same AST the compiler uses, so
// editor highlighting can never drift from what the parser actually
// accepts — the agent-first version of "the compiler is the source of
// truth". Tree-sitter / TextMate provide the lexical baseline; these
// tokens add the identifier classification only the compiler knows
// (builtin kind vs user component, state cell vs param, tone names…).

// Token type indexes into tokenTypeNames. Standard LSP names — themes
// map them without configuration.
const (
	tokNamespace = iota
	tokType
	tokClass
	tokEnumMember
	tokParameter
	tokVariable
	tokProperty
	tokEvent
	tokFunction
	tokMethod
	tokMacro
	tokKeyword
	tokComment
	tokString
	tokNumber
)

var tokenTypeNames = []string{
	"namespace", "type", "class", "enumMember", "parameter", "variable",
	"property", "event", "function", "method", "macro", "keyword",
	"comment", "string", "number",
}

const (
	modDeclaration    = 1 << 0
	modDefaultLibrary = 1 << 1
)

var tokenModifierNames = []string{"declaration", "defaultLibrary"}

// tok is one semantic token in parser coordinates (1-based line,
// 1-based byte column). name carries the identifier text (dotted
// chains included) so definition/hover can reuse the same walk.
type tok struct {
	line, col, length int
	typ               int
	mods              int
	name              string
}

// ───────────── public entry points ──────────────────────────────────

// encodeTokens runs the walk and delta-encodes per the LSP spec.
func encodeTokens(doc *document) []int {
	toks := buildTokens(doc)
	data := make([]int, 0, len(toks)*5)
	prevLine, prevCol := 0, 0 // 0-based line, utf16 col
	for _, t := range toks {
		l := t.line - 1
		if l < 0 || l >= len(doc.lines) {
			continue
		}
		c := utf16Col(doc.lines[l], t.col)
		length := utf16Col(doc.lines[l], t.col+t.length) - c
		if length <= 0 {
			continue
		}
		dl := l - prevLine
		dc := c
		if dl == 0 {
			dc = c - prevCol
		}
		data = append(data, dl, dc, length, t.typ, t.mods)
		prevLine, prevCol = l, c
	}
	return data
}

// buildTokens walks the AST + rescans lines for the few keywords the
// AST doesn't record (handler `on`, `extends`, `as`, …) and comments.
func buildTokens(doc *document) []tok {
	w := &tokWalker{
		lines:    doc.lines,
		builtins: builtinKindSet(),
	}
	w.walkRoot(doc.an.root)
	w.scanComments()

	sort.SliceStable(w.toks, func(i, j int) bool {
		a, b := w.toks[i], w.toks[j]
		if a.line != b.line {
			return a.line < b.line
		}
		return a.col < b.col
	})
	// Drop exact-position duplicates (e.g. a rescan re-finding an AST
	// token); first wins.
	out := w.toks[:0]
	for i, t := range w.toks {
		if i > 0 && t.line == out[len(out)-1].line && t.col == out[len(out)-1].col {
			continue
		}
		out = append(out, t)
	}
	return out
}

var builtinsOnce struct {
	set map[string]bool
}

func builtinKindSet() map[string]bool {
	if builtinsOnce.set == nil {
		set := map[string]bool{}
		for _, k := range lower.BuiltinKinds() {
			set[k] = true
		}
		builtinsOnce.set = set
	}
	return builtinsOnce.set
}

// ───────────── walker ────────────────────────────────────────────────

type tokWalker struct {
	lines    []string
	toks     []tok
	builtins map[string]bool

	masked     map[int]string // line number → string-masked, comment-stripped copy
	commentCol map[int]int    // line number → 1-based comment start col (0 = none)
}

func (w *tokWalker) emit(line, col, length, typ, mods int, name string) {
	if length <= 0 || line < 1 {
		return
	}
	w.toks = append(w.toks, tok{line: line, col: col, length: length, typ: typ, mods: mods, name: name})
}

func (w *tokWalker) emitIdent(v ast.Value, typ, mods int) {
	w.emit(v.Pos.Line, v.Pos.Col, len(v.String), typ, mods, v.String)
}

func (w *tokWalker) emitQuoted(v ast.Value) {
	w.emit(v.Pos.Line, v.Pos.Col, w.stringSrcLen(v), tokString, 0, "")
}

func (w *tokWalker) emitInt(v ast.Value) {
	w.emit(v.Pos.Line, v.Pos.Col, w.intSrcLen(v), tokNumber, 0, "")
}

func (w *tokWalker) kw(line, col, length int) {
	w.emit(line, col, length, tokKeyword, 0, "")
}

func (w *tokWalker) walkRoot(root *ast.Node) {
	if root == nil {
		return
	}
	decls := []*ast.Node{root}
	if root.Kind == "module" || root.Kind == "__root__" {
		decls = root.Children
	}
	for _, d := range decls {
		w.walkTopDecl(d)
	}
}

func (w *tokWalker) walkTopDecl(n *ast.Node) {
	switch n.Kind {
	case "__error__":
		return

	case "view", "app", "component":
		w.kw(n.Pos.Line, n.Pos.Col, len(n.Kind))
		nameTyp := tokClass
		if n.Kind == "component" {
			nameTyp = tokFunction
		}
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], nameTyp, modDeclaration)
		}
		for _, p := range n.Args[1:] {
			w.emitIdent(p, tokParameter, modDeclaration)
		}
		// Param type annotations are parsed and discarded; recover them
		// from the signature line so `: Type` still highlights.
		w.rescanParamTypes(n.Pos.Line, n.Pos.Col)
		w.walkBody(n.Children)

	case "state":
		w.walkState(n)

	case "theme":
		w.kw(n.Pos.Line, n.Pos.Col, len(n.Kind))
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokClass, modDeclaration)
		}
		if base, ok := n.Kwargs["extends"]; ok {
			if col := w.findWord(n.Pos.Line, n.Pos.Col, "extends"); col > 0 {
				w.kw(n.Pos.Line, col, len("extends"))
			}
			w.emitIdent(base, tokClass, 0)
		}
		for _, c := range n.Children {
			w.walkThemeBinding(c)
		}

	case "test", "story":
		w.kw(n.Pos.Line, n.Pos.Col, len(n.Kind))
		if len(n.Args) > 0 {
			w.emitQuoted(n.Args[0])
		}
		w.walkBody(n.Children)

	case "type":
		w.kw(n.Pos.Line, n.Pos.Col, len(n.Kind))
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokType, modDeclaration)
		}
		for _, c := range n.Children {
			switch c.Kind {
			case "field_decl":
				w.walkFieldDecl(c, tokProperty)
			case "variant_decl":
				if len(c.Args) > 0 {
					w.emitIdent(c.Args[0], tokEnumMember, modDeclaration)
				}
				// `| name : Type` — the optional payload type ref.
				if len(c.Args) > 1 {
					w.emitTypeRef(c.Args[1])
				}
			}
		}

	case "query", "command", "stream":
		w.kw(n.Pos.Line, n.Pos.Col, len(n.Kind))
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokFunction, modDeclaration)
		}
		if len(n.Args) > 1 {
			w.emitTypeRef(n.Args[1]) // return type
		}
		for _, c := range n.Children {
			switch c.Kind {
			case "field_decl":
				w.walkFieldDecl(c, tokParameter)
			case "op-backend":
				if col := w.findWord(n.Pos.Line, n.Pos.Col, "backend"); col > 0 {
					w.kw(n.Pos.Line, col, len("backend"))
				}
				if len(c.Args) > 0 {
					w.emitIdent(c.Args[0], tokNamespace, 0)
				}
			case "invalidates":
				if col := w.findWord(n.Pos.Line, n.Pos.Col, "invalidates"); col > 0 {
					w.kw(n.Pos.Line, col, len("invalidates"))
				}
				for _, q := range c.Args {
					w.emitIdent(q, tokFunction, 0)
				}
			}
		}

	case "import":
		w.kw(n.Pos.Line, n.Pos.Col, len(n.Kind))
		if len(n.Args) > 0 {
			// Module path: unquoted in source, so source length ==
			// string length even though the value kind is "string".
			p := n.Args[0]
			w.emit(p.Pos.Line, p.Pos.Col, len(p.String), tokNamespace, 0, p.String)
		}
		if len(n.Args) > 1 {
			if col := w.findWord(n.Pos.Line, n.Pos.Col, "as"); col > 0 {
				w.kw(n.Pos.Line, col, len("as"))
			}
			w.emitIdent(n.Args[1], tokNamespace, modDeclaration)
		}

	case "icons":
		w.kw(n.Pos.Line, n.Pos.Col, len(n.Kind))
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokNamespace, modDeclaration)
		}
		for _, c := range n.Children {
			if c.Kind == "icon-target" && len(c.Args) >= 2 {
				w.emitIdent(c.Args[0], tokEnumMember, 0)
				w.emitQuoted(c.Args[1])
			}
		}

	case "fonts":
		w.kw(n.Pos.Line, n.Pos.Col, len(n.Kind))
		for i, a := range n.Args {
			if i == 0 {
				w.emitIdent(a, tokEnumMember, 0)
				continue
			}
			w.emitQuoted(a)
		}

	case "backend":
		w.kw(n.Pos.Line, n.Pos.Col, len(n.Kind))
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokNamespace, modDeclaration)
		}
		for _, c := range n.Children {
			w.walkBackendBinding(c)
		}

	case "session":
		w.kw(n.Pos.Line, n.Pos.Col, len(n.Kind))
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokNamespace, modDeclaration)
		}
		w.walkBody(n.Children)

	default:
		// A top-level invocation (e.g. the inline body of a story) walks
		// like any body line.
		w.walkBodyNode(n)
	}
}

// walkBody walks view/component/test/story body lines.
func (w *tokWalker) walkBody(children []*ast.Node) {
	for _, c := range children {
		w.walkBodyNode(c)
	}
}

func (w *tokWalker) walkBodyNode(n *ast.Node) {
	switch n.Kind {
	case "__error__":
		return

	case "state":
		w.walkState(n)

	case "for":
		w.kw(n.Pos.Line, n.Pos.Col, len("for"))
		// Args are [var, "in", list]; kwargs (`filter=…`) may follow.
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokVariable, modDeclaration)
		}
		if len(n.Args) > 1 && n.Args[1].String == "in" {
			w.kw(n.Args[1].Pos.Line, n.Args[1].Pos.Col, len("in"))
		}
		if len(n.Args) > 2 {
			w.emitIdent(n.Args[2], tokVariable, 0)
		}
		w.walkKwargs(n)
		w.walkBody(n.Children)

	case "if":
		w.kw(n.Pos.Line, n.Pos.Col, len("if"))
		for _, a := range n.Args {
			if a.Kind == ast.ValueIdent {
				w.emitIdent(a, tokVariable, 0)
			}
		}
		w.walkBody(n.Children)

	case "match":
		w.kw(n.Pos.Line, n.Pos.Col, len("match"))
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokVariable, 0) // subject cell
		}
		for _, arm := range n.Children {
			if arm.Kind != "match_arm" {
				continue
			}
			// `| variant [as bind]`
			if len(arm.Args) > 0 {
				w.emitIdent(arm.Args[0], tokEnumMember, 0)
			}
			if len(arm.Args) > 1 {
				if col := w.findWord(arm.Args[0].Pos.Line, arm.Args[0].Pos.Col, "as"); col > 0 {
					w.kw(arm.Args[0].Pos.Line, col, len("as"))
				}
				w.emitIdent(arm.Args[1], tokVariable, modDeclaration)
			}
			w.walkBody(arm.Children)
		}

	case "splice":
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokParameter, 0)
		}

	case "on_mount":
		w.kw(n.Pos.Line, n.Pos.Col, len("on"))
		if col := w.findWord(n.Pos.Line, n.Pos.Col, "mount"); col > 0 {
			w.emit(n.Pos.Line, col, len("mount"), tokEvent, 0, "")
		}
		for _, stmt := range n.Children {
			w.walkStmt(stmt)
		}

	case "field_decl":
		// Structured-list-state field declaration in body position.
		w.walkFieldDecl(n, tokProperty)

	case "code":
		// A code block's head highlights as a builtin kind; its body is a
		// verbatim raw string spanning multiple source lines, so we do NOT
		// emit it as a single-line string token (the editor's tree-sitter
		// grammar shades the raw block on its own).
		w.emit(n.Pos.Line, n.Pos.Col, len("code"), tokFunction, modDefaultLibrary, "code")

	default:
		w.walkInvocation(n)
	}
}

func (w *tokWalker) walkState(n *ast.Node) {
	w.kw(n.Pos.Line, n.Pos.Col, len("state"))
	if len(n.Args) > 0 {
		w.emitIdent(n.Args[0], tokProperty, modDeclaration)
	}
	if len(n.Args) > 1 {
		w.emitTypeRef(n.Args[1])
	}
	for _, c := range n.Children {
		if c.Kind == "field_decl" {
			w.walkFieldDecl(c, tokProperty)
			continue
		}
		w.walkExpr(c)
	}
}

func (w *tokWalker) walkFieldDecl(n *ast.Node, nameTyp int) {
	if len(n.Args) > 0 {
		w.emitIdent(n.Args[0], nameTyp, modDeclaration)
	}
	if len(n.Args) > 1 {
		w.emitTypeRef(n.Args[1])
	}
	for _, c := range n.Children {
		w.walkExpr(c) // default value
	}
}

func (w *tokWalker) walkThemeBinding(n *ast.Node) {
	switch n.Kind {
	case "tone_binding":
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokEnumMember, modDeclaration)
		}
		for _, a := range n.Args[1:] {
			w.emitQuoted(a)
		}
		// `on` between bg and fg colors.
		if len(n.Args) >= 3 {
			if col := w.findWord(n.Pos.Line, n.Pos.Col, "on"); col > 0 {
				w.kw(n.Pos.Line, col, len("on"))
			}
		}
	case "text_binding":
		w.kw(n.Pos.Line, n.Pos.Col, len("text"))
		for i, a := range n.Args {
			switch {
			case i == 0:
				w.emitIdent(a, tokProperty, modDeclaration)
			case a.Kind == ast.ValueString:
				w.emitQuoted(a)
			case a.Kind == ast.ValueInt:
				w.emitInt(a)
			default: // italic / caps / tracking
				w.kw(a.Pos.Line, a.Pos.Col, len(a.String))
			}
		}
	}
}

func (w *tokWalker) walkBackendBinding(n *ast.Node) {
	if n.Kind != "backend-binding" || len(n.Args) == 0 {
		return
	}
	key := n.Args[0]
	w.kw(key.Pos.Line, key.Pos.Col, len(key.String))
	if len(n.Args) < 2 {
		return
	}
	val := n.Args[1]
	switch key.String {
	case "url":
		if val.Kind == ast.ValueString {
			w.emitQuoted(val)
		} else {
			w.emitIdent(val, tokEnumMember, 0) // same-origin
		}
	case "auth":
		w.emitIdent(val, tokEnumMember, 0)
	case "token":
		if col := w.findWord(key.Pos.Line, key.Pos.Col, "from"); col > 0 {
			w.kw(key.Pos.Line, col, len("from"))
		}
		w.emitIdent(val, tokProperty, 0)
	}
}

func (w *tokWalker) walkInvocation(n *ast.Node) {
	// Head: builtin kind vs user-defined component.
	typ, mods := tokFunction, 0
	if w.builtins[n.Kind] {
		mods = modDefaultLibrary
	}
	w.emit(n.Pos.Line, n.Pos.Col, len(n.Kind), typ, mods, n.Kind)

	for _, a := range n.Args {
		switch a.Kind {
		case ast.ValueString:
			w.emitQuoted(a)
		case ast.ValueInt:
			w.emitInt(a)
		default:
			if a.String == "true" || a.String == "false" {
				w.emitIdent(a, tokEnumMember, 0)
			} else {
				w.emitIdent(a, tokVariable, 0)
			}
		}
	}

	w.walkKwargs(n)

	if len(n.Handlers) > 0 {
		w.rescanHandlers(n.Pos.Line, n.Pos.Col)
		// Events sorted for deterministic token order (map iteration).
		events := make([]string, 0, len(n.Handlers))
		for ev := range n.Handlers {
			events = append(events, ev)
		}
		sort.Strings(events)
		for _, ev := range events {
			w.walkStmt(n.Handlers[ev])
		}
	}

	w.walkBody(n.Children)
}

func (w *tokWalker) walkKwargs(n *ast.Node) {
	for key, v := range n.Kwargs {
		// Kwargs forbid whitespace around `=`, so the key sits exactly
		// len(key)+1 bytes before the value.
		keyCol := v.Pos.Col - len(key) - 1
		w.emit(v.Pos.Line, keyCol, len(key), tokProperty, 0, key)
		switch v.Kind {
		case ast.ValueString:
			w.emitQuoted(v)
		case ast.ValueInt:
			w.emitInt(v)
		default:
			if key == "tone" || v.String == "true" || v.String == "false" {
				w.emitIdent(v, tokEnumMember, 0)
			} else {
				w.emitIdent(v, tokVariable, 0)
			}
		}
	}
}

func (w *tokWalker) walkStmt(n *ast.Node) {
	switch n.Kind {
	case "seq":
		for _, c := range n.Children {
			w.walkStmt(c)
		}
	case "assign", "stream_assign":
		for _, t := range n.Args {
			w.emitIdent(t, tokVariable, 0)
		}
		for _, c := range n.Children {
			w.walkExpr(c)
		}
	case "method_call":
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokVariable, 0)
		}
		if len(n.Args) > 1 {
			w.emitIdent(n.Args[1], tokMethod, 0)
		}
		for _, c := range n.Children {
			w.walkExpr(c)
		}
	case "op_call_stmt", "op_call":
		if len(n.Args) > 0 {
			w.emitIdent(n.Args[0], tokFunction, 0)
		}
		for _, c := range n.Children {
			w.walkExpr(c)
		}
		// `Op(...) then navigate "<path>"` success hook: the path string
		// is the kwarg value; the `then`/`navigate` keywords precede it.
		if tn, ok := n.Kwargs["then_navigate"]; ok {
			if col := w.findWord(n.Pos.Line, n.Pos.Col, "then"); col > 0 {
				w.kw(n.Pos.Line, col, len("then"))
			}
			if col := w.findWord(n.Pos.Line, n.Pos.Col, "navigate"); col > 0 {
				w.kw(n.Pos.Line, col, len("navigate"))
			}
			w.emitQuoted(tn)
		}
	case "navigate":
		w.kw(n.Pos.Line, n.Pos.Col, len("navigate"))
		if len(n.Args) > 0 {
			w.emitQuoted(n.Args[0])
		}
	}
}

func (w *tokWalker) walkExpr(n *ast.Node) {
	switch n.Kind {
	case "lit":
		for _, a := range n.Args {
			if a.Kind == ast.ValueString {
				w.emitQuoted(a)
			} else {
				w.emitInt(a)
			}
		}
	case "ref":
		for _, a := range n.Args {
			if a.String == "true" || a.String == "false" {
				w.emitIdent(a, tokEnumMember, 0)
			} else {
				w.emitIdent(a, tokVariable, 0)
			}
		}
	case "op_call":
		w.walkStmt(n)
	case "not", "list_lit":
		for _, c := range n.Children {
			w.walkExpr(c)
		}
	default:
		if strings.HasPrefix(n.Kind, "binop:") {
			for _, c := range n.Children {
				w.walkExpr(c)
			}
		}
	}
}

// emitTypeRef highlights a type reference including generic args:
// `List<types.Pokemon>?` → List + types.Pokemon as type tokens.
func (w *tokWalker) emitTypeRef(v ast.Value) {
	if v.Kind != ast.ValueIdent || v.String == "" {
		return
	}
	w.emit(v.Pos.Line, v.Pos.Col, len(v.String), tokType, 0, v.String)
	for _, g := range v.GenericArgs {
		w.emitTypeRef(g)
	}
}

// ───────────── line rescans ──────────────────────────────────────────

// maskLine returns a copy of the 1-based line with string interiors
// blanked and the trailing `//` comment removed, plus the comment's
// 1-based start col (0 when none). Keyword rescans run on the masked
// copy so words inside strings or comments can't false-positive.
func (w *tokWalker) maskLine(line int) (string, int) {
	if w.masked == nil {
		w.masked = map[int]string{}
		w.commentCol = map[int]int{}
	}
	if m, ok := w.masked[line]; ok {
		return m, w.commentCol[line]
	}
	src := ""
	if line-1 < len(w.lines) {
		src = w.lines[line-1]
	}
	buf := []byte(src)
	inStr := false
	comment := 0
	for i := 0; i < len(buf); i++ {
		c := buf[i]
		if inStr {
			if c == '\\' && i+1 < len(buf) {
				buf[i], buf[i+1] = ' ', ' '
				i++
				continue
			}
			if c == '"' {
				inStr = false
				continue
			}
			buf[i] = ' '
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		if c == '/' && i+1 < len(buf) && buf[i+1] == '/' {
			comment = i + 1
			buf = buf[:i]
			break
		}
	}
	m := string(buf)
	w.masked[line] = m
	w.commentCol[line] = comment
	return m, comment
}

// findWord returns the 1-based col of word as a standalone token on
// the masked line, searching from fromCol; 0 when absent.
func (w *tokWalker) findWord(line, fromCol int, word string) int {
	masked, _ := w.maskLine(line)
	if fromCol < 1 {
		fromCol = 1
	}
	for start := fromCol - 1; start <= len(masked)-len(word); {
		i := strings.Index(masked[start:], word)
		if i < 0 {
			return 0
		}
		at := start + i
		beforeOK := at == 0 || !isWordByte(masked[at-1])
		afterOK := at+len(word) >= len(masked) || !isWordByte(masked[at+len(word)])
		if beforeOK && afterOK {
			return at + 1
		}
		start = at + 1
	}
	return 0
}

func isWordByte(b byte) bool {
	return b == '_' || b == '-' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

var handlerRe = regexp.MustCompile(`\bon[ \t]+([A-Za-z_][A-Za-z0-9_-]*)[ \t]*\{`)

// rescanHandlers finds `on <event> {` clauses on an invocation line —
// the AST keeps the handler bodies but not the keyword/event token
// positions, so those two tokens come from the source line.
func (w *tokWalker) rescanHandlers(line, fromCol int) {
	masked, _ := w.maskLine(line)
	if fromCol-1 >= len(masked) {
		return
	}
	for _, m := range handlerRe.FindAllStringSubmatchIndex(masked[fromCol-1:], -1) {
		base := fromCol - 1
		w.kw(line, base+m[0]+1, len("on"))
		w.emit(line, base+m[2]+1, m[3]-m[2], tokEvent, 0, "")
	}
}

var paramTypeRe = regexp.MustCompile(`:[ \t]*([A-Za-z_][A-Za-z0-9_-]*)`)

// rescanParamTypes recovers `: Type` annotations on view/component
// signature lines (the parser swallows them at v0).
func (w *tokWalker) rescanParamTypes(line, fromCol int) {
	masked, _ := w.maskLine(line)
	if fromCol-1 >= len(masked) {
		return
	}
	for _, m := range paramTypeRe.FindAllStringSubmatchIndex(masked[fromCol-1:], -1) {
		base := fromCol - 1
		w.emit(line, base+m[2]+1, m[3]-m[2], tokType, 0, "")
	}
}

// scanComments emits one comment token per line that has `//` outside
// a string. The parser drops comments entirely, so this is the one
// token class that comes purely from the source text.
func (w *tokWalker) scanComments() {
	for i := range w.lines {
		line := i + 1
		_, col := w.maskLine(line)
		if col > 0 {
			w.emit(line, col, len(w.lines[i])-col+1, tokComment, 0, "")
		}
	}
}

// ───────────── source-length helpers ─────────────────────────────────

// stringSrcLen measures the source byte length of a quoted string
// starting at v.Pos (escapes make the decoded value shorter than the
// source). Falls back to decoded length + quotes when the source
// doesn't line up (defensive — shouldn't happen).
func (w *tokWalker) stringSrcLen(v ast.Value) int {
	line := ""
	if v.Pos.Line-1 < len(w.lines) {
		line = w.lines[v.Pos.Line-1]
	}
	i := v.Pos.Col - 1
	if i < 0 || i >= len(line) || line[i] != '"' {
		return len(v.String) + 2
	}
	j := i + 1
	for j < len(line) {
		if line[j] == '\\' && j+1 < len(line) {
			j += 2
			continue
		}
		if line[j] == '"' {
			return j - i + 1
		}
		j++
	}
	return len(line) - i
}

// intSrcLen measures the source length of an integer literal at v.Pos
// (sign included; tolerates non-canonical forms like leading zeros).
func (w *tokWalker) intSrcLen(v ast.Value) int {
	line := ""
	if v.Pos.Line-1 < len(w.lines) {
		line = w.lines[v.Pos.Line-1]
	}
	i := v.Pos.Col - 1
	if i < 0 || i >= len(line) {
		return 1
	}
	n := 0
	if line[i] == '-' {
		n++
		i++
	}
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		n++
		i++
	}
	if n == 0 {
		return 1
	}
	return n
}
