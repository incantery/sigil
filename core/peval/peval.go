// Package peval is a partial evaluator (const-folder) for mako core
// expressions. Given a set of top-level definitions, it reduces an expression as
// far as it can — inlining calls to known pure functions, looking through value
// bindings — and returns the residual expression. It never fails: anything it
// cannot reduce is returned structurally unchanged.
//
// It is best-effort by design. Its first consumer is compile-time CSS extraction
// (reducing `p s4` to `__style "padding" "1rem"` so the emitter can hoist it to
// an atomic class), where a missed reduction simply falls back to a runtime
// value. The same machinery generalizes to constant folding and dead-code
// elimination later.
package peval

import (
	"maps"

	"github.com/incantery/mako/core/ast"
)

// Env holds the top-level value/function definitions available for inlining,
// gathered across every linked module.
type Env struct {
	defs map[string]*ast.LetDecl
}

// NewEnv returns an empty environment.
func NewEnv() *Env { return &Env{defs: map[string]*ast.LetDecl{}} }

// Add registers a top-level let binding (named, with or without parameters).
// Destructuring binds (Name=="") are ignored — they have no single name to call.
func (env *Env) Add(d *ast.LetDecl) {
	if d.Name != "" {
		env.defs[d.Name] = d
	}
}

// AddModule registers every top-level let declaration in a module.
func (env *Env) AddModule(m *ast.Module) {
	for _, d := range m.Decls {
		if ld, ok := d.(*ast.LetDecl); ok {
			env.Add(ld)
		}
	}
}

const maxDepth = 256 // runaway-inlining backstop

// Eval partially evaluates e and returns the residual expression.
func (env *Env) Eval(e ast.Expr) ast.Expr { return env.eval(e, 0) }

func (env *Env) eval(e ast.Expr, depth int) ast.Expr {
	if depth > maxDepth {
		return e
	}
	switch e := e.(type) {
	case *ast.Var:
		// Inline a zero-argument value binding. Recursive bindings are never
		// inlined — they don't usefully const-fold and would expand unbounded.
		if d, ok := env.defs[e.Name]; ok && !d.Rec && len(d.Params) == 0 {
			return env.eval(d.Body, depth+1)
		}
		return e
	case *ast.App:
		return env.evalApp(e, depth)
	case *ast.Match:
		return env.evalMatch(e, depth)
	default:
		// Literals, constructors, and forms peval doesn't reduce yet are returned
		// as-is (the caller treats an unreduced result as "not foldable").
		return e
	}
}

// evalMatch reduces a match when the scrutinee folds to a known constructor or
// literal and the selected arm has no guard; otherwise it returns the match
// unreduced (the caller falls back to the runtime form).
func (env *Env) evalMatch(m *ast.Match, depth int) ast.Expr {
	scrut := env.eval(m.Scrut, depth+1)
	for _, arm := range m.Arms {
		binds, matched, certain := matchPat(arm.Pat, scrut)
		if !certain {
			return m // can't decide this arm statically — give up
		}
		if !matched {
			continue
		}
		if arm.Guard != nil {
			return m // guard outcome is unknown at compile time
		}
		body := arm.Body
		for name, repl := range binds {
			body = subst(body, name, repl)
		}
		return env.eval(body, depth+1)
	}
	return m // no arm matched statically (e.g. non-exhaustive); leave as-is
}

// matchPat tests a pattern against an already-reduced scrutinee. certain reports
// whether the outcome is known at compile time; when false the caller must not
// reduce the match.
func matchPat(p ast.Pattern, scrut ast.Expr) (binds map[string]ast.Expr, matched, certain bool) {
	switch p := p.(type) {
	case ast.WildPat:
		return nil, true, true
	case ast.VarPat:
		return map[string]ast.Expr{p.Name: scrut}, true, true
	case ast.CtorPat:
		name, args, ok := asCtor(scrut)
		if !ok {
			return nil, false, false // scrutinee is not a known constructor value
		}
		if name != p.Name || len(args) != len(p.Args) {
			return nil, false, true // a different (or wrong-arity) constructor
		}
		out := map[string]ast.Expr{}
		for i, sub := range p.Args {
			b, m, c := matchPat(sub, args[i])
			if !c {
				return nil, false, false
			}
			if !m {
				return nil, false, true
			}
			maps.Copy(out, b)
		}
		return out, true, true
	case ast.StrPat:
		if s, ok := scrut.(*ast.StrLit); ok {
			return nil, s.Value == p.Value, true
		}
		return nil, false, false
	case ast.IntPat:
		if i, ok := scrut.(*ast.IntLit); ok {
			return nil, i.Raw == p.Raw, true
		}
		return nil, false, false
	default:
		return nil, false, false // tuple/list/record patterns not folded yet
	}
}

