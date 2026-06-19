package types

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/incantery/mako/core/ast"
	"github.com/incantery/mako/core/token"
)

// Checker performs type inference over a module.
type Checker struct {
	nextVar int
	level   int
	data    *dataEnv
	// ctorSchemes resolves constructor names from pattern position (where the
	// value env is not threaded through).
	ctorSchemes map[string]*Scheme
}

// dataEnv records ADTs so constructors can be typed and matches checked for
// exhaustiveness.
type dataEnv struct {
	// typeArity maps a type constructor name to its number of parameters.
	typeArity map[string]int
	// ctorsOf maps a type name to its constructor names (declaration order).
	ctorsOf map[string][]string
	// ctorType maps a constructor name to the type it constructs.
	ctorType map[string]string
}

// env is a lexical scope of value bindings.
type env struct {
	parent *env
	vars   map[string]*Scheme
}

func newEnv(parent *env) *env { return &env{parent: parent, vars: map[string]*Scheme{}} }

func (e *env) lookup(name string) (*Scheme, bool) {
	for s := e; s != nil; s = s.parent {
		if sc, ok := s.vars[name]; ok {
			return sc, true
		}
	}
	return nil, false
}

func (e *env) set(name string, s *Scheme) { e.vars[name] = s }

// Exports is the slice of a checked module that downstream modules can import:
// its public value/function schemes plus the data-environment contributions of
// its public types (so importers can name those types, build their constructors,
// and pattern-match them exhaustively).
type Exports struct {
	Values      map[string]*Scheme // pub let bindings: name -> generalized scheme
	TypeArity   map[string]int     // pub type name -> number of type parameters
	CtorsOf     map[string][]string
	CtorType    map[string]string
	CtorSchemes map[string]*Scheme
}

func newExports() *Exports {
	return &Exports{
		Values:      map[string]*Scheme{},
		TypeArity:   map[string]int{},
		CtorsOf:     map[string][]string{},
		CtorType:    map[string]string{},
		CtorSchemes: map[string]*Scheme{},
	}
}

func newChecker() *Checker {
	return &Checker{
		data: &dataEnv{
			typeArity: map[string]int{
				"Int": 0, "Float": 0, "String": 0, "Bool": 0, "Unit": 0,
				"List": 1, "Option": 1,
				// Built-in opaque types for the host/reactive intrinsics.
				"Cell": 1, "Node": 0, "Attr": 0, "Event": 0,
			},
			ctorsOf: map[string][]string{
				"Option": {"None", "Some"},
			},
			ctorType: map[string]string{"None": "Option", "Some": "Option"},
		},
		ctorSchemes: map[string]*Scheme{},
	}
}

// seed installs an upstream module's exports into this checker's data
// environment and root value scope, so the importing module can refer to them.
func (c *Checker) seed(root *env, deps *Exports) {
	maps.Copy(c.data.typeArity, deps.TypeArity)
	maps.Copy(c.data.ctorsOf, deps.CtorsOf)
	maps.Copy(c.data.ctorType, deps.CtorType)
	for n, sc := range deps.CtorSchemes {
		c.ctorSchemes[n] = sc
		root.set(n, sc)
	}
	for n, sc := range deps.Values {
		root.set(n, sc)
	}
}

// checkInto runs the two inference passes plus the effect-context check over m,
// seeding deps (may be nil) first. It returns the populated checker and root
// scope so callers can read inferred schemes back out.
func checkInto(m *ast.Module, deps *Exports) (*Checker, *env, error) {
	c := newChecker()
	root := newEnv(nil)
	c.installBuiltins(root)
	if deps != nil {
		c.seed(root, deps)
	}

	// First pass: register all type declarations so constructors are available
	// regardless of decl order.
	for _, d := range m.Decls {
		if td, ok := d.(*ast.TypeDecl); ok {
			if err := c.registerType(td, root); err != nil {
				return nil, nil, err
			}
		}
	}
	// Second pass: infer value bindings in order.
	for _, d := range m.Decls {
		if ld, ok := d.(*ast.LetDecl); ok {
			if err := c.inferDecl(ld, root); err != nil {
				return nil, nil, err
			}
		}
	}

	// Effect-context discipline: effect operations only inside effect { } blocks.
	if err := checkEffects(m); err != nil {
		return nil, nil, err
	}
	return c, root, nil
}

