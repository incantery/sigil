// Package parser is the v0 Sigil parser — line-and-indent oriented.
//
// Grammar at this stage:
//
//	file        = line*
//	line        = WS* ( comment | declLine | invokeLine )? NL
//	comment     = "//" .*
//
//	declLine    = ident name ("->" param (":" typeExpr)?)* "=" inlineBody?
//	invokeLine  = ident (arg | kwarg)*
//
//	param       = ident
//	arg         = string | ident | int
//	kwarg       = ident "=" value     (no whitespace around =)
//	value       = string | ident | int
//	inlineBody  = invokeLine
//	typeExpr    = ident       -- swallowed for now; types arrive later
//	string      = '"' ( \" | char )* '"'
//	ident       = letter (letter | digit | _ | -)*
//	int         = '-'? digit+
//	WS          = ' ' | '\t'
//
// Errors come back as *diag.Diagnostic values (which satisfy the `error`
// interface) so the `sigil check --json` command can surface structured
// file/line/col/stage info without parsing message strings.
package parser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/incantery/sigil/pkg/lang/ast"
	"github.com/incantery/sigil/pkg/lang/diag"
)

// Parse reads source and returns the top-level node plus any diagnostics
// collected. When a single line fails to parse we record the diagnostic,
// push an `__error__` placeholder node into the tree (so subsequent
// indented children attach to the right parent), and keep going. Lower
// silently skips `__error__` nodes — the diagnostics have already been
// surfaced through the returned error.
func Parse(source string) (*ast.Node, error) {
	diags := &diag.Diagnostics{}
	root := parseAll(source, diags)
	return root, diags.Err()
}

// parseAll is the internals of Parse — accumulates into the supplied
// Diagnostics instead of bailing.
func parseAll(source string, diags *diag.Diagnostics) *ast.Node {
	lines := strings.Split(source, "\n")

	root := &ast.Node{Kind: "__root__"}
	stack := []*ast.Node{root}
	indents := []int{-1}

	for i := 0; i < len(lines); i++ {
		lineNo := i
		raw := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimLeft(raw, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		indent := len(raw) - len(trimmed)

		for indent <= indents[len(indents)-1] {
			stack = stack[:len(stack)-1]
			indents = indents[:len(indents)-1]
		}
		parent := stack[len(stack)-1]

		// Context-sensitive dispatch for body lines:
		//   theme: body line is a tone binding `name = "#bg" on "#fg"`
		//   state-with-list-init: body line is a field decl `name : Type = default`
		//   icons: body line is a target binding `web "./path"`
		//   anything else: general invocation/decl grammar via parseLine
		var (
			node *ast.Node
			err  error
		)
		switch {
		case parent != nil && parent.Kind == "theme":
			// Theme body lines: `text <token> = …` declares/overrides a
			// text-scale entry; anything else is a tone binding. No tone
			// is named `text`, so the prefix dispatch is unambiguous.
			if strings.HasPrefix(trimmed, "text ") || strings.HasPrefix(trimmed, "text\t") {
				node, err = parseThemeTextBinding(trimmed, lineNo+1, indent+1)
			} else {
				node, err = parseToneBinding(trimmed, lineNo+1, indent+1)
			}
		case parent != nil && parent.Kind == "icons":
			node, err = parseIconTargetBinding(trimmed, lineNo+1, indent+1)
		case parent != nil && parent.Kind == "backend":
			node, err = parseBackendBinding(trimmed, lineNo+1, indent+1)
		case parent != nil && parent.Kind == "route":
			// A route's body is its view subtree, plus structured facet
			// lines (`path`, `params`, `view`). parseRouteBinding peels off
			// the facets and parses everything else as a normal view child.
			node, err = parseRouteBinding(trimmed, lineNo+1, indent+1)
		case parent != nil && parent.Kind == "route-params":
			// A route's `params` block is a list of `<name> <Type>` decls.
			node, err = parseRouteParam(trimmed, lineNo+1, indent+1)
		case parent != nil && parent.Kind == "state" && isStructuredListState(parent):
			node, err = parseFieldDecl(trimmed, lineNo+1, indent+1)
		case parent != nil && parent.Kind == "match":
			// A `match` body is a list of `| variant [as bind]` arms;
			// each arm's own indented subtree parses as normal view
			// lines (the arm node, not "match", parents them).
			node, err = parseMatchArm(trimmed, lineNo+1, indent+1)
		case parent != nil && parent.Kind == "type":
			// A `type` body is either a record (field decls) or a
			// sum (variant decls). The first character of each body
			// line tells us which — `|` starts a variant; an ident
			// starts a field decl. Mixing the two is a lower-time
			// error; the parser accepts both shapes uniformly.
			if strings.HasPrefix(trimmed, "|") {
				node, err = parseVariantDecl(trimmed, lineNo+1, indent+1)
			} else {
				node, err = parseFieldDecl(trimmed, lineNo+1, indent+1)
			}
		default:
			node, err = parseLine(trimmed, lineNo+1, indent+1)
		}
		if err != nil {
			diags.AddErr(err)
			node = &ast.Node{
				Kind: "__error__",
				Pos:  ast.Pos{Line: lineNo + 1, Col: indent + 1},
			}
		}
		parent.Children = append(parent.Children, node)

		// `code` (with no inline string arg) captures its indented body
		// verbatim — a monospace code block. The body is NOT the view
		// grammar: it may contain `{`, `}`, `=`, `${…}`, comment-looking
		// lines, and blank lines, so we consume every line indented deeper
		// than the `code` line as a single raw string and skip the normal
		// child-parsing path. `code` is a leaf, so it is never pushed as a
		// parent.
		if node.Kind == "code" && len(node.Args) == 0 {
			text, next := captureCodeBlock(lines, i+1, indent)
			node.Args = append(node.Args, ast.Value{Kind: ast.ValueString, String: text, Pos: node.Pos})
			i = next - 1 // -1 because the for-loop will i++
			continue
		}

		stack = append(stack, node)
		indents = append(indents, indent)
	}

	switch len(root.Children) {
	case 0:
		if diags.Empty() {
			diags.Add(diag.New("parse", 1, 1, "empty source"))
		}
		return &ast.Node{Kind: "__root__"}
	case 1:
		return root.Children[0]
	default:
		return &ast.Node{Kind: "module", Children: root.Children}
	}
}

// captureCodeBlock consumes the verbatim body of a `code` block. Starting at
// lines[from], it collects every line indented deeper than baseIndent (the
// `code` line's own indent), including blank lines that sit between content
// lines. It strips the common leading indentation, trims leading/trailing
// blank lines, and joins the rest with `\n`. The returned index is the first
// line that does NOT belong to the block (the dedented sibling, or len(lines)).
//
// No part of the body is parsed as Sigil — code samples routinely contain
// `{`, `}`, `=`, and `${…}`, none of which should be interpreted.
func captureCodeBlock(lines []string, from, baseIndent int) (string, int) {
	var body []string
	minIndent := -1
	i := from
	for ; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		t := strings.TrimLeft(l, " \t")
		if t == "" {
			body = append(body, "")
			continue
		}
		ind := len(l) - len(t)
		if ind <= baseIndent {
			break
		}
		if minIndent < 0 || ind < minIndent {
			minIndent = ind
		}
		body = append(body, l)
	}
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	if minIndent < 0 {
		minIndent = baseIndent + 2
	}
	for j, l := range body {
		if len(l) >= minIndent {
			body[j] = l[minIndent:]
		} else {
			body[j] = strings.TrimLeft(l, " \t")
		}
	}
	return strings.Join(body, "\n"), i
}

// declKeywords are head idents that *always* start a declaration line, so
// missing the `=` is a hard error (with a hint), not a silent fallback to
// "treat the rest as juxtaposed args."
var declKeywords = map[string]bool{
	"view":      true,
	"component": true,
	"flow":      true,
	"state":     true,
	"theme":     true,
	"test":      true,
	"story":     true,
	"type":      true,
	"query":     true,
	"command":   true,
	"stream":    true,
	"app":       true,
	"import":    true,
	"icons":     true,
	"backend":   true,
	"session":   true,
	"fonts":     true,
}

// parseLine reads one already-indent-stripped line into a Node.
func parseLine(s string, lineNo, startCol int) (*ast.Node, error) {
	pos := ast.Pos{Line: lineNo, Col: startCol}

	// `*name` at the head of a body line is a splice — used inside a
	// component body to expand the variadic param's captured children
	// into the current children list. Lower enforces that the name
	// actually refers to a variadic param; the parser just emits the
	// AST shape.
	if len(s) > 0 && s[0] == '*' {
		nameCol := startCol + 1
		name, after, err := readIdent(s[1:], ast.Pos{Line: lineNo, Col: nameCol})
		if err != nil {
			return nil, diag.New("parse", lineNo, nameCol, "expected identifier after `*`")
		}
		after = strings.TrimLeft(after, " \t")
		if after != "" && !strings.HasPrefix(after, "//") {
			return nil, diag.New("parse", lineNo, startCol,
				"`*"+name+"` splice line takes no arguments")
		}
		return &ast.Node{
			Kind: "splice",
			Pos:  pos,
			Args: []ast.Value{{Kind: ast.ValueIdent, String: name, Pos: ast.Pos{Line: lineNo, Col: nameCol}}},
		}, nil
	}

	head, rest, err := readIdent(s, pos)
	if err != nil {
		return nil, err
	}
	// Package-qualified invocation head: `components.Pill "hi"`.
	// The dot only reads as a qualifier when the suffix starts
	// uppercase (decl names are CamelCase by convention); lowercase
	// suffixes stay errors here because body lines never start with
	// a cell-field ref. The loader's merge pass rewrites the
	// qualified Kind to its bare name before lowering.
	//
	// Never consume a dot after a decl keyword (`view`, `theme`, …) or
	// `on`: those dispatch below, and swallowing the dot would turn a
	// decl typo into a confusing "unknown component" far downstream.
	if !declKeywords[head] && head != "on" &&
		strings.HasPrefix(rest, ".") && len(rest) > 1 && rest[1] >= 'A' && rest[1] <= 'Z' {
		suffix, after, err := readIdent(rest[1:], ast.Pos{Line: lineNo, Col: startCol + len(head) + 1})
		if err != nil {
			return nil, err
		}
		head = head + "." + suffix
		rest = after
	}
	n := &ast.Node{Kind: head, Pos: pos}
	col := startCol + len(head)

	switch head {
	case "view", "component", "app", "flow":
		return parseDecl(n, rest, lineNo, col)
	case "group":
		return parseGroupPipe(n, rest, lineNo, col)
	case "state":
		return parseStateDecl(n, rest, lineNo, col)
	case "theme":
		return parseThemeDecl(n, rest, lineNo, col)
	case "test", "story":
		return parseQuotedNameDecl(n, rest, lineNo, col)
	case "type":
		return parseTypeDecl(n, rest, lineNo, col)
	case "query", "command", "stream":
		return parseOpDecl(n, rest, lineNo, col)
	case "import":
		return parseImportDecl(n, rest, lineNo, col)
	case "icons":
		return parseIconsDecl(n, rest, lineNo, col)
	case "fonts":
		return parseFontsDecl(n, rest, lineNo, col)
	case "backend":
		return parseBackendDecl(n, rest, lineNo, col)
	case "session":
		return parseSessionDecl(n, rest, lineNo, col)
	case "on":
		return parseOnDecl(n, rest, lineNo, col)
	}
	return parseInvocation(n, rest, lineNo, col)
}

