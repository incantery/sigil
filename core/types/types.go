// Package types is the mako core type checker: Hindley-Milner inference with
// level-based let-generalization (Algorithm W), algebraic data types, and match
// exhaustiveness checking.
//
// Types are represented with destructive unification: a TVar holds a *tvar cell
// that is either unbound (with a generalization level) or bound to another type.
package types

import (
	"fmt"
	"sort"
	"strings"
)

// Type is an inferred type.
type Type interface{ isType() }

type (
	// TCon is a named type constructor with zero or more arguments: Int, Bool,
	// String, List a, Option a, and user-declared ADTs.
	TCon struct {
		Name string
		Args []Type
	}
	// TArrow is a function type from -> to.
	TArrow struct{ From, To Type }
	// TTuple is a tuple type; the empty tuple is unit.
	TTuple struct{ Elems []Type }
	// TRecord is a closed structural record type.
	TRecord struct{ Fields map[string]Type }
	// TVar is a unification variable.
	TVar struct{ V *tvar }
	// TGen is a quantified (generalized) variable inside a Scheme.
	TGen struct{ ID int }
)

func (*TCon) isType()    {}
func (*TArrow) isType()  {}
func (*TTuple) isType()  {}
func (*TRecord) isType() {}
func (*TVar) isType()    {}
func (*TGen) isType()    {}

// tvar is a unification cell. When Ref != nil the variable is bound; otherwise it
// is unbound at generalization level Level.
type tvar struct {
	ID    int
	Level int
	Ref   Type
}

// Scheme is a (possibly) polymorphic type: forall Vars. Body, where quantified
// variables appear in Body as *TGen.
type Scheme struct {
	Vars []int
	Body Type
}

// Convenience constructors for built-in types.
var (
	tInt    = &TCon{Name: "Int"}
	tFloat  = &TCon{Name: "Float"}
	tString = &TCon{Name: "String"}
	tBool   = &TCon{Name: "Bool"}
	tUnit   = &TCon{Name: "Unit"}
)

func tList(elem Type) Type   { return &TCon{Name: "List", Args: []Type{elem}} }
func tOption(elem Type) Type { return &TCon{Name: "Option", Args: []Type{elem}} }
func tCell(elem Type) Type   { return &TCon{Name: "Cell", Args: []Type{elem}} }

var (
	tNode  = &TCon{Name: "Node"}
	tAttr  = &TCon{Name: "Attr"}
	tEvent = &TCon{Name: "Event"}
)

// arrows builds a right-associated function type a -> b -> ... -> r.
func arrows(ts ...Type) Type {
	t := ts[len(ts)-1]
	for i := len(ts) - 2; i >= 0; i-- {
		t = &TArrow{From: ts[i], To: t}
	}
	return t
}

// monoScheme wraps a type with no quantified variables.
func monoScheme(t Type) *Scheme { return &Scheme{Body: t} }

// prune follows bound variables, path-compressing as it goes, and returns the
// representative type.
func prune(t Type) Type {
	if v, ok := t.(*TVar); ok && v.V.Ref != nil {
		r := prune(v.V.Ref)
		v.V.Ref = r
		return r
	}
	return t
}

// --- type errors ---

// Error is a type error with source position.
type Error struct {
	Line, Col int
	Msg       string
}

func (e *Error) Error() string {
	if e.Line == 0 {
		return e.Msg
	}
	return fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Msg)
}

// --- pretty printing ---

// String renders a type with stable variable names for diagnostics/tests.
func String(t Type) string { return (&namer{names: map[int]string{}}).typ(t) }

// SchemeString renders a scheme's body, naming quantified vars a, b, c, ...
func SchemeString(s *Scheme) string {
	n := &namer{names: map[int]string{}}
	for _, id := range s.Vars {
		n.gen(id)
	}
	return n.typ(s.Body)
}

type namer struct {
	names map[int]string
	next  int
}

func (n *namer) letter() string {
	s := string(rune('a' + n.next%26))
	if n.next >= 26 {
		s += fmt.Sprint(n.next / 26)
	}
	n.next++
	return s
}

func (n *namer) gen(id int) string {
	if s, ok := n.names[id]; ok {
		return s
	}
	s := n.letter()
	n.names[id] = s
	return s
}

func (n *namer) typ(t Type) string {
	t = prune(t)
	switch t := t.(type) {
	case *TCon:
		if len(t.Args) == 0 {
			return t.Name
		}
		parts := []string{t.Name}
		for _, a := range t.Args {
			parts = append(parts, n.atom(a))
		}
		return strings.Join(parts, " ")
	case *TArrow:
		return n.atom(t.From) + " -> " + n.typ(t.To)
	case *TTuple:
		if len(t.Elems) == 0 {
			return "Unit"
		}
		var ps []string
		for _, e := range t.Elems {
			ps = append(ps, n.typ(e))
		}
		return "(" + strings.Join(ps, ", ") + ")"
	case *TRecord:
		keys := make([]string, 0, len(t.Fields))
		for k := range t.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var ps []string
		for _, k := range keys {
			ps = append(ps, k+": "+n.typ(t.Fields[k]))
		}
		return "{" + strings.Join(ps, ", ") + "}"
	case *TGen:
		return n.gen(t.ID)
	case *TVar:
		return "_" + n.gen(-t.V.ID-1) // unbound: distinct namespace
	default:
		return "?"
	}
}

// atom prints a type, parenthesizing arrows/applications where needed.
func (n *namer) atom(t Type) string {
	t = prune(t)
	switch tt := t.(type) {
	case *TArrow:
		return "(" + n.typ(t) + ")"
	case *TCon:
		if len(tt.Args) > 0 {
			return "(" + n.typ(t) + ")"
		}
	}
	return n.typ(t)
}