// Check type-checks a single, import-free module and returns the inferred scheme
// of every top-level binding (name -> printed type), or the first type error.
func Check(m *ast.Module) (map[string]string, error) {
	c, root, err := checkInto(m, nil)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, sc := range root.vars {
		if _, builtin := builtinNames[name]; builtin {
			continue
		}
		if _, isCtor := c.ctorSchemes[name]; isCtor {
			continue // constructors aren't user value bindings
		}
		if strings.HasPrefix(name, "__") {
			continue // intrinsics
		}
		out[name] = SchemeString(sc)
	}
	return out, nil
}

// CheckModule type-checks m with the exports of its already-checked dependencies
// seeded in, and returns m's own public exports. This is the cross-module entry
// point used by the loader; Check is the single-module shorthand.
func CheckModule(m *ast.Module, deps *Exports) (*Exports, error) {
	c, root, err := checkInto(m, deps)
	if err != nil {
		return nil, err
	}
	ex := newExports()
	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			if d.Pub && d.Name != "" {
				if sc, ok := root.vars[d.Name]; ok {
					ex.Values[d.Name] = sc
				}
			}
		case *ast.TypeDecl:
			if !d.Pub {
				continue
			}
			ex.TypeArity[d.Name] = c.data.typeArity[d.Name]
			if cs, ok := c.data.ctorsOf[d.Name]; ok {
				ex.CtorsOf[d.Name] = cs
			}
			for _, v := range d.Variants {
				ex.CtorType[v.Name] = d.Name
				if sc, ok := c.ctorSchemes[v.Name]; ok {
					ex.CtorSchemes[v.Name] = sc
				}
			}
		}
	}
	return ex, nil
}

var builtinNames = map[string]bool{"true": true, "false": true, "Some": true, "None": true}

func (c *Checker) installBuiltins(e *env) {
	e.set("true", monoScheme(tBool))
	e.set("false", monoScheme(tBool))
	// Option constructors: None : forall a. Option a ; Some : forall a. a -> Option a
	none := &Scheme{Vars: []int{0}, Body: tOption(&TGen{ID: 0})}
	some := &Scheme{Vars: []int{0}, Body: &TArrow{From: &TGen{ID: 0}, To: tOption(&TGen{ID: 0})}}
	e.set("None", none)
	e.set("Some", some)
	c.ctorSchemes["None"] = none
	c.ctorSchemes["Some"] = some

	c.installIntrinsics(e)
}

// installIntrinsics gives principal types to the host/reactive __intrinsics. A
// thunk is unit -> unit; the host builds a tree of Node from List Attr/Node.
func (c *Checker) installIntrinsics(e *env) {
	g := func(id int) *TGen { return &TGen{ID: id} }
	thunk := arrows(tUnit, tUnit)      // unit -> unit
	strThunk := arrows(tUnit, tString) // unit -> String
	poly := func(t Type) *Scheme { return &Scheme{Vars: []int{0}, Body: t} }
	mono := func(t Type) *Scheme { return &Scheme{Body: t} }

	// Reactive core.
	e.set("__cell", poly(arrows(g(0), tCell(g(0)))))       // a -> Cell a
	e.set("__get", poly(arrows(tCell(g(0)), g(0))))        // Cell a -> a   (pure read)
	e.set("__set", poly(arrows(tCell(g(0)), g(0), tUnit))) // Cell a -> a -> unit (effect)
	e.set("__effect", mono(arrows(thunk, tUnit)))          // (unit -> unit) -> unit (effect)

	// Host (DOM).
	e.set("__elem", mono(arrows(tString, tList(tAttr), tList(tNode), tNode)))
	e.set("__text", mono(arrows(strThunk, tNode)))              // reactive text node
	e.set("__attr", mono(arrows(tString, tString, tAttr)))      // static attribute
	e.set("__style", mono(arrows(tString, tString, tAttr)))     // CSS property:value (extracted to an atomic class when static; inline otherwise)
	e.set("__bindAttr", mono(arrows(tString, strThunk, tAttr))) // reactive attribute
	// Event handler: the listener receives the (opaque) Event and returns a
	// deferred effect. Event data is read back through total decoders (below).
	e.set("__on", mono(arrows(tString, arrows(tEvent, thunk), tAttr)))
	e.set("__eventValue", mono(arrows(tEvent, tString)))  // decode event.target.value ("" if absent)
	e.set("__mount", mono(arrows(tNode, tString, tUnit))) // mount under selector (effect)

	// Guarded boundary: fetch a URL, then invoke the continuation with the raw
	// (ok, body) primitives. stdlib (std/http) decodes these into a Result the
	// caller must match — untrusted network data never enters untyped.
	e.set("__fetch", mono(arrows(tString, arrows(tBool, tString, thunk), tUnit)))

	// Location boundary: read the path (pure), push history, listen for back/
	// forward. std/router builds path-driven routing on top of these.
	e.set("__path", mono(arrows(tUnit, tString)))     // window.location.pathname (pure read)
	e.set("__pushPath", mono(arrows(tString, tUnit))) // history.pushState (effect)
	e.set("__onPopState", mono(arrows(thunk, tUnit))) // register a popstate handler (effect)

	// Data primitives (pure). String split + total list access; std/string and
	// std/list build the rest in mako over these.
	e.set("__split", mono(arrows(tString, tString, tList(tString))))           // split by separator
	e.set("__listLen", poly(arrows(tList(g(0)), tInt)))                        // length
	e.set("__listAt", poly(arrows(tList(g(0)), tInt, tOption(g(0)))))          // total indexed access
	e.set("__listConcat", poly(arrows(tList(g(0)), tList(g(0)), tList(g(0))))) // concatenation (the list builder)

	// Reactive structure: keyed list and conditional. Both take a reader thunk
	// (auto-tracking, like __text) rather than a raw Cell, so stdlib readers and
	// derived expressions drive them directly.
	e.set("__each", poly(arrows(arrows(tUnit, tList(g(0))), arrows(g(0), tNode), tNode))) // (unit -> List a) -> (a -> Node) -> Node
	e.set("__when", mono(arrows(arrows(tUnit, tBool), arrows(tUnit, tNode), tNode)))      // (unit -> Bool) -> (unit -> Node) -> Node
}

