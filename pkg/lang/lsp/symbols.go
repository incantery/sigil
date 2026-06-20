package lsp

import (
	"strings"

	"github.com/incantery/sigil/pkg/lang/ast"
)

// symbolIndex is the navigation view of one file: every top-level
// declaration plus the locals (params, state cells, for-vars, fields)
// scoped inside each. Built from the buffer's own AST, so navigation
// is in-file at v1 — cross-package jumps arrive with workspace
// indexing later.
type symbolIndex struct {
	decls []*declSym
}

type declSym struct {
	kind   string // "view", "component", "type", …
	name   string
	line   int // name token position
	col    int
	length int
	start  int // subtree extent in lines (for scoping + symbol ranges)
	end    int
	detail string
	locals []localSym
}

type localSym struct {
	kind   string // "param", "state", "for", "field", "variant", "tone"
	name   string
	line   int
	col    int
	length int
	start  int // scope extent in lines
	end    int
}

func buildIndex(doc *document, root *ast.Node) *symbolIndex {
	idx := &symbolIndex{}
	if root == nil {
		return idx
	}
	decls := []*ast.Node{root}
	if root.Kind == "module" || root.Kind == "__root__" {
		decls = root.Children
	}
	for _, n := range decls {
		if n.Kind == "__error__" {
			continue
		}
		d := declFor(doc, n)
		if d != nil {
			idx.decls = append(idx.decls, d)
		}
	}
	return idx
}

func declFor(doc *document, n *ast.Node) *declSym {
	if len(n.Args) == 0 {
		return nil
	}
	name := n.Args[0]
	d := &declSym{
		kind:   n.Kind,
		name:   name.String,
		line:   name.Pos.Line,
		col:    name.Pos.Col,
		length: len(name.String),
		start:  n.Pos.Line,
		end:    maxLine(n),
		detail: sourceLine(doc, n.Pos.Line),
	}
	if name.Kind == ast.ValueString {
		// test/story names are quoted phrases; the stored length is the
		// decoded length, good enough for a selection range.
		d.length = len(name.String) + 2
	}

	switch n.Kind {
	case "view", "app", "component":
		for _, p := range n.Args[1:] {
			d.locals = append(d.locals, localSym{
				kind: "param", name: p.String,
				line: p.Pos.Line, col: p.Pos.Col, length: len(p.String),
				start: d.start, end: d.end,
			})
		}
		collectBodyLocals(d, n.Children)
	case "session":
		collectBodyLocals(d, n.Children)
	case "type":
		for _, c := range n.Children {
			switch c.Kind {
			case "field_decl":
				d.locals = append(d.locals, fieldLocal(c, "field", d))
			case "variant_decl":
				if len(c.Args) > 0 {
					d.locals = append(d.locals, fieldLocal(c, "variant", d))
				}
			}
		}
	case "theme":
		for _, c := range n.Children {
			if (c.Kind == "tone_binding" || c.Kind == "text_binding") && len(c.Args) > 0 {
				d.locals = append(d.locals, fieldLocal(c, "tone", d))
			}
		}
	case "query", "command", "stream":
		for _, c := range n.Children {
			if c.Kind == "field_decl" {
				d.locals = append(d.locals, fieldLocal(c, "param", d))
			}
		}
	}
	return d
}

func fieldLocal(c *ast.Node, kind string, d *declSym) localSym {
	name := c.Args[0]
	return localSym{
		kind: kind, name: name.String,
		line: name.Pos.Line, col: name.Pos.Col, length: len(name.String),
		start: d.start, end: d.end,
	}
}

// collectBodyLocals walks a view/session body for state decls and for
// loops. State scope is the whole decl; a for-var's scope is its loop
// subtree.
func collectBodyLocals(d *declSym, children []*ast.Node) {
	var walk func(ns []*ast.Node)
	walk = func(ns []*ast.Node) {
		for _, c := range ns {
			switch c.Kind {
			case "state":
				if len(c.Args) > 0 {
					d.locals = append(d.locals, fieldLocal(c, "state", d))
				}
			case "for":
				if len(c.Args) > 0 {
					v := c.Args[0]
					d.locals = append(d.locals, localSym{
						kind: "for", name: v.String,
						line: v.Pos.Line, col: v.Pos.Col, length: len(v.String),
						start: c.Pos.Line, end: maxLine(c),
					})
				}
			}
			walk(c.Children)
		}
	}
	walk(children)
}

func maxLine(n *ast.Node) int {
	m := n.Pos.Line
	for _, c := range n.Children {
		if l := maxLine(c); l > m {
			m = l
		}
	}
	return m
}

func sourceLine(doc *document, line int) string {
	if line-1 < 0 || line-1 >= len(doc.lines) {
		return ""
	}
	return strings.TrimSpace(doc.lines[line-1])
}

// ───────────── documentSymbol ────────────────────────────────────────

