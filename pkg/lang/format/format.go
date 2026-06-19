// Package format pretty-prints a Sigil AST back to canonical source.
//
// "Canonical" here means: a fixed indent (2 spaces), kwargs sorted
// alphabetically, handlers sorted by event name, and a small re-sugaring
// pass that turns the desugared `cell = cell ± literal` assignment back
// into `cell += literal` / `cell -= literal` so authors who wrote the
// sugar see it preserved. Comments are not preserved at v0 — the parser
// drops them, and round-tripping comment positions is non-trivial.
//
// Output ends with a trailing newline, like gofmt.
package format

import (
	"sort"
	"strconv"
	"strings"

	"github.com/incantery/mako/pkg/lang/ast"
)

// Source returns the canonical formatted source for the given AST root.
func Source(root *ast.Node) string {
	var b strings.Builder
	emitNode(&b, root, 0)
	return b.String()
}

// emitNode dispatches by kind. Declarations, expressions, and
// invocations all have distinct output shapes.
func emitNode(b *strings.Builder, n *ast.Node, depth int) {
	switch n.Kind {
	case "__root__", "module":
		for _, c := range n.Children {
			emitNode(b, c, depth)
		}
		return
	case "__error__":
		// Error placeholders don't survive formatting.
		return
	case "code":
		emitCode(b, n, depth)
		return
	case "view", "component":
		emitDecl(b, n, depth)
		return
	case "test", "story":
		emitQuotedNameDecl(b, n, depth)
		return
	case "type":
		emitType(b, n, depth)
		return
	case "query", "command":
		emitOpDecl(b, n, depth)
		return
	case "state":
		emitState(b, n, depth)
		return
	case "splice":
		emitSplice(b, n, depth)
		return
	case "theme":
		emitTheme(b, n, depth)
		return
	case "tone_binding":
		emitToneBinding(b, n, depth)
		return
	case "text_binding":
		emitTextBinding(b, n, depth)
		return
	case "fonts":
		emitFontsDecl(b, n, depth)
		return
	case "field_decl":
		emitFieldDecl(b, n, depth)
		return
	case "variant_decl":
		emitVariantDecl(b, n, depth)
		return
	}
	emitInvocation(b, n, depth)
}

// emitVariantDecl writes one `| name` body line of a sum-type decl.
func emitVariantDecl(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	b.WriteString("| ")
	if len(n.Args) > 0 {
		b.WriteString(n.Args[0].String)
	}
	b.WriteString("\n")
}

// emitFieldDecl writes a structured-list field line:
//
//	<name> : <Type>            (no default)
//	<name> : <Type> = <expr>   (with default)
func emitFieldDecl(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	if len(n.Args) < 2 {
		return
	}
	b.WriteString(n.Args[0].String)
	b.WriteString(" : ")
	emitTypeRefValue(b, n.Args[1])
	if len(n.Children) > 0 {
		b.WriteString(" = ")
		emitExpr(b, n.Children[0])
	}
	b.WriteString("\n")
}

// emitTypeRefValue writes a type-reference Value as it appears in
// source: `Foo`, `List<Bar>`, `Int?`, `List<Pokemon>?`, …
// Mirrors the lowerer's recursive TypeRef shape.
func emitTypeRefValue(b *strings.Builder, v ast.Value) {
	b.WriteString(v.String)
	if len(v.GenericArgs) > 0 {
		b.WriteString("<")
		for i, a := range v.GenericArgs {
			if i > 0 {
				b.WriteString(", ")
			}
			emitTypeRefValue(b, a)
		}
		b.WriteString(">")
	}
	if v.Optional {
		b.WriteString("?")
	}
}

// emitTheme writes `theme <name> [extends <base>] =` plus the indented
// tone bindings (handled per-child by emitToneBinding via the normal
// emitNode dispatch).
func emitTheme(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	b.WriteString("theme")
	if len(n.Args) > 0 {
		b.WriteString(" ")
		b.WriteString(n.Args[0].String)
	}
	if extendsVal, ok := n.Kwargs["extends"]; ok {
		b.WriteString(" extends ")
		b.WriteString(extendsVal.String)
	}
	b.WriteString(" =\n")
	for _, c := range n.Children {
		emitNode(b, c, depth+1)
	}
}

// emitToneBinding writes one theme color line: the paired form
// `<tone> = "#bg" on "#fg"`, or the single-color form
// `outline/muted = "#hex"` (2 Args).
func emitToneBinding(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	if len(n.Args) < 2 {
		return
	}
	b.WriteString(n.Args[0].String)
	b.WriteString(" = ")
	emitValue(b, n.Args[1])
	if len(n.Args) >= 3 {
		b.WriteString(" on ")
		emitValue(b, n.Args[2])
	}
	b.WriteString("\n")
}