// --- fresh variables, levels, generalization ---

func (c *Checker) fresh() *TVar {
	c.nextVar++
	return &TVar{V: &tvar{ID: c.nextVar, Level: c.level}}
}

func (c *Checker) enterLevel() { c.level++ }
func (c *Checker) exitLevel()  { c.level-- }

// generalize quantifies every unbound variable in t whose level is deeper than
// the current level, producing a polymorphic scheme.
func (c *Checker) generalize(t Type) *Scheme {
	var vars []int
	seen := map[int]int{} // tvar id -> gen id
	body := c.genWalk(t, &vars, seen)
	return &Scheme{Vars: vars, Body: body}
}

func (c *Checker) genWalk(t Type, vars *[]int, seen map[int]int) Type {
	t = prune(t)
	switch t := t.(type) {
	case *TVar:
		if t.V.Level > c.level {
			gid, ok := seen[t.V.ID]
			if !ok {
				gid = len(seen)
				seen[t.V.ID] = gid
				*vars = append(*vars, gid)
			}
			return &TGen{ID: gid}
		}
		return t
	case *TArrow:
		return &TArrow{From: c.genWalk(t.From, vars, seen), To: c.genWalk(t.To, vars, seen)}
	case *TTuple:
		es := make([]Type, len(t.Elems))
		for i, e := range t.Elems {
			es[i] = c.genWalk(e, vars, seen)
		}
		return &TTuple{Elems: es}
	case *TRecord:
		fs := make(map[string]Type, len(t.Fields))
		for k, v := range t.Fields {
			fs[k] = c.genWalk(v, vars, seen)
		}
		return &TRecord{Fields: fs}
	case *TCon:
		as := make([]Type, len(t.Args))
		for i, a := range t.Args {
			as[i] = c.genWalk(a, vars, seen)
		}
		return &TCon{Name: t.Name, Args: as}
	default:
		return t
	}
}

// instantiate replaces a scheme's quantified variables with fresh unification
// variables at the current level.
func (c *Checker) instantiate(s *Scheme) Type {
	if len(s.Vars) == 0 {
		return s.Body
	}
	sub := make(map[int]*TVar, len(s.Vars))
	for _, id := range s.Vars {
		sub[id] = c.fresh()
	}
	return instWalk(s.Body, sub)
}

func instWalk(t Type, sub map[int]*TVar) Type {
	switch t := t.(type) {
	case *TGen:
		if v, ok := sub[t.ID]; ok {
			return v
		}
		return t
	case *TArrow:
		return &TArrow{From: instWalk(t.From, sub), To: instWalk(t.To, sub)}
	case *TTuple:
		es := make([]Type, len(t.Elems))
		for i, e := range t.Elems {
			es[i] = instWalk(e, sub)
		}
		return &TTuple{Elems: es}
	case *TRecord:
		fs := make(map[string]Type, len(t.Fields))
		for k, v := range t.Fields {
			fs[k] = instWalk(v, sub)
		}
		return &TRecord{Fields: fs}
	case *TCon:
		as := make([]Type, len(t.Args))
		for i, a := range t.Args {
			as[i] = instWalk(a, sub)
		}
		return &TCon{Name: t.Name, Args: as}
	default:
		return t
	}
}

// --- unification ---