// parseBackendDecl reads `backend <Name> =` plus an indented body
// of `url "..."`, `auth <method>`, and `token from <session-path>`
// lines. Backends declare a named call target (URL + auth method)
// that operations route through. Multiple backends per project are
// allowed; how each op picks a backend is decided at lower time.
//
// AST shape:
//   - n.Kind   = "backend"
//   - n.Args[0] = name ident
//   - n.Children = list of "backend-binding" nodes (one per body line)
func parseBackendDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, diag.New("parse", lineNo, col, "expected backend name after `backend`")
	}
	namePos := ast.Pos{Line: lineNo, Col: col}
	name, after, err := readIdent(rest, namePos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected backend name after `backend`")
	}
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: name, Pos: namePos})
	col += len(rest) - len(after)
	rest = after
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col, "expected `=` after backend name")
	}
	rest = rest[1:]
	col++
	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col,
			"backend body must be indented bindings on following lines")
	}
	return n, nil
}

// parseBackendBinding reads one body line of a backend block. v1
// shape:
//
//	url "<string>"        — backend's URL prefix
//	auth <method>         — closed enum: none, bearer, cookie
//	token from <Path.cell> — only meaningful when auth is bearer
//
// AST shape:
//   - Kind   = "backend-binding"
//   - Args[0] = key ident (url / auth / token)
//   - Args[1] = the value (string for url, ident for auth, dotted
//     ident for token-from)
func parseBackendBinding(s string, lineNo, startCol int) (*ast.Node, error) {
	pos := ast.Pos{Line: lineNo, Col: startCol}
	key, rest, err := readIdent(s, pos)
	if err != nil {
		return nil, diag.New("parse", lineNo, startCol,
			"expected `url`, `auth`, or `token` at start of backend body line")
	}
	col := startCol + len(s) - len(rest)
	rest, col = skipWS(rest, col)
	n := &ast.Node{
		Kind: "backend-binding",
		Pos:  pos,
		Args: []ast.Value{{Kind: ast.ValueIdent, String: key, Pos: pos}},
	}
	switch key {
	case "url":
		// `url same-origin` — bare keyword meaning "the origin the page
		// was served from". Lowered to an empty URL prefix so the client
		// requests `/query/<op>` etc. relative to its own origin; the
		// serve-the-page-and-the-ops-from-one-process deployment needs no
		// baked-in host. Kept a keyword (not an empty string) so a missing
		// URL stays a compile error rather than a silent same-origin.
		if word := firstWord(rest); word == "same-origin" {
			vPos := ast.Pos{Line: lineNo, Col: col}
			n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: "same-origin", Pos: vPos})
			rest = rest[len("same-origin"):]
			col += len("same-origin")
			break
		}
		if rest == "" || rest[0] != '"' {
			return nil, diag.New("parse", lineNo, col,
				"expected quoted URL or `same-origin` after `url`")
		}
		vPos := ast.Pos{Line: lineNo, Col: col}
		url, after, err := readString(rest, vPos)
		if err != nil {
			return nil, err
		}
		col += len(rest) - len(after)
		rest = after
		n.Args = append(n.Args, ast.Value{Kind: ast.ValueString, String: url, Pos: vPos})
	case "auth":
		if rest == "" {
			return nil, diag.New("parse", lineNo, col,
				"expected auth method after `auth` (none / bearer / cookie)")
		}
		vPos := ast.Pos{Line: lineNo, Col: col}
		method, after, err := readIdent(rest, vPos)
		if err != nil {
			return nil, diag.New("parse", lineNo, col,
				"expected auth method ident (none / bearer / cookie)")
		}
		col += len(rest) - len(after)
		rest = after
		n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: method, Pos: vPos})
	case "token":
		// `token from Auth.token` — points at a session cell.
		if !strings.HasPrefix(rest, "from") || (len(rest) > 4 && isIdentRest(rune(rest[4]))) {
			return nil, diag.New("parse", lineNo, col,
				"expected `from <Session.cell>` after `token`")
		}
		rest = rest[4:]
		col += 4
		rest, col = skipWS(rest, col)
		vPos := ast.Pos{Line: lineNo, Col: col}
		ref, after, err := readCellRef(rest, vPos)
		if err != nil {
			return nil, diag.New("parse", lineNo, col,
				"expected `<Session>.<cell>` path after `from`")
		}
		col += len(rest) - len(after)
		rest = after
		n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: ref, Pos: vPos})
	default:
		return nil, diag.New("parse", lineNo, startCol,
			"unknown backend binding "+key+" (v1: url / auth / token)")
	}
	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col,
			"unexpected content after backend binding")
	}
	return n, nil
}

// routeFacetKeywords are the heads that introduce a route's structured
// facets rather than view-tree children. `load` / `guard` / `layout` land
// as the router grows.
var routeFacetKeywords = map[string]bool{
	"path":   true,
	"params": true,
	"view":   true,
	"guard":  true,
	"public": true,
	"layout": true,
}

// parseRouteBinding reads one body line of a `route` block. A line whose
// head is a route facet keyword (`path`, `params`, `view`) becomes a facet
// node; any other line is a normal view-tree child — a route with no
// explicit `view` facet treats its non-facet children as its view subtree.
//
// AST shapes:
//   - `path "<pat>"`  → Kind "route-path",   Args[0] = path pattern string
//   - `params`        → Kind "route-params", body = `route-param` decls
//   - `view`          → Kind "route-view",   body = view-tree children
func parseRouteBinding(s string, lineNo, startCol int) (*ast.Node, error) {
	head := firstWord(s)
	if !routeFacetKeywords[head] {
		return parseLine(s, lineNo, startCol)
	}
	pos := ast.Pos{Line: lineNo, Col: startCol}
	rest := strings.TrimLeft(s[len(head):], " \t")
	col := startCol + (len(s) - len(rest))
	switch head {
	case "path":
		if rest == "" || rest[0] != '"' {
			return nil, diag.New("parse", lineNo, col, "expected a quoted path after `path`")
		}
		vPos := ast.Pos{Line: lineNo, Col: col}
		p, after, err := readString(rest, vPos)
		if err != nil {
			return nil, err
		}
		if t := strings.TrimSpace(after); t != "" && !strings.HasPrefix(t, "//") {
			return nil, diag.New("parse", lineNo, col, "unexpected content after path")
		}
		return &ast.Node{
			Kind: "route-path",
			Pos:  pos,
			Args: []ast.Value{{Kind: ast.ValueString, String: p, Pos: vPos}},
		}, nil
	case "params", "view":
		// Container facets: no inline argument, body on the following
		// indented lines (params → param decls, view → view children).
		if t := strings.TrimSpace(rest); t != "" && !strings.HasPrefix(t, "//") {
			return nil, diag.New("parse", lineNo, col,
				"`"+head+"` takes no inline argument; put its body on the following indented lines")
		}
		return &ast.Node{Kind: "route-" + head, Pos: pos}, nil
	case "public":
		// `public` — this route needs no access check. Stated explicitly so
		// default-deny can flag any route that declares neither this nor a
		// guard.
		if t := strings.TrimSpace(rest); t != "" && !strings.HasPrefix(t, "//") {
			return nil, diag.New("parse", lineNo, col, "`public` takes no argument")
		}
		return &ast.Node{Kind: "route-public", Pos: pos}, nil
	case "guard":
		// `guard <op> [args...]` — an access check. The op is run on every
		// navigation; a falsy result or thrown error redirects to a public
		// route. Args are cell refs (e.g. a `:param`) or string literals.
		if rest == "" {
			return nil, diag.New("parse", lineNo, col, "expected an operation after `guard`")
		}
		opPos := ast.Pos{Line: lineNo, Col: col}
		opName, after, err := readIdent(rest, opPos)
		if err != nil {
			return nil, diag.New("parse", lineNo, col, "expected an operation name after `guard`")
		}
		col += len(rest) - len(after)
		rest = after
		args := []ast.Value{{Kind: ast.ValueIdent, String: opName, Pos: opPos}}
		for {
			rest, col = skipWS(rest, col)
			if rest == "" || strings.HasPrefix(rest, "//") {
				break
			}
			aPos := ast.Pos{Line: lineNo, Col: col}
			if rest[0] == '"' {
				sv, aft, err := readString(rest, aPos)
				if err != nil {
					return nil, err
				}
				col += len(rest) - len(aft)
				rest = aft
				args = append(args, ast.Value{Kind: ast.ValueString, String: sv, Pos: aPos})
				continue
			}
			iv, aft, err := readIdent(rest, aPos)
			if err != nil {
				return nil, diag.New("parse", lineNo, col, "expected an identifier or string guard argument")
			}
			col += len(rest) - len(aft)
			rest = aft
			args = append(args, ast.Value{Kind: ast.ValueIdent, String: iv, Pos: aPos})
		}
		return &ast.Node{Kind: "route-guard", Pos: pos, Args: args}, nil
	case "layout":
		// `layout <View>` — wrap this route's (or group's) view in a layout
		// component (shared chrome). The view is referenced by name.
		if rest == "" {
			return nil, diag.New("parse", lineNo, col, "expected a layout view name after `layout`")
		}
		vPos := ast.Pos{Line: lineNo, Col: col}
		name, after, err := readIdent(rest, vPos)
		if err != nil {
			return nil, diag.New("parse", lineNo, col, "expected a layout view name after `layout`")
		}
		if t := strings.TrimSpace(after); t != "" && !strings.HasPrefix(t, "//") {
			return nil, diag.New("parse", lineNo, col, "unexpected content after layout view")
		}
		return &ast.Node{
			Kind: "route-layout",
			Pos:  pos,
			Args: []ast.Value{{Kind: ast.ValueIdent, String: name, Pos: vPos}},
		}, nil
	}
	return nil, diag.New("parse", lineNo, startCol, "unknown route facet "+head)
}

// parseGroupPipe reads a `group |> <facet> |> <facet> ...` header. The
// pipe composes cross-cutting facets (guard / layout / public) that every
// member route under the group inherits. The member routes follow as
// indented children. The facet nodes are attached as the group's leading
// children; lowering separates facets from members by kind.
func parseGroupPipe(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	if !strings.HasPrefix(rest, "|>") {
		return nil, diag.New("parse", lineNo, col,
			"expected `|> <facet>` after `group` (e.g. `group |> guard memberOf id |> layout AppShell`)")
	}
	segs := strings.Split(rest, "|>")
	// segs[0] is the text before the first `|>` (empty here).
	for _, seg := range segs[1:] {
		facet := strings.TrimSpace(seg)
		if facet == "" {
			return nil, diag.New("parse", lineNo, col, "empty `|>` segment in group pipeline")
		}
		fn, err := parseRouteBinding(facet, lineNo, col)
		if err != nil {
			return nil, err
		}
		switch fn.Kind {
		case "route-guard", "route-public", "route-layout":
			n.Children = append(n.Children, fn)
		default:
			return nil, diag.New("parse", lineNo, col,
				"group `|>` facets must be `guard`, `public`, or `layout`")
		}
	}
	return n, nil
}

// parseRouteParam reads one body line of a route's `params` block:
// `<name> <Type>` (e.g. `orgID OrgID`). The type is captured for the
// future param/op typecheck; at runtime the value arrives as a string
// extracted from the matched path segment.
//
// AST shape: Kind "route-param", Args[0] = name ident, Args[1] = type ident.
func parseRouteParam(s string, lineNo, startCol int) (*ast.Node, error) {
	pos := ast.Pos{Line: lineNo, Col: startCol}
	name, rest, err := readIdent(s, pos)
	if err != nil {
		return nil, diag.New("parse", lineNo, startCol, "expected a param name")
	}
	col := startCol + len(s) - len(rest)
	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, diag.New("parse", lineNo, col, "expected a type after param name")
	}
	tPos := ast.Pos{Line: lineNo, Col: col}
	typ, after, err := readIdent(rest, tPos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected a type after param name")
	}
	if t := strings.TrimSpace(after); t != "" && !strings.HasPrefix(t, "//") {
		return nil, diag.New("parse", lineNo, col, "unexpected content after param type")
	}
	return &ast.Node{
		Kind: "route-param",
		Pos:  pos,
		Args: []ast.Value{
			{Kind: ast.ValueIdent, String: name, Pos: pos},
			{Kind: ast.ValueIdent, String: typ, Pos: tPos},
		},
	}, nil
}