// emitTextBinding writes one `text <token> = …` theme body line. The
// value sequence (family string / `italic` / size / weight) round-trips
// in source order.
func emitTextBinding(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	if len(n.Args) < 2 {
		return
	}
	b.WriteString("text ")
	b.WriteString(n.Args[0].String)
	b.WriteString(" =")
	for _, v := range n.Args[1:] {
		b.WriteString(" ")
		emitValue(b, v)
	}
	b.WriteString("\n")
}

// emitFontsDecl writes a `fonts <provider> = "A" "B"` declaration line.
func emitFontsDecl(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	if len(n.Args) < 2 {
		return
	}
	b.WriteString("fonts ")
	b.WriteString(n.Args[0].String)
	b.WriteString(" =")
	for _, v := range n.Args[1:] {
		b.WriteString(" ")
		emitValue(b, v)
	}
	b.WriteString("\n")
}

// emitSplice writes a `*name` body line. Used only inside component bodies.
func emitSplice(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	b.WriteString("*")
	if len(n.Args) > 0 {
		b.WriteString(n.Args[0].String)
	}
	b.WriteString("\n")
}

// emitCode re-emits a `code` block: the keyword on its own line, then the
// verbatim body indented one level deeper. The body string is what the
// parser captured (common indent already stripped, blank lines preserved),
// so formatting is just re-indentation — the content is never reflowed.
func emitCode(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	b.WriteString("code\n")
	if len(n.Args) == 0 {
		return
	}
	for _, line := range strings.Split(n.Args[0].String, "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		indent(b, depth+1)
		b.WriteString(line)
		b.WriteString("\n")
	}
}

func indent(b *strings.Builder, depth int) {
	for i := 0; i < depth; i++ {
		b.WriteString("  ")
	}
}

// emitDecl handles `view Name (-> arg)* =` plus the indented body.
// Variadic params (set on ast.Value) emit with a leading `*`.
func emitDecl(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	b.WriteString(n.Kind)
	if len(n.Args) > 0 {
		b.WriteString(" ")
		b.WriteString(n.Args[0].String)
		for i := 1; i < len(n.Args); i++ {
			b.WriteString(" -> ")
			if n.Args[i].Variadic {
				b.WriteString("*")
			}
			b.WriteString(n.Args[i].String)
		}
	}
	b.WriteString(" =\n")
	for _, c := range n.Children {
		emitNode(b, c, depth+1)
	}
}

// emitOpDecl writes a `query` or `command` signature on a single
// line: `<kind> Name (-> arg : Type)* = ReturnType`. Inputs come
// from n.Children (typed field decls in source order); the return
// type lives at n.Args[1].
func emitOpDecl(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	b.WriteString(n.Kind)
	if len(n.Args) > 0 {
		b.WriteString(" ")
		b.WriteString(n.Args[0].String)
	}
	for _, c := range n.Children {
		if c == nil || c.Kind != "field_decl" || len(c.Args) < 2 {
			continue
		}
		b.WriteString(" -> ")
		b.WriteString(c.Args[0].String)
		b.WriteString(" : ")
		emitTypeRefValue(b, c.Args[1])
	}
	b.WriteString(" = ")
	if len(n.Args) >= 2 {
		emitTypeRefValue(b, n.Args[1])
	}
	b.WriteString("\n")
}

// emitType writes `type <Name> =` plus indented field decls. Field
// decls render via the existing emitFieldDecl pathway since the AST
// shape is identical to a structured-list state's field decls.
func emitType(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	b.WriteString("type")
	if len(n.Args) > 0 {
		b.WriteString(" ")
		b.WriteString(n.Args[0].String)
	}
	b.WriteString(" =\n")
	for _, c := range n.Children {
		emitNode(b, c, depth+1)
	}
}

// emitQuotedNameDecl writes `test "<name>" =` or `story "<name>" =`
// plus the indented body. Test steps and story component invocations
// both render via the standard emitInvocation path, so no special-case
// formatting for either body shape.
func emitQuotedNameDecl(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	b.WriteString(n.Kind)
	if len(n.Args) > 0 {
		b.WriteString(" ")
		emitValue(b, n.Args[0])
	}
	b.WriteString(" =\n")
	for _, c := range n.Children {
		emitNode(b, c, depth+1)
	}
}