func (c *Checker) unify(a, b Type, pos ast.Pos) error {
	a, b = prune(a), prune(b)
	if a == b {
		return nil
	}
	if av, ok := a.(*TVar); ok {
		return c.bind(av, b, pos)
	}
	if bv, ok := b.(*TVar); ok {
		return c.bind(bv, a, pos)
	}
	switch at := a.(type) {
	case *TArrow:
		bt, ok := b.(*TArrow)
		if !ok {
			return mismatch(a, b, pos)
		}
		if err := c.unify(at.From, bt.From, pos); err != nil {
			return err
		}
		return c.unify(at.To, bt.To, pos)
	case *TTuple:
		bt, ok := b.(*TTuple)
		if !ok || len(at.Elems) != len(bt.Elems) {
			return mismatch(a, b, pos)
		}
		for i := range at.Elems {
			if err := c.unify(at.Elems[i], bt.Elems[i], pos); err != nil {
				return err
			}
		}
		return nil
	case *TRecord:
		bt, ok := b.(*TRecord)
		if !ok || len(at.Fields) != len(bt.Fields) {
			return mismatch(a, b, pos)
		}
		for k, av := range at.Fields {
			bv, ok := bt.Fields[k]
			if !ok {
				return mismatch(a, b, pos)
			}
			if err := c.unify(av, bv, pos); err != nil {
				return err
			}
		}
		return nil
	case *TCon:
		bt, ok := b.(*TCon)
		if !ok || at.Name != bt.Name || len(at.Args) != len(bt.Args) {
			return mismatch(a, b, pos)
		}
		for i := range at.Args {
			if err := c.unify(at.Args[i], bt.Args[i], pos); err != nil {
				return err
			}
		}
		return nil
	}
	return mismatch(a, b, pos)
}

func (c *Checker) bind(v *TVar, t Type, pos ast.Pos) error {
	if occurs(v.V, t) {
		return &Error{Line: pos.Line, Col: pos.Col, Msg: fmt.Sprintf("cannot construct infinite type %s = %s", String(v), String(t))}
	}
	adjustLevels(v.V.Level, t)
	v.V.Ref = t
	return nil
}

// occurs reports whether variable v appears in t (the occurs check), and is also
// where we would detect cycles.
func occurs(v *tvar, t Type) bool {
	t = prune(t)
	switch t := t.(type) {
	case *TVar:
		return t.V == v
	case *TArrow:
		return occurs(v, t.From) || occurs(v, t.To)
	case *TTuple:
		for _, e := range t.Elems {
			if occurs(v, e) {
				return true
			}
		}
	case *TRecord:
		for _, f := range t.Fields {
			if occurs(v, f) {
				return true
			}
		}
	case *TCon:
		for _, a := range t.Args {
			if occurs(v, a) {
				return true
			}
		}
	}
	return false
}

// adjustLevels lowers the level of every unbound variable in t to at most max,
// preserving the invariant that a variable's level bounds where it may be
// generalized.
func adjustLevels(max int, t Type) {
	t = prune(t)
	switch t := t.(type) {
	case *TVar:
		if t.V.Level > max {
			t.V.Level = max
		}
	case *TArrow:
		adjustLevels(max, t.From)
		adjustLevels(max, t.To)
	case *TTuple:
		for _, e := range t.Elems {
			adjustLevels(max, e)
		}
	case *TRecord:
		for _, f := range t.Fields {
			adjustLevels(max, f)
		}
	case *TCon:
		for _, a := range t.Args {
			adjustLevels(max, a)
		}
	}
}

func mismatch(a, b Type, pos ast.Pos) error {
	return &Error{Line: pos.Line, Col: pos.Col, Msg: fmt.Sprintf("type mismatch: %s vs %s", String(a), String(b))}
}

// --- type declarations ---

func (c *Checker) registerType(td *ast.TypeDecl, e *env) error {
	c.data.typeArity[td.Name] = len(td.Params)
	if td.Record != nil {
		return nil // record types declare no constructors
	}
	// Build the result type: TypeName applied to its parameters as TGen vars.
	paramIdx := map[string]int{}
	resultArgs := make([]Type, len(td.Params))
	for i, p := range td.Params {
		paramIdx[p] = i
		resultArgs[i] = &TGen{ID: i}
	}
	var result Type = &TCon{Name: td.Name, Args: resultArgs}
	if len(td.Params) == 0 {
		result = &TCon{Name: td.Name}
	}
	allVars := make([]int, len(td.Params))
	for i := range td.Params {
		allVars[i] = i
	}
	for _, v := range td.Variants {
		c.data.ctorsOf[td.Name] = append(c.data.ctorsOf[td.Name], v.Name)
		c.data.ctorType[v.Name] = td.Name
		var scheme *Scheme
		if v.Arg == nil {
			scheme = &Scheme{Vars: allVars, Body: result}
		} else {
			argT, err := c.fromTypeExpr(v.Arg, paramIdx)
			if err != nil {
				return err
			}
			scheme = &Scheme{Vars: allVars, Body: &TArrow{From: argT, To: result}}
		}
		e.set(v.Name, scheme)
		c.ctorSchemes[v.Name] = scheme
	}
	return nil
}