// firstWord returns the run of non-whitespace characters at the start
// of s — used for keyword values that aren't plain idents (e.g. the
// hyphenated `same-origin`), which readIdent would split at the hyphen.
func firstWord(s string) string {
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return s[:i]
		}
	}
	return s
}

// parseSessionDecl reads `session <Name> =` plus an indented body of
// `state` decls. Sessions are like views but stateless visually —
// they declare persistent reactive cells (auth token, current user,
// feature flags) that outlive any one view. Other decls (backends,
// views) reference session cells via the dotted-path form
// `<SessionName>.<cell>`.
//
// AST shape:
//   - n.Kind   = "session"
//   - n.Args[0] = name ident
//   - n.Children = list of `state` decls (parsed by parseStateDecl
//     via the same indented-body dispatcher views use)
func parseSessionDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, diag.New("parse", lineNo, col, "expected session name after `session`")
	}
	namePos := ast.Pos{Line: lineNo, Col: col}
	name, after, err := readIdent(rest, namePos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected session name after `session`")
	}
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: name, Pos: namePos})
	col += len(rest) - len(after)
	rest = after
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col, "expected `=` after session name")
	}
	rest = rest[1:]
	col++
	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col,
			"session body must be indented state decls on following lines")
	}
	return n, nil
}

// parseOnDecl reads `on <event> { <stmts> }` at the view body level.
// v1 supports `on mount { ... }` only — fires once when the SPA DOM
// is built. The AST shape is Kind="on_mount" with Children being the
// handler statements.
func parseOnDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	eventPos := ast.Pos{Line: lineNo, Col: col}
	event, after, err := readIdent(rest, eventPos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected event name after `on`")
	}
	if event != "mount" {
		return nil, diag.New("parse", lineNo, col,
			fmt.Sprintf("unknown view-level event %q (v1: mount)", event))
	}
	col += len(rest) - len(after)
	rest = after
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '{' {
		return nil, diag.New("parse", lineNo, col, "expected `{` after `on mount`")
	}
	rest = rest[1:]
	col++
	rest, col = skipWS(rest, col)

	n.Kind = "on_mount"
	var stmts []*ast.Node
	for {
		stmt, after2, err := parseStmt(rest, lineNo, col)
		if err != nil {
			return nil, err
		}
		col += len(rest) - len(after2)
		rest = after2
		stmts = append(stmts, stmt)
		rest, col = skipWS(rest, col)
		if rest != "" && rest[0] == ';' {
			rest = rest[1:]
			col++
			rest, col = skipWS(rest, col)
			continue
		}
		break
	}
	if rest == "" || rest[0] != '}' {
		return nil, diag.New("parse", lineNo, col, "expected `}` to close `on mount` block")
	}
	n.Children = stmts
	return n, nil
}

// parseIconsDecl reads `icons <Name> =` plus indented body lines of
// the form `<target> "<folder-path>"` (handled by parseAll's
// dispatcher when parent.Kind == "icons"). Each body line declares a
// per-target source folder for the icon set. Folder discovery + SVG
// validation happen at compile time in the loader.
//
// AST shape:
//   - n.Kind   = "icons"
//   - n.Args[0] = name ident (the set name; namespaces every icon
//     declared by the set)
//   - n.Children = list of "icon-target" nodes (one per body line)
//
// v1 supports the `web` target only; the lowerer rejects unknown
// target names with a clear "target X not yet supported" error so
// adding `ios` / `terminal` is a renderer change, not a parser
// change.
func parseIconsDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, diag.New("parse", lineNo, col, "expected icon-set name after `icons`")
	}
	namePos := ast.Pos{Line: lineNo, Col: col}
	name, after, err := readIdent(rest, namePos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected icon-set name after `icons`")
	}
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: name, Pos: namePos})
	col += len(rest) - len(after)
	rest = after

	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col, "expected `=` after icon-set name")
	}
	rest = rest[1:]
	col++

	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col,
			"icon-set body must be indented target bindings on following lines")
	}
	return n, nil
}

// parseIconTargetBinding reads one body line of an icons block:
//
//	<target> "<folder-path>"
//
// Example: `web "./icons/lucide"`. The target name is a bare ident
// (closed enum at lower time); the path is a quoted string. The
// path is interpreted relative to the package directory at lower
// time so authors can reference assets via the same conventions as
// any other relative path in the project.
func parseIconTargetBinding(s string, lineNo, startCol int) (*ast.Node, error) {
	pos := ast.Pos{Line: lineNo, Col: startCol}
	target, rest, err := readIdent(s, pos)
	if err != nil {
		return nil, diag.New("parse", lineNo, startCol,
			"expected target name (e.g. `web`) at start of icons body line")
	}
	col := startCol + len(s) - len(rest)
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '"' {
		return nil, diag.New("parse", lineNo, col,
			"expected quoted folder path after target name")
	}
	pathPos := ast.Pos{Line: lineNo, Col: col}
	path, after, err := readString(rest, pathPos)
	if err != nil {
		return nil, err
	}
	col += len(rest) - len(after)
	rest = after
	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col,
			"unexpected content after icons target binding")
	}
	return &ast.Node{
		Kind: "icon-target",
		Pos:  pos,
		Args: []ast.Value{
			{Kind: ast.ValueIdent, String: target, Pos: pos},
			{Kind: ast.ValueString, String: path, Pos: pathPos},
		},
	}, nil
}

// parseImportDecl reads `import <module-path>` plus an optional
// `as <alias>` tail. Module paths are dotted-and-slashed URLs
// (`github.com/seth/pokedex/types`), unquoted — same surface form as
// `go.mod` and Go import lines (sans the quotes). The alias is
// the local name the file uses to qualify references; default is the
// last segment of the path (handled by the lowerer).
//
// AST shape:
//   - n.Kind   = "import"
//   - n.Args[0] = path (ValueString, holds the full dotted path)
//   - n.Args[1] = alias ident (ValueIdent), only present when `as`
//     was given
func parseImportDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, diag.New("parse", lineNo, col, "expected module path after `import`")
	}
	pathPos := ast.Pos{Line: lineNo, Col: col}
	path, after, err := readModulePath(rest, pathPos)
	if err != nil {
		return nil, err
	}
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueString, String: path, Pos: pathPos})
	col += len(rest) - len(after)
	rest = after

	rest, col = skipWS(rest, col)
	if strings.HasPrefix(rest, "as") && (len(rest) == 2 || !isIdentRest(rune(rest[2]))) {
		rest = rest[2:]
		col += 2
		rest, col = skipWS(rest, col)
		aliasPos := ast.Pos{Line: lineNo, Col: col}
		alias, after2, ierr := readIdent(rest, aliasPos)
		if ierr != nil {
			return nil, diag.New("parse", lineNo, col, "expected alias identifier after `as`")
		}
		n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: alias, Pos: aliasPos})
		col += len(rest) - len(after2)
		rest = after2
	}

	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col,
			"unexpected content after import path")
	}
	return n, nil
}

// readModulePath consumes a dotted-and-slashed identifier sequence
// (the format of a module import path: `github.com/seth/pokedex`).
// Allowed characters mirror sigilmod.validatePath; if the prefix is
// empty or not a valid path character, returns an error.
func readModulePath(s string, pos ast.Pos) (string, string, error) {
	i := 0
	for i < len(s) {
		r := rune(s[i])
		ok := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '/'
		if !ok {
			break
		}
		i++
	}
	if i == 0 {
		return "", s, diag.New("parse", pos.Line, pos.Col, "expected module path")
	}
	return s[:i], s[i:], nil
}

// parseOpDecl reads a query or command signature, one line, no body:
//
//	query Name (-> arg : Type)* = ReturnType
//	command Name (-> arg : Type)* = ReturnType
//
// AST shape:
//   - n.Kind   = "query" | "command"
//   - n.Args[0] = name ident
//   - n.Args[1] = return TypeRef value
//   - n.Children = list of "field_decl" nodes (one per typed input,
//     in declaration order). Reusing field_decl keeps the lowerer's
//     existing TypeFieldSpec path live.
//
// Anonymous args (`-> arg` without a type) and bodies are not
// supported — queries and commands are strictly typed contracts.
func parseOpDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, diag.New("parse", lineNo, col,
			fmt.Sprintf("expected %s name", n.Kind))
	}
	namePos := ast.Pos{Line: lineNo, Col: col}
	name, after, err := readIdent(rest, namePos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col,
			fmt.Sprintf("expected %s name", n.Kind))
	}
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: name, Pos: namePos})
	col += len(rest) - len(after)
	rest = after

	// Read zero or more `-> argName : Type` segments.
	for {
		rest, col = skipWS(rest, col)
		if !strings.HasPrefix(rest, "->") {
			break
		}
		rest = rest[2:]
		col += 2
		rest, col = skipWS(rest, col)
		argPos := ast.Pos{Line: lineNo, Col: col}
		argName, after2, ierr := readIdent(rest, argPos)
		if ierr != nil {
			return nil, diag.New("parse", lineNo, col, "expected arg name after `->`")
		}
		col += len(rest) - len(after2)
		rest = after2
		rest, col = skipWS(rest, col)
		if rest == "" || rest[0] != ':' {
			return nil, diag.New("parse", lineNo, col,
				fmt.Sprintf("%s args must be typed: `%s -> %s : Type`", n.Kind, n.Kind, argName))
		}
		rest = rest[1:]
		col++
		rest, col = skipWS(rest, col)
		tref, after3, newCol, terr := readTypeRef(rest, lineNo, col)
		if terr != nil {
			return nil, terr
		}
		col = newCol
		rest = after3
		n.Children = append(n.Children, &ast.Node{
			Kind: "field_decl",
			Pos:  argPos,
			Args: []ast.Value{
				{Kind: ast.ValueIdent, String: argName, Pos: argPos},
				tref,
			},
		})
	}

	// Required `=` plus return TypeRef.
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col,
			fmt.Sprintf("expected `=` and a return type in %s signature", n.Kind))
	}
	rest = rest[1:]
	col++
	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, diag.New("parse", lineNo, col, "expected return type after `=`")
	}
	ret, after4, newCol, rerr := readTypeRef(rest, lineNo, col)
	if rerr != nil {
		return nil, rerr
	}
	n.Args = append(n.Args, ret)
	col = newCol
	rest = after4

	// Optional `backend <Name>` clause: binds this op to a named
	// backend. Without the clause the lowerer applies the default
	// rule (sole backend if exactly one exists, else error).
	rest, col = skipWS(rest, col)
	if strings.HasPrefix(rest, "backend") && (len(rest) == 7 || !isIdentRest(rune(rest[7]))) {
		rest = rest[7:]
		col += 7
		rest, col = skipWS(rest, col)
		bPos := ast.Pos{Line: lineNo, Col: col}
		bName, after5, berr := readIdent(rest, bPos)
		if berr != nil {
			return nil, diag.New("parse", lineNo, col, "expected backend name after `backend`")
		}
		col += len(rest) - len(after5)
		rest = after5
		n.Children = append(n.Children, &ast.Node{
			Kind: "op-backend",
			Pos:  bPos,
			Args: []ast.Value{{Kind: ast.ValueIdent, String: bName, Pos: bPos}},
		})
	}

	// Optional `invalidates Q1 Q2 ...` clause on commands. Queries
	// don't get this — they're read-only by definition. The list of
	// invalidated query names attaches as a child node (Kind
	// "invalidates", Args = ident list) so the lowerer can pull it
	// off without growing parseOpDecl's signature.
	rest, col = skipWS(rest, col)
	if strings.HasPrefix(rest, "invalidates") && (len(rest) == 11 || !isIdentRest(rune(rest[11]))) {
		if n.Kind != "command" {
			return nil, diag.New("parse", lineNo, col,
				"only `command` declarations can have `invalidates` clauses")
		}
		rest = rest[11:]
		col += 11
		inv := &ast.Node{Kind: "invalidates", Pos: ast.Pos{Line: lineNo, Col: col}}
		for {
			rest, col = skipWS(rest, col)
			if rest == "" || strings.HasPrefix(rest, "//") {
				break
			}
			qPos := ast.Pos{Line: lineNo, Col: col}
			qName, after5, ierr := readIdent(rest, qPos)
			if ierr != nil {
				return nil, diag.New("parse", lineNo, col,
					"expected query name in `invalidates` list")
			}
			inv.Args = append(inv.Args, ast.Value{
				Kind: ast.ValueIdent, String: qName, Pos: qPos,
			})
			col += len(rest) - len(after5)
			rest = after5
		}
		if len(inv.Args) == 0 {
			return nil, diag.New("parse", lineNo, col,
				"`invalidates` clause must list at least one query name")
		}
		n.Children = append(n.Children, inv)
	}

	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col,
			fmt.Sprintf("unexpected content after %s signature", n.Kind))
	}
	return n, nil
}