// asCtor decomposes a reduced value into a constructor name and its arguments:
// a nullary Ctor, or an App spine headed by a Ctor.
func asCtor(e ast.Expr) (name string, args []ast.Expr, ok bool) {
	switch e := e.(type) {
	case *ast.Ctor:
		return e.Name, nil, true
	case *ast.App:
		head, as := flattenApp(e)
		if c, isCtor := head.(*ast.Ctor); isCtor {
			return c.Name, as, true
		}
	}
	return "", nil, false
}

func (env *Env) evalApp(e *ast.App, depth int) ast.Expr {
	head, args := flattenApp(e)
	for i := range args {
		args[i] = env.eval(args[i], depth+1)
	}

	switch h := head.(type) {
	case *ast.Var:
		if d, ok := env.defs[h.Name]; ok && !d.Rec && len(d.Params) > 0 && len(args) >= len(d.Params) {
			if body, ok := substParams(d.Params, args, d.Body); ok {
				reduced := env.eval(body, depth+1)
				return env.eval(rebuildApp(reduced, args[len(d.Params):]), depth+1)
			}
		}
	case *ast.Lambda:
		if len(args) >= len(h.Params) {
			if body, ok := substParams(h.Params, args, h.Body); ok {
				reduced := env.eval(body, depth+1)
				return env.eval(rebuildApp(reduced, args[len(h.Params):]), depth+1)
			}
		}
	}
	return rebuildApp(head, args)
}

// flattenApp turns App(App(f, a), b) into (f, [a, b]).
func flattenApp(e *ast.App) (ast.Expr, []ast.Expr) {
	var args []ast.Expr
	cur := ast.Expr(e)
	for {
		app, ok := cur.(*ast.App)
		if !ok {
			break
		}
		args = append([]ast.Expr{app.Arg}, args...)
		cur = app.Fn
	}
	return cur, args
}

// rebuildApp re-applies args to head left-to-right.
func rebuildApp(head ast.Expr, args []ast.Expr) ast.Expr {
	out := head
	for _, a := range args {
		out = &ast.App{Fn: out, Arg: a}
	}
	return out
}

// substParams substitutes the first len(params) args into body. It only handles
// simple VarParam bindings; any other parameter form aborts (ok=false) so the
// caller leaves the application unreduced rather than risk an unsound rewrite.
func substParams(params []ast.Param, args []ast.Expr, body ast.Expr) (ast.Expr, bool) {
	out := body
	for i, p := range params {
		vp, ok := p.(ast.VarParam)
		if !ok {
			return nil, false
		}
		out = subst(out, vp.Name, args[i])
	}
	return out, true
}