// fromTypeExpr converts a surface type into an internal type, mapping the named
// type parameters in scope to their TGen ids.
func (c *Checker) fromTypeExpr(te ast.TypeExpr, params map[string]int) (Type, error) {
	switch te := te.(type) {
	case *ast.TyVar:
		if id, ok := params[te.Name]; ok {
			return &TGen{ID: id}, nil
		}
		return nil, &Error{Msg: fmt.Sprintf("unbound type variable %q", te.Name)}
	case *ast.TyCon:
		args := make([]Type, len(te.Args))
		for i, a := range te.Args {
			t, err := c.fromTypeExpr(a, params)
			if err != nil {
				return nil, err
			}
			args[i] = t
		}
		if len(args) == 0 {
			return &TCon{Name: te.Name}, nil
		}
		return &TCon{Name: te.Name, Args: args}, nil
	case *ast.TyArrow:
		f, err := c.fromTypeExpr(te.From, params)
		if err != nil {
			return nil, err
		}
		t, err := c.fromTypeExpr(te.To, params)
		if err != nil {
			return nil, err
		}
		return &TArrow{From: f, To: t}, nil
	case *ast.TyTuple:
		if len(te.Elems) == 0 {
			return tUnit, nil
		}
		es := make([]Type, len(te.Elems))
		for i, el := range te.Elems {
			t, err := c.fromTypeExpr(el, params)
			if err != nil {
				return nil, err
			}
			es[i] = t
		}
		return &TTuple{Elems: es}, nil
	case *ast.TyRecord:
		fs := map[string]Type{}
		for _, f := range te.Fields {
			t, err := c.fromTypeExpr(f.Type, params)
			if err != nil {
				return nil, err
			}
			fs[f.Name] = t
		}
		return &TRecord{Fields: fs}, nil
	default:
		return nil, &Error{Msg: "unknown type expression"}
	}
}

// --- declaration inference ---

func (c *Checker) inferDecl(d *ast.LetDecl, e *env) error {
	if d.Name == "" {
		// Pattern binding: infer the body, then bind pattern names.
		c.enterLevel()
		t, err := c.infer(d.Body, e)
		if err != nil {
			return err
		}
		c.exitLevel()
		binds := map[string]Type{}
		if err := c.inferPattern(d.Pat, t, binds, d.Pos); err != nil {
			return err
		}
		for name, bt := range binds {
			e.set(name, c.generalize(bt))
		}
		return nil
	}

	c.enterLevel()
	tv := c.fresh()
	e.set(d.Name, monoScheme(tv)) // allow self-reference / recursion
	eff := effectiveBody(d.Params, d.Body)
	t, err := c.infer(eff, e)
	if err != nil {
		return err
	}
	if err := c.unify(tv, t, d.Pos); err != nil {
		return err
	}
	c.exitLevel()
	e.set(d.Name, c.generalize(t))
	return nil
}

// effectiveBody wraps a parameterized binding in a lambda so functions and values
// share one inference path.
func effectiveBody(params []ast.Param, body ast.Expr) ast.Expr {
	if len(params) == 0 {
		return body
	}
	return &ast.Lambda{Params: params, Body: body}
}

// --- expression inference ---