// parseTypeDecl reads `type <Name> =` plus indented `field : Type`
// body lines (handled by the parseAll dispatcher when parent.Kind ==
// "type"). v0 supports record types only: a body is a list of named
// typed fields. Sum types, generics, and optionals are deferred to
// follow-up commits.
func parseTypeDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, diag.New("parse", lineNo, col, "expected type name after `type`")
	}
	namePos := ast.Pos{Line: lineNo, Col: col}
	name, after, err := readIdent(rest, namePos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected type name after `type`")
	}
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: name, Pos: namePos})
	col += len(rest) - len(after)
	rest = after

	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col, "expected `=` after type name")
	}
	rest = rest[1:]
	col++

	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col,
			"type body must be indented field declarations on following lines")
	}
	return n, nil
}

// parseQuotedNameDecl reads `test "<name>" = <inlineBody>?` and the
// identically-shaped `story "<name>" =`. The name is a string literal so
// authors can write phrases ("+ increments count", "Empty card") rather
// than identifier-shaped slugs. Body lines (test steps, or a story's
// component invocations) attach as children via the main indent walker;
// an inline `= scenario Counter` style first child is supported to match
// the `view Name = body` ergonomic.
func parseQuotedNameDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '"' {
		return nil, diag.New("parse", lineNo, col,
			"expected quoted "+n.Kind+" name after `"+n.Kind+"`")
	}
	namePos := ast.Pos{Line: lineNo, Col: col}
	name, after, err := readString(rest, namePos)
	if err != nil {
		return nil, err
	}
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueString, String: name, Pos: namePos})
	col += len(rest) - len(after)
	rest = after

	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col, "expected `=` after "+n.Kind+" name")
	}
	rest = rest[1:]
	col++

	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		body, err := parseLine(rest, lineNo, col)
		if err != nil {
			return nil, err
		}
		n.Children = append(n.Children, body)
	}
	return n, nil
}

// parseThemeDecl reads `theme <name> [extends <base>] =` plus indented
// `<tone> = "#bg" on "#fg"` body lines.
//
// Grammar:
//
//	themeDecl   = "theme" ident ("extends" ident)? "="
//	toneBinding = ident "=" string "on" string
//
// Each tone-binding becomes a "tone_binding" child of the theme node with
// Args[0]=tone-name ident, Args[1]=bg color string, Args[2]=fg color
// string. Lower validates the names against the closed tone vocabulary
// and the colors via WCAG contrast.
func parseThemeDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	namePos := ast.Pos{Line: lineNo, Col: col}
	name, after, err := readIdent(rest, namePos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected theme name")
	}
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: name, Pos: namePos})
	col += len(rest) - len(after)
	rest = after

	rest, col = skipWS(rest, col)
	if strings.HasPrefix(rest, "extends") {
		rest = rest[len("extends"):]
		col += len("extends")
		rest, col = skipWS(rest, col)
		basePos := ast.Pos{Line: lineNo, Col: col}
		base, after2, err := readIdent(rest, basePos)
		if err != nil {
			return nil, diag.New("parse", lineNo, col, "expected base theme name after `extends`")
		}
		if n.Kwargs == nil {
			n.Kwargs = map[string]ast.Value{}
		}
		n.Kwargs["extends"] = ast.Value{Kind: ast.ValueIdent, String: base, Pos: basePos}
		col += len(rest) - len(after2)
		rest = after2
	}

	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col, "expected `=` in theme declaration")
	}
	rest = rest[1:]
	col++
	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col, "theme body must be indented on following lines")
	}
	// Children attach via the usual indent-walker in parseAll.
	return n, nil
}

// isStructuredListState returns true when a `state` decl has a list
// initializer — these are the only states whose body lines mean "field
// declarations." Scalar/string/bool states have no body lines.
func isStructuredListState(state *ast.Node) bool {
	if state == nil || state.Kind != "state" {
		return false
	}
	if len(state.Children) == 0 {
		return false
	}
	return state.Children[0].Kind == "list_lit"
}

// parseFieldDecl reads one `name : Type ( = literal )?` body line of a
// structured list state. Shape:
//
//	field_decl
//	  Args[0] = name ident
//	  Args[1] = type ident (String / Bool / Int)
//	  Children[0] = literal default (optional)
//
// Lower validates the type and the default; the parser just enforces shape.
func parseFieldDecl(s string, lineNo, startCol int) (*ast.Node, error) {
	pos := ast.Pos{Line: lineNo, Col: startCol}
	name, rest, err := readIdent(s, pos)
	if err != nil {
		return nil, diag.New("parse", lineNo, startCol, "expected field name")
	}
	col := startCol + len(s) - len(rest)
	n := &ast.Node{
		Kind: "field_decl",
		Pos:  pos,
		Args: []ast.Value{{Kind: ast.ValueIdent, String: name, Pos: pos}},
	}
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != ':' {
		return nil, diag.New("parse", lineNo, col, "expected `:` after field name")
	}
	rest = rest[1:]
	col++
	rest, col = skipWS(rest, col)
	typeRef, after, col, err := readTypeRef(rest, lineNo, col)
	if err != nil {
		return nil, err
	}
	n.Args = append(n.Args, typeRef)
	rest = after
	rest, col = skipWS(rest, col)
	if rest != "" && rest[0] == '=' {
		rest = rest[1:]
		col++
		rest, col = skipWS(rest, col)
		defExpr, after2, err := parseExpr(rest, lineNo, col)
		if err != nil {
			return nil, err
		}
		n.Children = append(n.Children, defExpr)
		col += len(rest) - len(after2)
		rest = after2
	}
	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col, "unexpected content after field decl")
	}
	return n, nil
}

// readTypeRef reads one type reference: an ident, optionally
// followed by `<inner-type>` (recursive — `List<List<Int>>` works),
// optionally followed by `?` (postfix optional). Returns the
// resulting ast.Value and the remaining input + column. Whitespace
// is allowed inside `<...>` but not between an ident and its
// trailing `<` or `?`.
//
// v0 grammar:
//
//	typeRef = ident ( "<" typeRef ( "," typeRef )* ">" )? "?"?
//
// The parser is permissive on the head ident — it accepts any name,
// generic or not. The lowerer validates that generic heads are in
// the closed builtin set (`List` at v0) and that single-name refs
// resolve to a primitive or declared type.
func readTypeRef(s string, lineNo, startCol int) (ast.Value, string, int, error) {
	pos := ast.Pos{Line: lineNo, Col: startCol}
	if s == "" {
		return ast.Value{}, s, startCol, diag.New("parse", lineNo, startCol,
			"expected type reference")
	}
	name, after, err := readIdent(s, pos)
	if err != nil {
		return ast.Value{}, s, startCol, diag.New("parse", lineNo, startCol,
			"expected type name")
	}
	col := startCol + len(s) - len(after)
	rest := after

	// Optional dotted qualifier: `alias.TypeName`. Used when a type
	// reference names a declaration imported from another package.
	// The loader resolves the qualifier; here we just preserve the
	// dotted form in the value's String so downstream code can split
	// on `.` to find the alias.
	if strings.HasPrefix(rest, ".") {
		rest = rest[1:]
		col++
		secondName, after2, err := readIdent(rest, ast.Pos{Line: lineNo, Col: col})
		if err != nil {
			return ast.Value{}, s, startCol,
				diag.New("parse", lineNo, col, "expected type name after `.`")
		}
		name = name + "." + secondName
		col += len(rest) - len(after2)
		rest = after2
	}

	v := ast.Value{Kind: ast.ValueIdent, String: name, Pos: pos}

	// Generic args: `<inner [, inner...]>`. No whitespace between
	// the head ident and `<` so we don't get confused with
	// less-than comparisons in expression position (no expression
	// position has type refs anyway at v0, but be conservative).
	if rest != "" && rest[0] == '<' {
		rest = rest[1:]
		col++
		for {
			rest, col = skipWS(rest, col)
			arg, after2, newCol, err := readTypeRef(rest, lineNo, col)
			if err != nil {
				return ast.Value{}, s, startCol, err
			}
			v.GenericArgs = append(v.GenericArgs, arg)
			rest = after2
			col = newCol
			rest, col = skipWS(rest, col)
			if rest == "" {
				return ast.Value{}, s, startCol, diag.New("parse", lineNo, col,
					"unterminated generic args (expected `>` or `,`)")
			}
			if rest[0] == ',' {
				rest = rest[1:]
				col++
				continue
			}
			if rest[0] == '>' {
				rest = rest[1:]
				col++
				break
			}
			return ast.Value{}, s, startCol, diag.New("parse", lineNo, col,
				"expected `,` or `>` in generic args")
		}
	}

	// Optional postfix marker.
	if rest != "" && rest[0] == '?' {
		v.Optional = true
		rest = rest[1:]
		col++
	}

	return v, rest, col, nil
}

// parseVariantDecl reads one `| name` or `| name : Type` line in a
// sum-type body. A bare name is a unit variant (the original string-
// enum shape); a `: Type` suffix makes it a discriminated-union
// variant carrying that single payload type. Multi-field payloads use
// a declared record type.
//
//	variant_decl
//	  Args[0] = variant name ident
//	  Args[1] = payload type ident (optional; present iff `: Type`)
func parseVariantDecl(s string, lineNo, startCol int) (*ast.Node, error) {
	if s == "" || s[0] != '|' {
		return nil, diag.New("parse", lineNo, startCol, "expected `|` to start variant")
	}
	pos := ast.Pos{Line: lineNo, Col: startCol}
	rest := s[1:]
	col := startCol + 1
	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, diag.New("parse", lineNo, col, "expected variant name after `|`")
	}
	namePos := ast.Pos{Line: lineNo, Col: col}
	name, after, err := readIdent(rest, namePos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected variant name after `|`")
	}
	col += len(rest) - len(after)
	rest = after
	args := []ast.Value{{Kind: ast.ValueIdent, String: name, Pos: namePos}}

	rest, col = skipWS(rest, col)
	if rest != "" && rest[0] == ':' {
		rest = rest[1:]
		col++
		rest, col = skipWS(rest, col)
		// Payload type ref (`User`, `String`, `List<Pokemon>`, `T?`) —
		// reuse the field-type reader so generics/optionals parse the
		// same way they do for record fields.
		typeRef, after2, newCol, terr := readTypeRef(rest, lineNo, col)
		if terr != nil {
			return nil, terr
		}
		col = newCol
		rest = after2
		args = append(args, typeRef)
	}

	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col,
			"unexpected content after variant (write `| name` for a unit variant or `| name : Type` for a payload)")
	}
	return &ast.Node{
		Kind: "variant_decl",
		Pos:  pos,
		Args: args,
	}, nil
}