var declSymbolKinds = map[string]int{
	"view": symClass, "app": symClass, "component": symFunction,
	"type": symStruct, "theme": symObject, "session": symNamespace,
	"backend": symObject, "icons": symNamespace, "fonts": symNamespace,
	"query": symFunction, "command": symFunction, "stream": symFunction,
	"test": symEvent, "story": symEvent, "import": symNamespace,
	"state": symField,
}

var localSymbolKinds = map[string]int{
	"param": symVariable, "state": symField, "for": symVariable,
	"field": symField, "variant": symEnumMember, "tone": symProperty,
}

func documentSymbols(doc *document) []documentSymbol {
	idx := doc.an.index
	out := make([]documentSymbol, 0, len(idx.decls))
	for _, d := range idx.decls {
		kind, ok := declSymbolKinds[d.kind]
		if !ok {
			kind = symObject
		}
		sym := documentSymbol{
			Name:           d.name,
			Detail:         d.kind,
			Kind:           kind,
			Range:          lineSpanRange(doc, d.start, d.end),
			SelectionRange: protoRange(doc.lines, d.line, d.col, d.length),
		}
		for _, l := range d.locals {
			if l.kind == "for" {
				continue // loop vars are noise in an outline
			}
			lk, ok := localSymbolKinds[l.kind]
			if !ok {
				lk = symField
			}
			sym.Children = append(sym.Children, documentSymbol{
				Name:           l.name,
				Detail:         l.kind,
				Kind:           lk,
				Range:          protoRange(doc.lines, l.line, l.col, l.length),
				SelectionRange: protoRange(doc.lines, l.line, l.col, l.length),
			})
		}
		out = append(out, sym)
	}
	return out
}

func lineSpanRange(doc *document, start, end int) Range {
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start
	}
	endLen := 0
	if end-1 < len(doc.lines) {
		endLen = len(doc.lines[end-1])
	}
	return Range{
		Start: Position{Line: start - 1, Character: 0},
		End:   Position{Line: end - 1, Character: utf16Col(doc.lines[min(end, len(doc.lines))-1], endLen+1)},
	}
}

// ───────────── definition + hover ────────────────────────────────────

// identAt finds the named token covering the LSP position.
func identAt(doc *document, pos Position) *tok {
	line := pos.Line + 1
	if line-1 >= len(doc.lines) {
		return nil
	}
	col := byteCol(doc.lines[line-1], pos.Character)
	toks := buildTokens(doc)
	for i := range toks {
		t := &toks[i]
		if t.name == "" || t.line != line {
			continue
		}
		if col >= t.col && col < t.col+t.length {
			return t
		}
	}
	return nil
}

// resolve maps an identifier (possibly dotted) used at line to its
// declaration site. Innermost scope wins: for-vars, then params/states
// of the enclosing decl, then top-level declarations, then
// `Session.cell` paths.
func resolve(doc *document, name string, line int) (dLine, dCol, dLen int, sig string, ok bool) {
	idx := doc.an.index
	head, rest, dotted := strings.Cut(name, ".")

	var enclosing *declSym
	for _, d := range idx.decls {
		if line >= d.start && line <= d.end {
			enclosing = d
			break
		}
	}

	if enclosing != nil {
		var best *localSym
		for i := range enclosing.locals {
			l := &enclosing.locals[i]
			if l.name != head || line < l.start || line > l.end {
				continue
			}
			// Narrower scope wins (a for-var shadows a state cell).
			if best == nil || (l.end-l.start) < (best.end-best.start) {
				best = l
			}
		}
		if best != nil {
			return best.line, best.col, best.length, sourceLine(doc, best.line), true
		}
	}

	for _, d := range idx.decls {
		if d.name != head {
			continue
		}
		// `Session.cell` / dotted package-ish ref: prefer the named cell
		// inside the target decl when it exists.
		if dotted {
			cell, _, _ := strings.Cut(rest, ".")
			for i := range d.locals {
				l := &d.locals[i]
				if l.name == cell {
					return l.line, l.col, l.length, sourceLine(doc, l.line), true
				}
			}
		}
		return d.line, d.col, d.length, d.detail, true
	}
	return 0, 0, 0, "", false
}

func definition(doc *document, pos Position) any {
	t := identAt(doc, pos)
	if t == nil {
		return nil
	}
	line, col, length, _, ok := resolve(doc, t.name, t.line)
	if !ok {
		return nil
	}
	return Location{URI: doc.uri, Range: protoRange(doc.lines, line, col, length)}
}

func hoverAt(doc *document, pos Position) any {
	t := identAt(doc, pos)
	if t == nil {
		return nil
	}
	_, _, _, sig, ok := resolve(doc, t.name, t.line)
	if !ok || sig == "" {
		return nil
	}
	rng := protoRange(doc.lines, t.line, t.col, t.length)
	return hover{
		Contents: markupContent{Kind: "markdown", Value: "```sigil\n" + sig + "\n```"},
		Range:    &rng,
	}
}