func (c *Checker) infer(e ast.Expr, env *env) (Type, error) {
	switch e := e.(type) {
	case *ast.IntLit:
		return tInt, nil
	case *ast.FloatLit:
		return tFloat, nil
	case *ast.StrLit:
		return tString, nil
	case *ast.Interp:
		for _, p := range e.Parts {
			if _, err := c.infer(p, env); err != nil {
				return nil, err
			}
		}
		return tString, nil
	case *ast.Unit:
		return tUnit, nil
	case *ast.Var:
		sc, ok := env.lookup(e.Name)
		if !ok {
			return nil, &Error{Line: e.Pos.Line, Col: e.Pos.Col, Msg: fmt.Sprintf("unbound variable %q", e.Name)}
		}
		return c.instantiate(sc), nil
	case *ast.Ctor:
		sc, ok := env.lookup(e.Name)
		if !ok {
			return nil, &Error{Line: e.Pos.Line, Col: e.Pos.Col, Msg: fmt.Sprintf("unknown constructor %q", e.Name)}
		}
		return c.instantiate(sc), nil
	case *ast.Tuple:
		es := make([]Type, len(e.Elems))
		for i, el := range e.Elems {
			t, err := c.infer(el, env)
			if err != nil {
				return nil, err
			}
			es[i] = t
		}
		return &TTuple{Elems: es}, nil
	case *ast.ListLit:
		elem := Type(c.fresh())
		for _, el := range e.Elems {
			t, err := c.infer(el, env)
			if err != nil {
				return nil, err
			}
			if err := c.unify(elem, t, e.Pos); err != nil {
				return nil, err
			}
		}
		return tList(elem), nil
	case *ast.RecordLit:
		fs := map[string]Type{}
		for _, f := range e.Fields {
			t, err := c.infer(f.Value, env)
			if err != nil {
				return nil, err
			}
			fs[f.Name] = t
		}
		return &TRecord{Fields: fs}, nil
	case *ast.Field:
		recv, err := c.infer(e.Recv, env)
		if err != nil {
			return nil, err
		}
		recv = prune(recv)
		rec, ok := recv.(*TRecord)
		if !ok {
			return nil, &Error{Line: e.Pos.Line, Col: e.Pos.Col, Msg: fmt.Sprintf("field access .%s on non-record type %s", e.Name, String(recv))}
		}
		ft, ok := rec.Fields[e.Name]
		if !ok {
			return nil, &Error{Line: e.Pos.Line, Col: e.Pos.Col, Msg: fmt.Sprintf("no field %q on %s", e.Name, String(recv))}
		}
		return ft, nil
	case *ast.Lambda:
		return c.inferLambda(e, env)
	case *ast.App:
		fnT, err := c.infer(e.Fn, env)
		if err != nil {
			return nil, err
		}
		argT, err := c.infer(e.Arg, env)
		if err != nil {
			return nil, err
		}
		res := Type(c.fresh())
		if err := c.unify(fnT, &TArrow{From: argT, To: res}, e.Pos); err != nil {
			return nil, err
		}
		return res, nil
	case *ast.Binop:
		return c.inferBinop(e, env)
	case *ast.Unop:
		x, err := c.infer(e.X, env)
		if err != nil {
			return nil, err
		}
		switch e.Op {
		case token.MINUS:
			return tInt, c.unify(x, tInt, e.Pos)
		case token.BANG:
			return tBool, c.unify(x, tBool, e.Pos)
		}
		return nil, &Error{Line: e.Pos.Line, Col: e.Pos.Col, Msg: "unknown unary operator"}
	case *ast.If:
		cond, err := c.infer(e.Cond, env)
		if err != nil {
			return nil, err
		}
		if err := c.unify(cond, tBool, e.Pos); err != nil {
			return nil, err
		}
		thn, err := c.infer(e.Then, env)
		if err != nil {
			return nil, err
		}
		els, err := c.infer(e.Else, env)
		if err != nil {
			return nil, err
		}
		if err := c.unify(thn, els, e.Pos); err != nil {
			return nil, err
		}
		return thn, nil
	case *ast.Match:
		return c.inferMatch(e, env)
	case *ast.Let:
		return c.inferLet(e, env)
	case *ast.Effect:
		// A deferred effect context; its value is a thunk unit -> unit.
		for _, s := range e.Stmts {
			if _, err := c.infer(s, env); err != nil {
				return nil, err
			}
		}
		return &TArrow{From: tUnit, To: tUnit}, nil
	default:
		return nil, &Error{Msg: fmt.Sprintf("cannot infer %T", e)}
	}
}

func (c *Checker) inferLambda(e *ast.Lambda, env *env) (Type, error) {
	body := newEnv(env)
	paramTypes := make([]Type, len(e.Params))
	for i, p := range e.Params {
		pt, err := c.bindParam(p, body)
		if err != nil {
			return nil, err
		}
		paramTypes[i] = pt
	}
	bodyT, err := c.infer(e.Body, body)
	if err != nil {
		return nil, err
	}
	t := bodyT
	for i := len(paramTypes) - 1; i >= 0; i-- {
		t = &TArrow{From: paramTypes[i], To: t}
	}
	return t, nil
}