// parseMatchArm reads one `| variant` or `| variant as binding` arm
// line in a `match` body. The optional `as <name>` binds the matched
// variant's payload for use inside the arm's subtree; a unit variant
// takes no binding.
//
//	match_arm
//	  Args[0] = variant name ident
//	  Args[1] = payload binding ident (optional; present iff `as name`)
func parseMatchArm(s string, lineNo, startCol int) (*ast.Node, error) {
	if s == "" || s[0] != '|' {
		return nil, diag.New("parse", lineNo, startCol, "expected `|` to start a match arm")
	}
	pos := ast.Pos{Line: lineNo, Col: startCol}
	rest := s[1:]
	col := startCol + 1
	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, diag.New("parse", lineNo, col, "expected variant name or string after `|`")
	}
	// String-literal arm — used by scenario `match` to branch on an
	// observed value (`| "pro"`). Union match arms (ident variants) take
	// the path below. A literal arm carries no `as` binding.
	if rest[0] == '"' {
		litPos := ast.Pos{Line: lineNo, Col: col}
		lit, after, lerr := readString(rest, litPos)
		if lerr != nil {
			return nil, lerr
		}
		col += len(rest) - len(after)
		rest = after
		rest, col = skipWS(rest, col)
		if rest != "" && !strings.HasPrefix(rest, "//") {
			return nil, diag.New("parse", lineNo, col,
				"unexpected content after match arm (write `| \"value\"`)")
		}
		return &ast.Node{Kind: "match_arm", Pos: pos,
			Args: []ast.Value{{Kind: ast.ValueString, String: lit, Pos: litPos}}}, nil
	}
	namePos := ast.Pos{Line: lineNo, Col: col}
	name, after, err := readIdent(rest, namePos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected variant name after `|`")
	}
	col += len(rest) - len(after)
	rest = after
	args := []ast.Value{{Kind: ast.ValueIdent, String: name, Pos: namePos}}

	rest, col = skipWS(rest, col)
	if strings.HasPrefix(rest, "as") && (len(rest) == 2 || rest[2] == ' ' || rest[2] == '\t') {
		rest = rest[2:]
		col += 2
		rest, col = skipWS(rest, col)
		bindPos := ast.Pos{Line: lineNo, Col: col}
		bind, after2, berr := readIdent(rest, bindPos)
		if berr != nil {
			return nil, diag.New("parse", lineNo, col, "expected a binding name after `as`")
		}
		col += len(rest) - len(after2)
		rest = after2
		args = append(args, ast.Value{Kind: ast.ValueIdent, String: bind, Pos: bindPos})
	}

	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col,
			"unexpected content after match arm (write `| variant` or `| variant as name`)")
	}
	return &ast.Node{Kind: "match_arm", Pos: pos, Args: args}, nil
}

// readCellRef reads an ident with zero or more `.field` suffixes
// (`a`, `a.b`, `a.b.c`, …). Returns the combined dotted string so
// downstream code can split on '.' without re-scanning the source.
// Used by every site that accepts a cell reference in an
// expression / arg position; method-call sites still use raw
// readIdent so `items.append(...)` doesn't get misread.
//
// Chains stop at the first non-`.<ident>` lookahead — a dangling
// `.` or `.(` is left for the caller (e.g. method-call lookahead).
func readCellRef(s string, pos ast.Pos) (string, string, error) {
	name, after, err := readIdent(s, pos)
	if err != nil {
		return "", "", err
	}
	for len(after) > 1 && after[0] == '.' && isIdentStart(rune(after[1])) {
		field, after2, ferr := readIdent(after[1:], pos)
		if ferr != nil {
			break
		}
		name += "." + field
		after = after2
	}
	return name, after, nil
}

// parseToneBinding is called by the indent walker when a child line inside
// a `theme` block needs to be parsed. Two shapes:
//
//	<tone> = "#rrggbb" on "#rrggbb"   (paired tone — 3 Args)
//	<name> = "#rrggbb"                (single color — 2 Args; outline/muted)
//
// Which names admit which shape is the lowerer's call; the parser only
// distinguishes by the presence of `on`. Lives next to parseThemeDecl so
// the theme grammar reads top-to-bottom.
func parseToneBinding(s string, lineNo, startCol int) (*ast.Node, error) {
	pos := ast.Pos{Line: lineNo, Col: startCol}
	tone, rest, err := readIdent(s, pos)
	if err != nil {
		return nil, diag.New("parse", lineNo, startCol, "expected tone name")
	}
	col := startCol + len(s) - len(rest)
	n := &ast.Node{
		Kind: "tone_binding",
		Pos:  pos,
		Args: []ast.Value{{Kind: ast.ValueIdent, String: tone, Pos: pos}},
	}
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col, "expected `=` after tone name")
	}
	rest = rest[1:]
	col++
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '"' {
		return nil, diag.New("parse", lineNo, col, "expected color string (e.g. \"#ff6b35\")")
	}
	bgStr, after, err := readString(rest, ast.Pos{Line: lineNo, Col: col})
	if err != nil {
		return nil, err
	}
	bgCol := col
	col += len(rest) - len(after)
	rest = after
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueString, String: bgStr, Pos: ast.Pos{Line: lineNo, Col: bgCol}})

	rest, col = skipWS(rest, col)
	// End of line after one color = the single-color form.
	if rest == "" || strings.HasPrefix(rest, "//") {
		return n, nil
	}
	if !strings.HasPrefix(rest, "on") || (len(rest) > 2 && (isIdentRest(rune(rest[2])))) {
		return nil, diag.New("parse", lineNo, col, "expected `on` between background and foreground colors")
	}
	rest = rest[2:]
	col += 2
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '"' {
		return nil, diag.New("parse", lineNo, col, "expected foreground color string after `on`")
	}
	fgStr, after2, err := readString(rest, ast.Pos{Line: lineNo, Col: col})
	if err != nil {
		return nil, err
	}
	fgCol := col
	col += len(rest) - len(after2)
	rest = after2
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueString, String: fgStr, Pos: ast.Pos{Line: lineNo, Col: fgCol}})

	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col, "unexpected content after tone binding")
	}
	return n, nil
}

// parseThemeTextBinding is called by the indent walker for a theme body
// line starting with `text`. Shape:
//
//	text <token> = ["<Family>"] [italic] [caps] [<size> [<weight>]] [tracking <n>]
//
// At least one component must follow the `=`. The token names a
// text-scale entry: an existing one (body, heading-md, …) is
// overridden field-by-field; a new one (wordmark, mono, …) is created
// and becomes valid in `size=` kwargs. `tracking <n>` is letter-spacing
// in 1/100 em (tracking 10 = 0.1em); `caps` uppercases. AST shape:
// Kind "text_binding", Args[0] = token ident, Args[1:] = the value
// sequence in source order (string = family, idents
// italic/caps/tracking, ints = size then weight — except an int right
// after `tracking`, which is the tracking value).
func parseThemeTextBinding(s string, lineNo, startCol int) (*ast.Node, error) {
	pos := ast.Pos{Line: lineNo, Col: startCol}
	// Strip the `text` head (dispatch guaranteed the prefix).
	rest := s[len("text"):]
	col := startCol + len("text")
	rest, col = skipWS(rest, col)

	tokenPos := ast.Pos{Line: lineNo, Col: col}
	token, after, err := readIdent(rest, tokenPos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected text-scale token after `text`")
	}
	col += len(rest) - len(after)
	rest = after

	n := &ast.Node{
		Kind: "text_binding",
		Pos:  pos,
		Args: []ast.Value{{Kind: ast.ValueIdent, String: token, Pos: tokenPos}},
	}

	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col, "expected `=` after text-scale token")
	}
	rest = rest[1:]
	col++

	parts := 0
	for {
		rest, col = skipWS(rest, col)
		if rest == "" || strings.HasPrefix(rest, "//") {
			break
		}
		vPos := ast.Pos{Line: lineNo, Col: col}
		switch {
		case rest[0] == '"':
			fam, after, err := readString(rest, vPos)
			if err != nil {
				return nil, err
			}
			n.Args = append(n.Args, ast.Value{Kind: ast.ValueString, String: fam, Pos: vPos})
			col += len(rest) - len(after)
			rest = after
		case rest[0] >= '0' && rest[0] <= '9':
			i := 0
			for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
				i++
			}
			num, _ := strconv.ParseInt(rest[:i], 10, 64)
			n.Args = append(n.Args, ast.Value{Kind: ast.ValueInt, Int: num, Pos: vPos})
			col += i
			rest = rest[i:]
		default:
			ident, after, err := readIdent(rest, vPos)
			if err != nil || (ident != "italic" && ident != "caps" && ident != "tracking") {
				return nil, diag.New("parse", lineNo, col,
					"text binding values are a quoted family, `italic`, `caps`, `tracking <n>`, and integer size/weight")
			}
			n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: ident, Pos: vPos})
			col += len(rest) - len(after)
			rest = after
		}
		parts++
	}
	if parts == 0 {
		return nil, diag.New("parse", lineNo, col,
			"text binding needs at least one value after `=` (family string, `italic`, or size)")
	}
	return n, nil
}

// parseFontsDecl reads a one-line web-font source declaration:
//
//	fonts <provider> = "Family A" "Family B" …
//
// The provider names a loading strategy (today: google). Families are
// quoted strings; which weights/styles to fetch is computed at render
// time from the theme text scales that reference each family.
func parseFontsDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	provPos := ast.Pos{Line: lineNo, Col: col}
	prov, after, err := readIdent(rest, provPos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected font provider after `fonts` (e.g. google)")
	}
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: prov, Pos: provPos})
	col += len(rest) - len(after)
	rest = after

	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col, "expected `=` after font provider")
	}
	rest = rest[1:]
	col++

	families := 0
	for {
		rest, col = skipWS(rest, col)
		if rest == "" || strings.HasPrefix(rest, "//") {
			break
		}
		if rest[0] != '"' {
			return nil, diag.New("parse", lineNo, col, "font families must be quoted strings")
		}
		fPos := ast.Pos{Line: lineNo, Col: col}
		fam, after, err := readString(rest, fPos)
		if err != nil {
			return nil, err
		}
		n.Args = append(n.Args, ast.Value{Kind: ast.ValueString, String: fam, Pos: fPos})
		col += len(rest) - len(after)
		rest = after
		families++
	}
	if families == 0 {
		return nil, diag.New("parse", lineNo, col,
			"fonts declaration needs at least one quoted family after `=`")
	}
	return n, nil
}