// emitState handles `state name (: T)? (= expr)?` plus any indented
// field decls for structured-list states. The type annotation and
// initializer are both optional; the parser allows omitting `=` when
// a type is provided.
func emitState(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	b.WriteString("state ")
	if len(n.Args) > 0 {
		b.WriteString(n.Args[0].String)
	}
	if len(n.Args) >= 2 {
		b.WriteString(" : ")
		emitTypeRefValue(b, n.Args[1])
	}
	if len(n.Children) > 0 {
		b.WriteString(" = ")
		emitExpr(b, n.Children[0])
	}
	b.WriteString("\n")
	// Indented field decls follow when the state is a structured list.
	if len(n.Children) > 0 {
		for _, c := range n.Children[1:] {
			emitNode(b, c, depth+1)
		}
	}
}

// emitInvocation handles ordinary component invocations: positional args,
// then sorted kwargs, then sorted handlers, then indented children.
func emitInvocation(b *strings.Builder, n *ast.Node, depth int) {
	indent(b, depth)
	b.WriteString(n.Kind)
	for _, a := range n.Args {
		b.WriteString(" ")
		emitValue(b, a)
	}
	if len(n.Kwargs) > 0 {
		keys := make([]string, 0, len(n.Kwargs))
		for k := range n.Kwargs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(" ")
			b.WriteString(k)
			b.WriteString("=")
			emitValue(b, n.Kwargs[k])
		}
	}
	if len(n.Handlers) > 0 {
		evts := make([]string, 0, len(n.Handlers))
		for k := range n.Handlers {
			evts = append(evts, k)
		}
		sort.Strings(evts)
		for _, ev := range evts {
			b.WriteString(" on ")
			b.WriteString(ev)
			b.WriteString(" { ")
			emitStmt(b, n.Handlers[ev])
			b.WriteString(" }")
		}
	}
	b.WriteString("\n")
	for _, c := range n.Children {
		emitNode(b, c, depth+1)
	}
}

// emitExpr emits an expression-shaped AST node (lit / ref / binop / not /
// list_lit).
func emitExpr(b *strings.Builder, e *ast.Node) {
	switch {
	case e.Kind == "lit":
		if len(e.Args) > 0 {
			emitValue(b, e.Args[0])
		}
	case e.Kind == "ref":
		if len(e.Args) > 0 {
			b.WriteString(e.Args[0].String)
		}
	case e.Kind == "not":
		b.WriteString("!")
		if len(e.Children) > 0 {
			emitExpr(b, e.Children[0])
		}
	case e.Kind == "list_lit":
		b.WriteString("[")
		for i, c := range e.Children {
			if i > 0 {
				b.WriteString(", ")
			}
			emitExpr(b, c)
		}
		b.WriteString("]")
	case strings.HasPrefix(e.Kind, "binop:") && len(e.Children) == 2:
		emitExpr(b, e.Children[0])
		b.WriteString(" ")
		b.WriteString(strings.TrimPrefix(e.Kind, "binop:"))
		b.WriteString(" ")
		emitExpr(b, e.Children[1])
	}
}

// emitStmt emits an assignment or method-call statement. For assignments
// where the parser desugared `lhs += rhs` to `lhs = lhs + rhs`, we
// re-sugar back so the printed form matches the typical author input.
func emitStmt(b *strings.Builder, s *ast.Node) {
	if s.Kind == "method_call" && len(s.Args) >= 2 {
		b.WriteString(s.Args[0].String)
		b.WriteString(".")
		b.WriteString(s.Args[1].String)
		b.WriteString("(")
		for i, c := range s.Children {
			if i > 0 {
				b.WriteString(", ")
			}
			emitExpr(b, c)
		}
		b.WriteString(")")
		return
	}
	if s.Kind != "assign" || len(s.Args) == 0 || len(s.Children) == 0 {
		return
	}
	lhs := s.Args[0].String
	rhs := s.Children[0]

	// Re-sugar `lhs = lhs ± lit` → `lhs += lit` / `lhs -= lit`.
	if strings.HasPrefix(rhs.Kind, "binop:") && len(rhs.Children) == 2 {
		op := strings.TrimPrefix(rhs.Kind, "binop:")
		left := rhs.Children[0]
		if (op == "+" || op == "-") &&
			left.Kind == "ref" && len(left.Args) > 0 &&
			left.Args[0].String == lhs {
			b.WriteString(lhs)
			b.WriteString(" ")
			b.WriteString(op)
			b.WriteString("= ")
			emitExpr(b, rhs.Children[1])
			return
		}
	}

	b.WriteString(lhs)
	b.WriteString(" = ")
	emitExpr(b, rhs)
}

// emitValue prints one ast.Value: strings get re-quoted, ints render as
// base 10, idents emit as-is.
func emitValue(b *strings.Builder, v ast.Value) {
	switch v.Kind {
	case ast.ValueString:
		b.WriteString(strconv.Quote(v.String))
	case ast.ValueInt:
		b.WriteString(strconv.FormatInt(v.Int, 10))
	case ast.ValueIdent:
		b.WriteString(v.String)
	}
}