// bindParam introduces a parameter's bindings into env and returns its type.
func (c *Checker) bindParam(p ast.Param, env *env) (Type, error) {
	switch p := p.(type) {
	case ast.VarParam:
		tv := c.fresh()
		env.set(p.Name, monoScheme(tv))
		return tv, nil
	case ast.WildParam:
		return c.fresh(), nil
	case ast.PatParam:
		tv := Type(c.fresh())
		binds := map[string]Type{}
		if err := c.inferPattern(p.Pat, tv, binds, ast.Pos{}); err != nil {
			return nil, err
		}
		for name, bt := range binds {
			env.set(name, monoScheme(bt))
		}
		return tv, nil
	case ast.RecordParam:
		fs := map[string]Type{}
		for _, f := range p.Fields {
			ft := Type(c.fresh())
			if f.Default != nil {
				dt, err := c.infer(f.Default, env)
				if err != nil {
					return nil, err
				}
				if err := c.unify(ft, dt, ast.Pos{}); err != nil {
					return nil, err
				}
			}
			fs[f.Name] = ft
			env.set(f.Name, monoScheme(ft))
		}
		return &TRecord{Fields: fs}, nil
	default:
		return nil, &Error{Msg: fmt.Sprintf("unknown param %T", p)}
	}
}

func (c *Checker) inferLet(e *ast.Let, env *env) (Type, error) {
	inner := newEnv(env)
	if e.Name == "" {
		c.enterLevel()
		t, err := c.infer(e.Body, inner)
		if err != nil {
			return nil, err
		}
		c.exitLevel()
		binds := map[string]Type{}
		if err := c.inferPattern(e.Pat, t, binds, e.Pos); err != nil {
			return nil, err
		}
		for name, bt := range binds {
			inner.set(name, c.generalize(bt))
		}
		return c.infer(e.In, inner)
	}
	c.enterLevel()
	tv := c.fresh()
	inner.set(e.Name, monoScheme(tv))
	t, err := c.infer(effectiveBody(e.Params, e.Body), inner)
	if err != nil {
		return nil, err
	}
	if err := c.unify(tv, t, e.Pos); err != nil {
		return nil, err
	}
	c.exitLevel()
	inner.set(e.Name, c.generalize(t))
	return c.infer(e.In, inner)
}

func (c *Checker) inferBinop(e *ast.Binop, env *env) (Type, error) {
	l, err := c.infer(e.L, env)
	if err != nil {
		return nil, err
	}
	r, err := c.infer(e.R, env)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT:
		if err := c.unify(l, tInt, e.Pos); err != nil {
			return nil, err
		}
		return tInt, c.unify(r, tInt, e.Pos)
	case token.LT, token.GT, token.LE, token.GE:
		if err := c.unify(l, tInt, e.Pos); err != nil {
			return nil, err
		}
		return tBool, c.unify(r, tInt, e.Pos)
	case token.EQEQ, token.NEQ:
		return tBool, c.unify(l, r, e.Pos)
	case token.ANDAND, token.OROR:
		if err := c.unify(l, tBool, e.Pos); err != nil {
			return nil, err
		}
		return tBool, c.unify(r, tBool, e.Pos)
	case token.CONCAT:
		if err := c.unify(l, tString, e.Pos); err != nil {
			return nil, err
		}
		return tString, c.unify(r, tString, e.Pos)
	case token.PIPEFWD:
		res := Type(c.fresh())
		return res, c.unify(r, &TArrow{From: l, To: res}, e.Pos)
	default:
		return nil, &Error{Line: e.Pos.Line, Col: e.Pos.Col, Msg: "unknown binary operator"}
	}
}

func (c *Checker) inferMatch(e *ast.Match, env *env) (Type, error) {
	scrut, err := c.infer(e.Scrut, env)
	if err != nil {
		return nil, err
	}
	result := Type(c.fresh())
	for _, arm := range e.Arms {
		armEnv := newEnv(env)
		binds := map[string]Type{}
		if err := c.inferPattern(arm.Pat, scrut, binds, e.Pos); err != nil {
			return nil, err
		}
		for name, bt := range binds {
			armEnv.set(name, monoScheme(bt))
		}
		if arm.Guard != nil {
			g, err := c.infer(arm.Guard, armEnv)
			if err != nil {
				return nil, err
			}
			if err := c.unify(g, tBool, e.Pos); err != nil {
				return nil, err
			}
		}
		bt, err := c.infer(arm.Body, armEnv)
		if err != nil {
			return nil, err
		}
		if err := c.unify(result, bt, e.Pos); err != nil {
			return nil, err
		}
	}
	if err := c.checkExhaustive(e, scrut); err != nil {
		return nil, err
	}
	return result, nil
}

// --- patterns ---