// parseStateDecl reads `state name (: type)? (= <expr>)?`.
//
// Shape:
//   - Args[0] = name ident
//   - Args[1] = optional TypeRef value (present when `: T` appears)
//   - Children[0] = optional initial expression (present when `= …`
//     appears). Allowed-without-`=` only when a type ref is given —
//     the lowerer derives a default from the type.
//
// The expression must fit on one line; it becomes the state's
// initial value.
func parseStateDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	namePos := ast.Pos{Line: lineNo, Col: col}
	name, after, err := readIdent(rest, namePos)
	if err != nil {
		return nil, diag.New("parse", lineNo, col, "expected state cell name")
	}
	n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: name, Pos: namePos})
	col += len(rest) - len(after)
	rest = after

	// Optional `: type` — full TypeRef (record/sum/list/optional all valid).
	// The lowerer is the source of truth for what's actually supported in
	// state position; the parser is permissive.
	haveType := false
	rest, col = skipWS(rest, col)
	if rest != "" && rest[0] == ':' {
		rest = rest[1:]
		col++
		rest, col = skipWS(rest, col)
		if rest == "" || !isIdentStart(rune(rest[0])) {
			return nil, diag.New("parse", lineNo, col, "expected type after `:`")
		}
		typeRef, after2, newCol, terr := readTypeRef(rest, lineNo, col)
		if terr != nil {
			return nil, terr
		}
		n.Args = append(n.Args, typeRef)
		col = newCol
		rest = after2
		haveType = true
	}

	// `=` is required unless the declaration has a type annotation; with
	// a type, the lowerer can derive the cell's default from the type.
	rest, col = skipWS(rest, col)
	if rest == "" || strings.HasPrefix(rest, "//") {
		if !haveType {
			return nil, diag.New("parse", lineNo, col, "expected `=` in state declaration")
		}
		return n, nil
	}
	if rest[0] != '=' {
		return nil, diag.New("parse", lineNo, col, "expected `=` in state declaration")
	}
	rest = rest[1:]
	col++

	// Initial expression.
	rest, col = skipWS(rest, col)
	expr, after2, err := parseExpr(rest, lineNo, col)
	if err != nil {
		return nil, err
	}
	n.Children = append(n.Children, expr)
	col += len(rest) - len(after2)
	rest = after2

	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return nil, diag.New("parse", lineNo, col, "unexpected content after state expression")
	}
	return n, nil
}

// parseExpr parses an expression:  atom (binop atom)?
// S0 supports a single + or - between two atoms; precedence and richer
// expression forms come later.
func parseExpr(s string, lineNo, col int) (*ast.Node, string, error) {
	left, after, err := parseAtom(s, lineNo, col)
	if err != nil {
		return nil, s, err
	}
	consumed := len(s) - len(after)
	opCol := col + consumed
	rest, opCol := skipWS(after, opCol)
	if len(rest) > 0 && (rest[0] == '+' || rest[0] == '-') {
		op := string(rest[0])
		rest = rest[1:]
		opCol++
		rhsCol := opCol
		rest, rhsCol = skipWS(rest, rhsCol)
		right, after2, err := parseAtom(rest, lineNo, rhsCol)
		if err != nil {
			return nil, s, err
		}
		return &ast.Node{
			Kind:     "binop:" + op,
			Pos:      ast.Pos{Line: lineNo, Col: col},
			Children: []*ast.Node{left, right},
		}, after2, nil
	}
	return left, after, nil
}

// parseAtom parses one atom: literal int / literal string / cell ref ident
// / list literal / unary `!atom`.
func parseAtom(s string, lineNo, col int) (*ast.Node, string, error) {
	pos := ast.Pos{Line: lineNo, Col: col}
	if len(s) == 0 {
		return nil, s, diag.New("parse", lineNo, col, "expected expression")
	}
	switch {
	case s[0] == '!':
		// Unary not. Lowering recognizes `cell = !cell` (same cell) as a
		// toggle action; other shapes error.
		inner, after, err := parseAtom(s[1:], lineNo, col+1)
		if err != nil {
			return nil, s, err
		}
		return &ast.Node{
			Kind:     "not",
			Pos:      pos,
			Children: []*ast.Node{inner},
		}, after, nil
	case s[0] == '"':
		str, after, err := readString(s, pos)
		if err != nil {
			return nil, s, err
		}
		return &ast.Node{
			Kind: "lit", Pos: pos,
			Args: []ast.Value{{Kind: ast.ValueString, String: str, Pos: pos}},
		}, after, nil
	case s[0] == '-' && len(s) > 1 && isDigit(rune(s[1])):
		v, after, err := readInt(s, pos)
		if err != nil {
			return nil, s, err
		}
		return &ast.Node{
			Kind: "lit", Pos: pos,
			Args: []ast.Value{{Kind: ast.ValueInt, Int: v, Pos: pos}},
		}, after, nil
	case isDigit(rune(s[0])):
		v, after, err := readInt(s, pos)
		if err != nil {
			return nil, s, err
		}
		return &ast.Node{
			Kind: "lit", Pos: pos,
			Args: []ast.Value{{Kind: ast.ValueInt, Int: v, Pos: pos}},
		}, after, nil
	case isIdentStart(rune(s[0])):
		// readCellRef accepts an optional ".field" so structured-row
		// access (item.done) reads as one expression atom. Lower decides
		// what to do with the dot.
		id, after, err := readCellRef(s, pos)
		if err != nil {
			return nil, s, err
		}
		// Op-call expression: a bare ident (no dots, paren follows)
		// is `Name(args…)` — calls a declared query / command. Lower
		// validates the name and resolves args.
		if !strings.Contains(id, ".") && len(after) > 0 && after[0] == '(' {
			callCol := col + (len(s) - len(after))
			return parseOpCallTail(id, pos, after, lineNo, callCol, "op_call")
		}
		return &ast.Node{
			Kind: "ref", Pos: pos,
			Args: []ast.Value{{Kind: ast.ValueIdent, String: id, Pos: pos}},
		}, after, nil
	case s[0] == '[':
		// List literal: `[<atom> (, <atom>)*]`. Each element is itself an
		// atom (so lists of lists are syntactically permitted; lowering
		// decides what to do).
		rest := s[1:]
		elemCol := col + 1
		listNode := &ast.Node{Kind: "list_lit", Pos: pos}
		rest, elemCol = skipWS(rest, elemCol)
		if len(rest) > 0 && rest[0] == ']' {
			return listNode, rest[1:], nil // empty list
		}
		for {
			elem, after, err := parseAtom(rest, lineNo, elemCol)
			if err != nil {
				return nil, s, err
			}
			elemCol += len(rest) - len(after)
			rest = after
			listNode.Children = append(listNode.Children, elem)
			rest, elemCol = skipWS(rest, elemCol)
			if len(rest) > 0 && rest[0] == ',' {
				rest = rest[1:]
				elemCol++
				rest, elemCol = skipWS(rest, elemCol)
				continue
			}
			if len(rest) > 0 && rest[0] == ']' {
				return listNode, rest[1:], nil
			}
			return nil, s, diag.New("parse", lineNo, elemCol, "expected `,` or `]` in list literal")
		}
	default:
		return nil, s, diag.New("parse", lineNo, col, "expected expression (got "+strconv.Quote(string(s[0]))+")")
	}
}

// parseOpCallTail reads the `(arg, arg, ...)` portion of an op call,
// given the op name + position already consumed by the caller. s must
// start with `(`. kind controls the resulting AST node Kind, so the
// same logic serves both the expression position (`op_call`) and
// the statement position (`op_call_stmt`).
//
// Arguments are full expressions — literals, cell refs, and binops
// all compose. Validation that the names match a declared op (and
// arity matches its signature) lives in the lowerer.
func parseOpCallTail(name string, namePos ast.Pos, s string, lineNo, col int, kind string) (*ast.Node, string, error) {
	if len(s) == 0 || s[0] != '(' {
		return nil, s, diag.New("parse", lineNo, col, "expected `(` to open op call args")
	}
	n := &ast.Node{
		Kind: kind,
		Pos:  namePos,
		Args: []ast.Value{{Kind: ast.ValueIdent, String: name, Pos: namePos}},
	}
	rest := s[1:]
	col++
	rest, col = skipWS(rest, col)
	if len(rest) > 0 && rest[0] == ')' {
		return n, rest[1:], nil
	}
	for {
		argExpr, after, err := parseExpr(rest, lineNo, col)
		if err != nil {
			return nil, s, err
		}
		col += len(rest) - len(after)
		rest = after
		n.Children = append(n.Children, argExpr)
		rest, col = skipWS(rest, col)
		if len(rest) > 0 && rest[0] == ',' {
			rest = rest[1:]
			col++
			rest, col = skipWS(rest, col)
			continue
		}
		if len(rest) > 0 && rest[0] == ')' {
			return n, rest[1:], nil
		}
		return nil, s, diag.New("parse", lineNo, col, "expected `,` or `)` in op call args")
	}
}

// parseTupleStreamAssign parses `(t1, t2, ...) <- StreamOp(args)`: a
// multi-channel stream-into. Each target is a dotted chain (so both the
// scalar form `(thinking, answer)` and the transcript row form
// `(conv.last.thinking, conv.last.answer)` parse). The targets map
// positionally to the channels of the op's record-typed delta; lowering
// validates the op, the record return type, and that every target is a
// String cell / field.
//
// AST shape mirrors the single-target `stream_assign` but with N target
// idents in Args:
//   - Kind     = "stream_assign"
//   - Args     = one ValueIdent per target (dotted string preserved)
//   - Children = [rhs op_call]
func parseTupleStreamAssign(s string, lineNo, col int) (*ast.Node, string, error) {
	pos := ast.Pos{Line: lineNo, Col: col}
	rest := s[1:] // consume '('
	col++
	var targets []ast.Value
	for {
		rest, col = skipWS(rest, col)
		if rest == "" {
			return nil, s, diag.New("parse", lineNo, col,
				"unterminated `(` in stream-into target list")
		}
		if rest[0] == ')' {
			if len(targets) == 0 {
				return nil, s, diag.New("parse", lineNo, col,
					"stream-into target list cannot be empty")
			}
			return nil, s, diag.New("parse", lineNo, col,
				"stream-into target list needs at least two targets; use `lhs <- Op(...)` for a single target")
		}
		// Read a dotted target chain: ident ( "." ident )*.
		tPos := ast.Pos{Line: lineNo, Col: col}
		name, after, err := readIdent(rest, tPos)
		if err != nil {
			return nil, s, diag.New("parse", lineNo, col,
				"expected target name in stream-into target list")
		}
		col += len(rest) - len(after)
		rest = after
		for len(rest) > 1 && rest[0] == '.' && isIdentStart(rune(rest[1])) {
			segStart := rest[1:]
			segName, after2, ferr := readIdent(segStart, ast.Pos{Line: lineNo, Col: col + 1})
			if ferr != nil {
				break
			}
			name += "." + segName
			col += 1 + len(segStart) - len(after2)
			rest = after2
		}
		targets = append(targets, ast.Value{Kind: ast.ValueIdent, String: name, Pos: tPos})
		rest, col = skipWS(rest, col)
		if rest == "" {
			return nil, s, diag.New("parse", lineNo, col,
				"unterminated `(` in stream-into target list")
		}
		if rest[0] == ',' {
			rest = rest[1:]
			col++
			continue
		}
		if rest[0] == ')' {
			rest = rest[1:]
			col++
			break
		}
		return nil, s, diag.New("parse", lineNo, col,
			"expected `,` or `)` in stream-into target list")
	}
	if len(targets) < 2 {
		return nil, s, diag.New("parse", lineNo, col,
			"stream-into target list needs at least two targets; use `lhs <- Op(...)` for a single target")
	}

	rest, col = skipWS(rest, col)
	if !strings.HasPrefix(rest, "<-") {
		return nil, s, diag.New("parse", lineNo, col,
			"expected `<-` after stream-into target list `(...)`")
	}
	rest = rest[2:]
	col += 2
	rest, col = skipWS(rest, col)
	rhs, after, perr := parseExpr(rest, lineNo, col)
	if perr != nil {
		return nil, s, perr
	}
	if rhs.Kind != "op_call" {
		return nil, s, diag.New("parse", lineNo, col,
			"`<-` expects a stream op call on the right (e.g. `(thinking, answer) <- Chat(prompt)`)")
	}
	return &ast.Node{
		Kind:     "stream_assign",
		Pos:      pos,
		Args:     targets,
		Children: []*ast.Node{rhs},
	}, after, nil
}

