// Package ast defines the Sigil language's abstract syntax tree.
//
// At v0 this is intentionally minimal: a Node is a named primitive (e.g.
// "view", "card", "title") with a list of Value args and a list of child
// Nodes. No expressions, no types, no state — those grow in here as the
// language does. The Sigil language work is staged so the AST can be
// inspected and lowered before the parser learns to recognize each new
// feature.
package ast

// Pos is a 1-indexed source position used in error messages.
type Pos struct {
	Line int
	Col  int
}

// Node is one node in the source tree.
//
// Args and Kwargs together capture the on-the-same-line arguments. A bare
// ident, string, or int goes into Args (positional). A `name=value` pair
// goes into Kwargs. Bare idents in Args double as "flags" — the lowering
// stage decides per kind which idents are flags vs. positional values.
//
// Handlers capture `on <event> { <stmt> }` suffixes attached to a
// component invocation. The value is itself a Node — at S0 always an
// "assign" statement with one expression child — so the same AST shape
// composes through into the expression grammar.
//
// Expression-shaped Nodes use these Kinds: "lit" (one Value in Args),
// "ref" (one ValueIdent in Args), "binop:+" / "binop:-" (two Children).
// "assign" is the only statement kind in S0 (Args[0] = LHS ident,
// Children[0] = RHS expression). "state" is a top-of-view declaration
// (Args[0] = name ident, Children[0] = initial expression).
type Node struct {
	Kind     string           // "view", "card", "title", "text", "stack", "state", "assign", "lit", "ref", "binop:+", ...
	Args     []Value          // positional / flag args after the kind
	Kwargs   map[string]Value // name=value keyword args
	Handlers map[string]*Node // event name → handler block (a statement node)
	Children []*Node          // indented children or sub-expressions
	Pos      Pos              // source position for diagnostics
}

// ValueKind tags a literal argument value. Kept as a string so future kinds
// (numbers, identifiers, cell refs) extend the set without breaking pattern
// matching.
type ValueKind string

const (
	ValueString ValueKind = "string"
	ValueIdent  ValueKind = "ident"
	ValueInt    ValueKind = "int"
)

// Value is a literal arg. Only one of the typed fields is meaningful per
// instance — read it according to Kind.
//
// Variadic is set only on `*name` parameters in `component` declarations
// (e.g. `component card -> heading -> *body =`). It captures the leading
// `*` so lowering can recognize the variadic param and bind call-site
// children to it. It is meaningless elsewhere.
//
// Optional marks a type-reference Value with a trailing `?` (`Int?`,
// `Pokemon?`). Only meaningful in positions that name a type — type
// decl fields, future service method args / return types, future
// generic positions. Ignored on cell refs, literals, kwargs.
//
// GenericArgs carries `<Inner>` type parameters parsed in postfix
// position on a type ident (`List<Pokemon>`, `Map<String, Int>` if
// we add multi-arg later). v0 recognizes only `List<T>`; the
// parser produces the AST shape for any `<...>` and the lowerer
// validates the head name.
type Value struct {
	Kind        ValueKind
	String      string
	Int         int64
	Pos         Pos
	Variadic    bool
	Optional    bool
	GenericArgs []Value
}