func (c *Checker) inferPattern(p ast.Pattern, expected Type, binds map[string]Type, pos ast.Pos) error {
	switch p := p.(type) {
	case ast.VarPat:
		if _, dup := binds[p.Name]; dup {
			return &Error{Line: pos.Line, Col: pos.Col, Msg: fmt.Sprintf("variable %q bound twice in pattern", p.Name)}
		}
		binds[p.Name] = expected
		return nil
	case ast.WildPat:
		return nil
	case ast.IntPat:
		return c.unify(expected, tInt, pos)
	case ast.FloatPat:
		return c.unify(expected, tFloat, pos)
	case ast.StrPat:
		return c.unify(expected, tString, pos)
	case ast.TuplePat:
		if len(p.Elems) == 0 {
			return c.unify(expected, tUnit, pos)
		}
		es := make([]Type, len(p.Elems))
		for i := range p.Elems {
			es[i] = c.fresh()
		}
		if err := c.unify(expected, &TTuple{Elems: es}, pos); err != nil {
			return err
		}
		for i, sub := range p.Elems {
			if err := c.inferPattern(sub, es[i], binds, pos); err != nil {
				return err
			}
		}
		return nil
	case ast.ListPat:
		elem := Type(c.fresh())
		if err := c.unify(expected, tList(elem), pos); err != nil {
			return err
		}
		for _, sub := range p.Elems {
			if err := c.inferPattern(sub, elem, binds, pos); err != nil {
				return err
			}
		}
		return nil
	case ast.RecordPat:
		fs := map[string]Type{}
		for _, f := range p.Fields {
			ft := Type(c.fresh())
			fs[f.Name] = ft
			sub := f.Pat
			if sub == nil {
				sub = ast.VarPat{Name: f.Name} // pun
			}
			if err := c.inferPattern(sub, ft, binds, pos); err != nil {
				return err
			}
		}
		return c.unify(expected, &TRecord{Fields: fs}, pos)
	case ast.CtorPat:
		sc, ok := lookupCtor(c, p.Name)
		if !ok {
			return &Error{Line: pos.Line, Col: pos.Col, Msg: fmt.Sprintf("unknown constructor %q", p.Name)}
		}
		ct := c.instantiate(sc)
		if arrow, isFn := prune(ct).(*TArrow); isFn {
			if len(p.Args) != 1 {
				return &Error{Line: pos.Line, Col: pos.Col, Msg: fmt.Sprintf("constructor %q takes 1 argument, got %d", p.Name, len(p.Args))}
			}
			if err := c.unify(expected, arrow.To, pos); err != nil {
				return err
			}
			return c.inferPattern(p.Args[0], arrow.From, binds, pos)
		}
		if len(p.Args) != 0 {
			return &Error{Line: pos.Line, Col: pos.Col, Msg: fmt.Sprintf("constructor %q takes no arguments, got %d", p.Name, len(p.Args))}
		}
		return c.unify(expected, ct, pos)
	default:
		return &Error{Line: pos.Line, Col: pos.Col, Msg: fmt.Sprintf("unknown pattern %T", p)}
	}
}

// ctorSchemes is set during Check so patterns can resolve constructors. We keep a
// reference on the checker via the root env, but constructors live in the value
// env; lookupCtor consults a side table populated in registerType + builtins.
func lookupCtor(c *Checker, name string) (*Scheme, bool) {
	sc, ok := c.ctorSchemes[name]
	return sc, ok
}

// --- exhaustiveness ---

func (c *Checker) checkExhaustive(e *ast.Match, scrut Type) error {
	// A top-level wildcard or variable arm catches everything.
	for _, arm := range e.Arms {
		switch arm.Pat.(type) {
		case ast.WildPat, ast.VarPat:
			if arm.Guard == nil {
				return nil
			}
		}
	}
	s := prune(scrut)
	con, ok := s.(*TCon)
	if !ok {
		return &Error{Line: e.Pos.Line, Col: e.Pos.Col, Msg: fmt.Sprintf("non-exhaustive match on %s: add a wildcard arm", String(s))}
	}
	all, isADT := c.data.ctorsOf[con.Name]
	if !isADT {
		return &Error{Line: e.Pos.Line, Col: e.Pos.Col, Msg: fmt.Sprintf("non-exhaustive match on %s: add a wildcard arm", con.Name)}
	}
	covered := map[string]bool{}
	for _, arm := range e.Arms {
		if cp, ok := arm.Pat.(ast.CtorPat); ok && arm.Guard == nil {
			covered[cp.Name] = true
		}
	}
	var missing []string
	for _, name := range all {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &Error{Line: e.Pos.Line, Col: e.Pos.Col, Msg: fmt.Sprintf("non-exhaustive match on %s: missing %s", con.Name, strings.Join(missing, ", "))}
	}
	return nil
}