// parseStmt parses one statement. Forms:
//
//	lhs = expr            plain assignment
//	lhs += expr           desugars to lhs = lhs + expr
//	lhs -= expr           desugars to lhs = lhs - expr
//	receiver.method(args) method call (e.g. items.append(0), items.remove(item))
//	OpName(args)          fire-and-forget op call (queries / commands)
//
// The compound forms reuse the binop AST so lowering's existing pattern
// matcher catches `cell = cell ± literal` (same cell) regardless of which
// surface form the author wrote.
// chompWord returns (true, rest) when s begins with word followed by
// whitespace or end-of-string; otherwise (false, s). Used to peel the
// `then` / `navigate` keywords without misreading `thenx`.
func chompWord(s, word string) (bool, string) {
	if !strings.HasPrefix(s, word) {
		return false, s
	}
	rest := s[len(word):]
	if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
		return true, rest
	}
	return false, s
}

// parseNavigateClause reads the `"<path>"` of a navigate action (the
// `navigate` keyword is consumed by the caller) into a "navigate" node
// with Args[0] = the path string.
func parseNavigateClause(s string, pos ast.Pos, lineNo, col int) (*ast.Node, string, error) {
	s, col = skipWS(s, col)
	if s == "" || s[0] != '"' {
		return nil, s, diag.New("parse", lineNo, col, "navigate expects a quoted path, e.g. `navigate \"/\"`")
	}
	strPos := ast.Pos{Line: lineNo, Col: col}
	path, after, err := readString(s, strPos)
	if err != nil {
		return nil, s, err
	}
	return &ast.Node{
		Kind: "navigate",
		Pos:  pos,
		Args: []ast.Value{{Kind: ast.ValueString, String: path, Pos: strPos}},
	}, after, nil
}

func parseStmt(s string, lineNo, col int) (*ast.Node, string, error) {
	pos := ast.Pos{Line: lineNo, Col: col}

	// Tuple stream-into: `(a, b, ...) <- StreamOp(args)` fans one stream
	// op (one backend request) into N targets, each bound to a channel of
	// a record-typed delta. Targets are dotted chains so the transcript
	// form `(conv.last.thinking, conv.last.answer) <- Chat(...)` works.
	// Distinct from the single-target `lhs <- StreamOp(...)` below.
	if len(s) > 0 && s[0] == '(' {
		return parseTupleStreamAssign(s, lineNo, col)
	}

	lhsName, after, err := readIdent(s, pos)
	if err != nil {
		return nil, s, diag.New("parse", lineNo, col, "expected cell name in statement")
	}
	rest := after
	col += len(s) - len(after)

	// `navigate "<path>"` — full-page navigation action. Reserved as a
	// statement head only when followed by a string, so a cell that
	// happens to be named `navigate` (assigned with `=`) still parses.
	if lhsName == "navigate" {
		peek := strings.TrimLeft(rest, " \t")
		if len(peek) > 0 && peek[0] == '"' {
			nav, navAfter, err := parseNavigateClause(rest, pos, lineNo, col)
			if err != nil {
				return nil, s, err
			}
			return nav, navAfter, nil
		}
	}

	// Op-call statement: a bare ident directly followed by `(` is a
	// fire-and-forget call to a declared query / command. No
	// whitespace between name and `(` so it doesn't shadow other
	// statement forms with leading ws-then-paren. An optional
	// `then navigate "<path>"` success hook may trail the call — it
	// runs only after the op resolves without error (see lowering).
	if len(rest) > 0 && rest[0] == '(' {
		node, callAfter, err := parseOpCallTail(lhsName, pos, rest, lineNo, col, "op_call_stmt")
		if err != nil {
			return nil, s, err
		}
		tail := strings.TrimLeft(callAfter, " \t")
		if w, wrest := chompWord(tail, "then"); w {
			wrest = strings.TrimLeft(wrest, " \t")
			nw, nrest := chompWord(wrest, "navigate")
			if !nw {
				return nil, s, diag.New("parse", lineNo, col, "`then` must be followed by `navigate \"<path>\"`")
			}
			nav, navAfter, nerr := parseNavigateClause(nrest, pos, lineNo, col)
			if nerr != nil {
				return nil, s, nerr
			}
			if node.Kwargs == nil {
				node.Kwargs = map[string]ast.Value{}
			}
			node.Kwargs["then_navigate"] = nav.Args[0]
			return node, navAfter, nil
		}
		return node, callAfter, nil
	}

	rest, col = skipWS(rest, col)
	if rest == "" {
		return nil, s, diag.New("parse", lineNo, col, "expected `=`, `+=`, `-=`, or `.method(...)` in statement")
	}

	// `.` after the receiver branches: `.method(...)` is a method call;
	// a chain of `.field` segments followed by `=`/`+=`/`-=` is a
	// sub-field assignment on a (possibly nested) structured-row cell.
	// Greedily consume the chain, then peek the operator to decide
	// whether to treat it as an assignment or hand off to method-call
	// parsing. We never split mid-chain — `a.b.c.method(...)` is a
	// method call on `a.b.c`, `a.b.c = …` is an assignment to it.
	if rest[0] == '.' {
		chain := lhsName
		chainRest := rest
		chainCol := col
		for len(chainRest) > 1 && chainRest[0] == '.' && isIdentStart(rune(chainRest[1])) {
			segStart := chainRest[1:]
			segName, after2, ferr := readIdent(segStart, ast.Pos{Line: lineNo, Col: chainCol + 1})
			if ferr != nil {
				break
			}
			chain += "." + segName
			chainCol += 1 + len(segStart) - len(after2)
			chainRest = after2
		}
		peek := strings.TrimLeft(chainRest, " \t")
		if len(peek) > 0 && (((peek[0] == '=' || peek[0] == '+' || peek[0] == '-') &&
			!strings.HasPrefix(peek, "==")) || strings.HasPrefix(peek, "<-")) {
			// Field-chain assignment: lhsName(.seg)+ = expr
			lhsName = chain
			col = chainCol
			rest = chainRest
			// Re-skip WS so the `=`/`+=`/`-=` reader below sees the
			// operator at rest[0]. Fall through.
			rest, col = skipWS(rest, col)
		} else {
			// Anything else after the chain → method call (existing path).
			return parseMethodCall(lhsName, pos, rest, lineNo, col)
		}
	}

	// Stream-into: `lhs <- StreamOp(args)` progressively fills lhs with
	// the deltas the stream op emits. Distinct from `=` (one atomic set)
	// — the arrow reads like a channel receive. The RHS must be an op
	// call; lowering checks it resolves to a declared `stream`.
	if strings.HasPrefix(rest, "<-") {
		rest = rest[2:]
		col += 2
		rest, col = skipWS(rest, col)
		rhs, after2, perr := parseExpr(rest, lineNo, col)
		if perr != nil {
			return nil, s, perr
		}
		if rhs.Kind != "op_call" {
			return nil, s, diag.New("parse", lineNo, col,
				"`<-` expects a stream op call on the right (e.g. `reply <- Chat(prompt)`)")
		}
		return &ast.Node{
			Kind:     "stream_assign",
			Pos:      pos,
			Args:     []ast.Value{{Kind: ast.ValueIdent, String: lhsName, Pos: pos}},
			Children: []*ast.Node{rhs},
		}, after2, nil
	}

	// Recognize `+=` and `-=` before plain `=`.
	var op string // "" for plain assignment; "+" or "-" for compound
	switch {
	case strings.HasPrefix(rest, "+="):
		op = "+"
		rest = rest[2:]
		col += 2
	case strings.HasPrefix(rest, "-="):
		op = "-"
		rest = rest[2:]
		col += 2
	case rest[0] == '=':
		if len(rest) > 1 && rest[1] == '=' {
			return nil, s, diag.New("parse", lineNo, col, "`==` not supported — handlers only do assignment")
		}
		rest = rest[1:]
		col++
	default:
		return nil, s, diag.New("parse", lineNo, col, "expected `=`, `+=`, or `-=` in statement")
	}

	rest, col = skipWS(rest, col)
	rhs, after2, err := parseExpr(rest, lineNo, col)
	if err != nil {
		return nil, s, err
	}

	// Desugar compound: lhs += rhs  →  lhs = lhs + rhs
	if op != "" {
		lhsRef := &ast.Node{
			Kind: "ref",
			Pos:  pos,
			Args: []ast.Value{{Kind: ast.ValueIdent, String: lhsName, Pos: pos}},
		}
		rhs = &ast.Node{
			Kind:     "binop:" + op,
			Pos:      pos,
			Children: []*ast.Node{lhsRef, rhs},
		}
	}

	return &ast.Node{
		Kind:     "assign",
		Pos:      pos,
		Args:     []ast.Value{{Kind: ast.ValueIdent, String: lhsName, Pos: pos}},
		Children: []*ast.Node{rhs},
	}, after2, nil
}

// parseMethodCall reads `receiver.method(arg1, arg2, ...)` after the
// receiver and the leading `.` are positioned. Returns a "method_call"
// AST node with Args[0]=receiver, Args[1]=method, and one child per arg.
func parseMethodCall(receiver string, recvPos ast.Pos, s string, lineNo, col int) (*ast.Node, string, error) {
	// s[0] is `.`
	s = s[1:]
	col++
	methodPos := ast.Pos{Line: lineNo, Col: col}
	method, after, err := readIdent(s, methodPos)
	if err != nil {
		return nil, s, diag.New("parse", lineNo, col, "expected method name after `.`")
	}
	col += len(s) - len(after)
	s = after

	s, col = skipWS(s, col)
	if s == "" || s[0] != '(' {
		return nil, s, diag.New("parse", lineNo, col, "expected `(` after method name")
	}
	s = s[1:]
	col++

	node := &ast.Node{
		Kind: "method_call",
		Pos:  recvPos,
		Args: []ast.Value{
			{Kind: ast.ValueIdent, String: receiver, Pos: recvPos},
			{Kind: ast.ValueIdent, String: method, Pos: methodPos},
		},
	}

	s, col = skipWS(s, col)
	if s != "" && s[0] == ')' {
		return node, s[1:], nil
	}
	for {
		argExpr, after, err := parseExpr(s, lineNo, col)
		if err != nil {
			return nil, s, err
		}
		col += len(s) - len(after)
		s = after
		node.Children = append(node.Children, argExpr)
		s, col = skipWS(s, col)
		if s != "" && s[0] == ',' {
			s = s[1:]
			col++
			s, col = skipWS(s, col)
			continue
		}
		if s != "" && s[0] == ')' {
			return node, s[1:], nil
		}
		return nil, s, diag.New("parse", lineNo, col, "expected `,` or `)` in method call")
	}
}