// subst replaces every free occurrence of Var{name} in e with repl, respecting
// shadowing introduced by lambdas and let bindings.
func subst(e ast.Expr, name string, repl ast.Expr) ast.Expr {
	switch e := e.(type) {
	case *ast.Var:
		if e.Name == name {
			return repl
		}
		return e
	case *ast.App:
		return &ast.App{Pos: e.Pos, Fn: subst(e.Fn, name, repl), Arg: subst(e.Arg, name, repl)}
	case *ast.Binop:
		return &ast.Binop{Pos: e.Pos, Op: e.Op, L: subst(e.L, name, repl), R: subst(e.R, name, repl)}
	case *ast.Unop:
		return &ast.Unop{Pos: e.Pos, Op: e.Op, X: subst(e.X, name, repl)}
	case *ast.ListLit:
		return &ast.ListLit{Pos: e.Pos, Elems: substAll(e.Elems, name, repl)}
	case *ast.Tuple:
		return &ast.Tuple{Pos: e.Pos, Elems: substAll(e.Elems, name, repl)}
	case *ast.Interp:
		return &ast.Interp{Pos: e.Pos, Parts: substAll(e.Parts, name, repl)}
	case *ast.Field:
		return &ast.Field{Pos: e.Pos, Recv: subst(e.Recv, name, repl), Name: e.Name}
	case *ast.If:
		return &ast.If{Pos: e.Pos, Cond: subst(e.Cond, name, repl), Then: subst(e.Then, name, repl), Else: subst(e.Else, name, repl)}
	case *ast.RecordLit:
		fs := make([]*ast.FieldVal, len(e.Fields))
		for i, f := range e.Fields {
			fs[i] = &ast.FieldVal{Name: f.Name, Value: subst(f.Value, name, repl)}
		}
		return &ast.RecordLit{Pos: e.Pos, Fields: fs}
	case *ast.Effect:
		return &ast.Effect{Pos: e.Pos, Stmts: substAll(e.Stmts, name, repl)}
	case *ast.Lambda:
		if bindsName(e.Params, name) {
			return e // shadowed
		}
		return &ast.Lambda{Pos: e.Pos, Params: e.Params, Body: subst(e.Body, name, repl)}
	case *ast.Match:
		arms := make([]*ast.Arm, len(e.Arms))
		for i, a := range e.Arms {
			body := a.Body
			guard := a.Guard
			if !patBinds(a.Pat, name) {
				body = subst(body, name, repl)
				if guard != nil {
					guard = subst(guard, name, repl)
				}
			}
			arms[i] = &ast.Arm{Pat: a.Pat, Guard: guard, Body: body}
		}
		return &ast.Match{Pos: e.Pos, Scrut: subst(e.Scrut, name, repl), Arms: arms}
	case *ast.Let:
		body := subst(e.Body, name, repl)
		in := e.In
		// The let's own name shadows in its `in` (and rec also in body).
		if e.Name != name && !bindsName(e.Params, name) {
			in = subst(in, name, repl)
		}
		return &ast.Let{Pos: e.Pos, Rec: e.Rec, Name: e.Name, Params: e.Params, Pat: e.Pat, Body: body, In: in}
	default:
		// Literals, Ctor, Unit — no subterms with this name.
		return e
	}
}

func substAll(es []ast.Expr, name string, repl ast.Expr) []ast.Expr {
	out := make([]ast.Expr, len(es))
	for i, e := range es {
		out[i] = subst(e, name, repl)
	}
	return out
}

func bindsName(params []ast.Param, name string) bool {
	for _, p := range params {
		if vp, ok := p.(ast.VarParam); ok && vp.Name == name {
			return true
		}
	}
	return false
}

// patBinds reports whether a pattern binds name (a conservative check: any
// variable in the pattern named `name`).
func patBinds(p ast.Pattern, name string) bool {
	switch p := p.(type) {
	case ast.VarPat:
		return p.Name == name
	case ast.CtorPat:
		for _, a := range p.Args {
			if patBinds(a, name) {
				return true
			}
		}
	case ast.TuplePat:
		for _, a := range p.Elems {
			if patBinds(a, name) {
				return true
			}
		}
	}
	return false
}

// AsStyle reports whether e is a fully-applied `__style "prop" "value"` with both
// arguments reduced to string literals, returning (prop, value, true) if so.
func AsStyle(e ast.Expr) (prop, value string, ok bool) {
	head, args := flattenApp2(e)
	v, isVar := head.(*ast.Var)
	if !isVar || v.Name != "__style" || len(args) != 2 {
		return "", "", false
	}
	p, ok1 := args[0].(*ast.StrLit)
	val, ok2 := args[1].(*ast.StrLit)
	if !ok1 || !ok2 {
		return "", "", false
	}
	return p.Value, val.Value, true
}

func flattenApp2(e ast.Expr) (ast.Expr, []ast.Expr) {
	app, ok := e.(*ast.App)
	if !ok {
		return e, nil
	}
	return flattenApp(app)
}