// parseDecl reads the args + `=` boundary + optional inline body of a
// declaration line.
func parseDecl(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	rest, col = skipWS(rest, col)
	if rest == "" || rest[0] == '=' {
		return nil, diag.New("parse", lineNo, col, "expected name after \""+n.Kind+"\"")
	}

	// First arg: the name.
	name, after, err := readIdent(rest, ast.Pos{Line: lineNo, Col: col})
	if err != nil {
		return nil, err
	}
	n.Args = append(n.Args, ast.Value{
		Kind: ast.ValueIdent, String: name,
		Pos: ast.Pos{Line: lineNo, Col: col},
	})
	col += len(rest) - len(after)
	rest = after

	for {
		rest, col = skipWS(rest, col)
		if rest == "" {
			return nil, diag.New("parse", lineNo, col, "expected `=` to start body")
		}
		if rest[0] == '=' {
			rest = rest[1:]
			col++
			break
		}
		if !strings.HasPrefix(rest, "->") {
			return nil, diag.New("parse", lineNo, col, "expected `->` or `=`")
		}
		rest = rest[2:]
		col += 2
		rest, col = skipWS(rest, col)

		// Optional leading `*` marks a variadic param. Only legal for
		// `component` decls — `view` doesn't have parameters in v0, so
		// reject up front for clarity.
		variadic := false
		if rest != "" && rest[0] == '*' {
			if n.Kind != "component" {
				return nil, diag.New("parse", lineNo, col,
					"`*name` variadic params are only valid on component decls")
			}
			variadic = true
			rest = rest[1:]
			col++
		}

		argPos := ast.Pos{Line: lineNo, Col: col}
		argName, after, err := readIdent(rest, argPos)
		if err != nil {
			return nil, diag.New("parse", lineNo, col, "expected arg name after `->`")
		}
		n.Args = append(n.Args, ast.Value{
			Kind: ast.ValueIdent, String: argName, Pos: argPos, Variadic: variadic,
		})
		col += len(rest) - len(after)
		rest = after

		// Optional `: typeExpr` — swallowed for forward compatibility.
		rest, col = skipWS(rest, col)
		if rest != "" && rest[0] == ':' {
			rest = rest[1:]
			col++
			rest, col = skipWS(rest, col)
			if rest == "" || !isIdentStart(rune(rest[0])) {
				return nil, diag.New("parse", lineNo, col, "expected type after `:`")
			}
			_, after, err := readIdent(rest, ast.Pos{Line: lineNo, Col: col})
			if err != nil {
				return nil, err
			}
			col += len(rest) - len(after)
			rest = after
		}
	}

	// Inline body, if anything is left on the line.
	rest, col = skipWS(rest, col)
	if rest != "" && !strings.HasPrefix(rest, "//") {
		body, err := parseLine(rest, lineNo, col)
		if err != nil {
			return nil, err
		}
		n.Children = append(n.Children, body)
	}
	return n, nil
}

// parseInvocation reads positional and keyword args until end of line.
func parseInvocation(n *ast.Node, rest string, lineNo, col int) (*ast.Node, error) {
	for {
		rest, col = skipWS(rest, col)
		if rest == "" || strings.HasPrefix(rest, "//") {
			break
		}
		argPos := ast.Pos{Line: lineNo, Col: col}
		switch {
		case rest[0] == '"':
			str, after, err := readString(rest, argPos)
			if err != nil {
				return nil, err
			}
			n.Args = append(n.Args, ast.Value{Kind: ast.ValueString, String: str, Pos: argPos})
			col += len(rest) - len(after)
			rest = after

		case rest[0] == '-' && len(rest) > 1 && isDigit(rune(rest[1])):
			v, after, err := readInt(rest, argPos)
			if err != nil {
				return nil, err
			}
			n.Args = append(n.Args, ast.Value{Kind: ast.ValueInt, Int: v, Pos: argPos})
			col += len(rest) - len(after)
			rest = after

		case isDigit(rune(rest[0])):
			v, after, err := readInt(rest, argPos)
			if err != nil {
				return nil, err
			}
			n.Args = append(n.Args, ast.Value{Kind: ast.ValueInt, Int: v, Pos: argPos})
			col += len(rest) - len(after)
			rest = after

		case isIdentStart(rune(rest[0])):
			id, after, err := readIdent(rest, argPos)
			if err != nil {
				return nil, err
			}
			col += len(rest) - len(after)
			rest = after
			// `name=value` kwarg (no whitespace allowed around `=`).
			// Dotted form `name.field` is NOT valid as a kwarg key, so
			// the dot check happens AFTER the `=` check below.
			if len(rest) > 0 && rest[0] == '=' {
				rest = rest[1:]
				col++
				val, after2, consumed, err := readValue(rest, lineNo, col)
				if err != nil {
					return nil, err
				}
				if n.Kwargs == nil {
					n.Kwargs = map[string]ast.Value{}
				}
				if _, dup := n.Kwargs[id]; dup {
					return nil, diag.New("parse", lineNo, argPos.Col, "duplicate kwarg \""+id+"\"")
				}
				n.Kwargs[id] = val
				col += consumed
				rest = after2
				continue
			}
			// `on <event> { <stmt> }` handler clause. We special-case the
			// "on" ident here so it can't accidentally be read as a flag.
			if id == "on" && len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
				rest, col = skipWS(rest, col)
				eventPos := ast.Pos{Line: lineNo, Col: col}
				event, after2, err := readIdent(rest, eventPos)
				if err != nil {
					return nil, diag.New("parse", lineNo, col, "expected event name after `on`")
				}
				col += len(rest) - len(after2)
				rest = after2
				rest, col = skipWS(rest, col)
				if rest == "" || rest[0] != '{' {
					return nil, diag.New("parse", lineNo, col, "expected `{` to open handler block")
				}
				rest = rest[1:]
				col++
				rest, col = skipWS(rest, col)
				// Handler body is one or more statements separated by `;`.
				// Single-statement form is the common case and stays a
				// single AST node; multi-statement wraps in a "seq" node.
				var stmts []*ast.Node
				for {
					stmt, after3, err := parseStmt(rest, lineNo, col)
					if err != nil {
						return nil, err
					}
					col += len(rest) - len(after3)
					rest = after3
					stmts = append(stmts, stmt)
					rest, col = skipWS(rest, col)
					if rest != "" && rest[0] == ';' {
						rest = rest[1:]
						col++
						rest, col = skipWS(rest, col)
						continue
					}
					break
				}
				if rest == "" || rest[0] != '}' {
					return nil, diag.New("parse", lineNo, col, "expected `}` or `;` to close handler block")
				}
				rest = rest[1:]
				col++
				if n.Handlers == nil {
					n.Handlers = map[string]*ast.Node{}
				}
				if _, dup := n.Handlers[event]; dup {
					return nil, diag.New("parse", lineNo, eventPos.Col, "duplicate handler for `"+event+"`")
				}
				if len(stmts) == 1 {
					n.Handlers[event] = stmts[0]
				} else {
					n.Handlers[event] = &ast.Node{
						Kind:     "seq",
						Pos:      stmts[0].Pos,
						Children: stmts,
					}
				}
				continue
			}
			// Positional ident arg — accept zero or more `.field` segments
			// for structured-row / nested-record access (`text item.label`,
			// `text p.stats.hp`). Mirrors readCellRef in expression
			// position; kept inline because parseInvocation tracks its
			// own column counter.
			for len(rest) > 1 && rest[0] == '.' && isIdentStart(rune(rest[1])) {
				field, after2, ferr := readIdent(rest[1:], argPos)
				if ferr != nil {
					break
				}
				id = id + "." + field
				col += 1 + len(rest[1:]) - len(after2)
				rest = after2
			}
			n.Args = append(n.Args, ast.Value{Kind: ast.ValueIdent, String: id, Pos: argPos})

		case strings.HasPrefix(rest, "->"):
			d := diag.New("parse", lineNo, col, "unexpected `->`")
			d.Suggestion = "did you mean to add `=` to declare this?"
			return nil, d
		case rest[0] == '=':
			return nil, diag.New("parse", lineNo, col, "unexpected `=`")
		case rest[0] == '*':
			return nil, diag.New("parse", lineNo, col,
				"`*name` is only allowed as a parameter declaration or as a body line; argument-position spread is not supported")
		default:
			return nil, diag.New("parse", lineNo, col, "unexpected character "+strconv.Quote(string(rest[0])))
		}
	}
	return n, nil
}

// readValue reads one value-shaped token (string, int, or bare ident).
func readValue(s string, lineNo, col int) (ast.Value, string, int, error) {
	pos := ast.Pos{Line: lineNo, Col: col}
	if len(s) == 0 {
		return ast.Value{}, s, 0, diag.New("parse", lineNo, col, "expected value")
	}
	switch {
	case s[0] == '"':
		str, after, err := readString(s, pos)
		if err != nil {
			return ast.Value{}, s, 0, err
		}
		return ast.Value{Kind: ast.ValueString, String: str, Pos: pos}, after, len(s) - len(after), nil
	case s[0] == '-' && len(s) > 1 && isDigit(rune(s[1])):
		v, after, err := readInt(s, pos)
		if err != nil {
			return ast.Value{}, s, 0, err
		}
		return ast.Value{Kind: ast.ValueInt, Int: v, Pos: pos}, after, len(s) - len(after), nil
	case isDigit(rune(s[0])):
		v, after, err := readInt(s, pos)
		if err != nil {
			return ast.Value{}, s, 0, err
		}
		return ast.Value{Kind: ast.ValueInt, Int: v, Pos: pos}, after, len(s) - len(after), nil
	case isIdentStart(rune(s[0])):
		// Accept dotted cell refs (`featured.hp`, `item.stats.hp`) as
		// kwarg values — the bar's `value=` kwarg and any future
		// primitive that wants to point at a nested cell needs this.
		// Single idents come through unchanged.
		id, after, err := readCellRef(s, pos)
		if err != nil {
			return ast.Value{}, s, 0, err
		}
		return ast.Value{Kind: ast.ValueIdent, String: id, Pos: pos}, after, len(s) - len(after), nil
	default:
		return ast.Value{}, s, 0, diag.New("parse", lineNo, col, "expected value (got "+strconv.Quote(string(s[0]))+")")
	}
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func readInt(s string, pos ast.Pos) (int64, string, error) {
	if len(s) == 0 {
		return 0, s, diag.New("parse", pos.Line, pos.Col, "expected integer")
	}
	i := 0
	if s[0] == '-' {
		i++
	}
	if i >= len(s) || !isDigit(rune(s[i])) {
		return 0, s, diag.New("parse", pos.Line, pos.Col, "expected integer")
	}
	for i < len(s) && isDigit(rune(s[i])) {
		i++
	}
	v, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil {
		return 0, s, diag.New("parse", pos.Line, pos.Col, "invalid integer: "+err.Error())
	}
	return v, s[i:], nil
}

func skipWS(s string, col int) (string, int) {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
		col++
	}
	return s, col
}

func isIdentStart(r rune) bool { return unicode.IsLetter(r) || r == '_' }
func isIdentRest(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

func readIdent(s string, pos ast.Pos) (string, string, error) {
	if len(s) == 0 || !isIdentStart(rune(s[0])) {
		return "", "", diag.New("parse", pos.Line, pos.Col, "expected identifier")
	}
	i := 0
	for i < len(s) && isIdentRest(rune(s[i])) {
		i++
	}
	return s[:i], s[i:], nil
}

func readString(s string, pos ast.Pos) (string, string, error) {
	if len(s) == 0 || s[0] != '"' {
		return "", "", diag.New("parse", pos.Line, pos.Col, "expected string")
	}
	var b strings.Builder
	i := 1
	for i < len(s) {
		c := s[i]
		if c == '"' {
			return b.String(), s[i+1:], nil
		}
		if c == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				return "", "", diag.New("parse", pos.Line, pos.Col+i, "unknown escape \\"+string(s[i]))
			}
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	return "", "", diag.New("parse", pos.Line, pos.Col, "unterminated string")
}
