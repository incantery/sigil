// Package emit lowers a type-checked sigil core module to plain JavaScript.
//
// The output is npm-free, dependency-free JS: ADT values become tagged objects
// ({$:"Some", _0: x}), tuples and lists become arrays, records become objects,
// and curried functions become nested arrows. There is no runtime framework —
// only a tiny prelude of equality/stringify helpers and the built-in Option
// constructors.
package emit

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/parse"
	"github.com/incantery/sigil/internal/peval"
	"github.com/incantery/sigil/internal/token"
	"github.com/incantery/sigil/internal/types"
)

// prelude is emitted ahead of user code.
const prelude = `"use strict";
function __eq(a, b) {
  if (a === b) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  if (Array.isArray(a)) {
    if (!Array.isArray(b) || a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (!__eq(a[i], b[i])) return false;
    return true;
  }
  const ka = Object.keys(a), kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  for (const k of ka) if (!__eq(a[k], b[k])) return false;
  return true;
}
function __str(x) {
  if (typeof x === "string") return x;
  if (x === null || x === undefined) return "()";
  if (typeof x === "object") {
    if (x.$ !== undefined) return x._0 !== undefined ? x.$ + "(" + __str(x._0) + ")" : x.$;
    if (Array.isArray(x)) return "[" + x.map(__str).join(", ") + "]";
  }
  return String(x);
}
const $Some = ($0) => ({ $: "Some", _0: $0 });
const $None = { $: "None" };

// --- reactive core (fine-grained signals) ---
let __ctx = null;
function __runEffect(e) {
  for (const c of e.deps) c.subs.delete(e);
  e.deps.clear();
  const prev = __ctx;
  __ctx = e;
  try { e.fn(); } finally { __ctx = prev; }
}
const __cell = (init) => ({ v: init, subs: new Set() });
const __get = (c) => {
  if (__ctx) { c.subs.add(__ctx); __ctx.deps.add(c); }
  return c.v;
};
const __set = (c) => (val) => {
  if (__eq(c.v, val)) return null;
  c.v = val;
  for (const e of [...c.subs]) __runEffect(e);
  return null;
};
const __effect = (fn) => { __runEffect({ fn, deps: new Set() }); return null; };
// Run fn without subscribing the current effect to any cell it reads.
const __untrack = (fn) => { const p = __ctx; __ctx = null; try { return fn(); } finally { __ctx = p; } };

// --- host (DOM) ---
const __elem = (tag) => (attrs) => (kids) => {
  const el = document.createElement(tag);
  for (const a of attrs) a(el);
  for (const k of kids) el.appendChild(k);
  return el;
};
const __text = (thunk) => {
  const n = document.createTextNode("");
  __effect(() => { n.textContent = thunk(); });
  return n;
};
const __attr = (name) => (val) => (el) => { el.setAttribute(name, val); };
// __style sets an inline CSS property. Static styles are hoisted to atomic
// classes at build time (see __addClass / __installStyles); this is the runtime
// fallback for any style the compiler could not fold to a literal.
const __style = (prop) => (val) => (el) => { el.style.setProperty(prop, val); };
const __addClass = (cls) => (el) => { el.classList.add(cls); };
const __installStyles = (css) => { const s = document.createElement("style"); s.textContent = css; document.head.appendChild(s); };
const __bindAttr = (name) => (thunk) => (el) => { __effect(() => { el.setAttribute(name, thunk()); }); };
const __on = (ev) => (handler) => (el) => { el.addEventListener(ev, (event) => handler(event)()); };
// Total decoder: read event.target.value, or "" when there is none.
const __eventValue = (e) => { const t = e && e.target; return (t && t.value != null) ? String(t.value) : ""; };
const __mount = (node) => (sel) => { document.querySelector(sel).appendChild(node); return null; };
// __fetch performs a network request, then calls the continuation with the
// (ok, body) primitives. Every outcome — HTTP error or network failure — routes
// through the same callback, so it is total: no exception ever crosses into
// sigil. stdlib decodes (ok, body) into a Result.
const __fetch = (url) => (cb) => {
  fetch(url).then((r) => r.text().then((t) => cb(r.ok)(t)())).catch((e) => cb(false)(String(e))());
  return null;
};
// --- location (history) boundary ---
const __path = () => window.location.pathname;
const __pushPath = (p) => { window.history.pushState({}, "", p); return null; };
const __onPopState = (cb) => { window.addEventListener("popstate", () => cb()); return null; };
// --- data primitives ---
const __split = (s) => (sep) => s.split(sep);
const __listLen = (xs) => xs.length;
const __listAt = (xs) => (i) => (i >= 0 && i < xs.length) ? $Some(xs[i]) : $None;
const __listConcat = (a) => (b) => a.concat(b);

// --- reactive structure ---
// A wrapper (display:contents) whose children the runtime reconciles. Child
// rendering is untracked so the structural effect subscribes only to the source.
const __each = (src) => (render) => {
  const box = document.createElement("div");
  box.style.display = "contents";
  let current = []; // [{ item, node }]
  __effect(() => {
    const items = src();
    const used = new Array(current.length).fill(false);
    const next = [];
    for (const item of items) {
      let node = null;
      for (let i = 0; i < current.length; i++) {
        if (!used[i] && __eq(current[i].item, item)) { used[i] = true; node = current[i].node; break; }
      }
      if (!node) node = __untrack(() => render(item));
      next.push({ item, node });
    }
    for (let i = 0; i < current.length; i++) {
      if (!used[i] && current[i].node.parentNode === box) box.removeChild(current[i].node);
    }
    for (const { node } of next) box.appendChild(node); // appendChild reorders existing nodes
    current = next;
  });
  return box;
};
const __when = (cond) => (thunk) => {
  const box = document.createElement("div");
  box.style.display = "contents";
  let node = null;
  __effect(() => {
    if (cond()) {
      if (!node) { node = __untrack(thunk); box.appendChild(node); }
    } else if (node) {
      if (node.parentNode === box) box.removeChild(node);
      node = null;
    }
  });
  return box;
};
`

// Runtime returns the JavaScript runtime prelude (equality/stringify helpers, the
// signal graph, and the DOM host) that every compiled module is prefixed with.
func Runtime() string { return prelude }

// devPrelude is the production prelude with four intrinsics swapped for
// HMR-instrumented versions that call into a global __sigilDev registry (set up
// by the dev client agent, or by tests). It is derived from prelude by exact
// string replacement so any future prelude edit propagates; the replacements are
// guarded below.
var devPrelude = buildDevPrelude()

type preludeSwap struct{ from, to string }

var devSwaps = []preludeSwap{
	{
		from: `const __cell = (init) => ({ v: init, subs: new Set() });`,
		to:   `const __cell = (init) => { const __i = __sigilDev.counter++; const __v = __sigilDev.hydration.has(__i) ? __sigilDev.hydration.get(__i) : init; const __c = { v: __v, subs: new Set() }; __sigilDev.cells.set(__i, __c); return __c; };`,
	},
	{
		from: `const __onPopState = (cb) => { window.addEventListener("popstate", () => cb()); return null; };`,
		to:   `const __onPopState = (cb) => { const __h = () => cb(); window.addEventListener("popstate", __h); __sigilDev.disposers.push(() => window.removeEventListener("popstate", __h)); return null; };`,
	},
	{
		from: `const __installStyles = (css) => { const s = document.createElement("style"); s.textContent = css; document.head.appendChild(s); };`,
		to:   `const __installStyles = (css) => { let __s = document.getElementById("__sigil_styles"); if (!__s) { __s = document.createElement("style"); __s.id = "__sigil_styles"; document.head.appendChild(__s); } __s.textContent = css; };`,
	},
	{
		from: "const __fetch = (url) => (cb) => {\n  fetch(url).then((r) => r.text().then((t) => cb(r.ok)(t)())).catch((e) => cb(false)(String(e))());\n  return null;\n};",
		to:   "const __fetch = (url) => (cb) => {\n  const __g = __sigilDev.generation; const __live = () => __g === __sigilDev.generation;\n  fetch(url).then((r) => r.text().then((t) => { if (__live()) cb(r.ok)(t)(); })).catch((e) => { if (__live()) cb(false)(String(e))(); });\n  return null;\n};",
	},
}

func buildDevPrelude() string {
	p := prelude
	for _, s := range devSwaps {
		if strings.Count(p, s.from) != 1 {
			panic("emit: dev prelude swap target not found exactly once: " + s.from)
		}
		p = strings.Replace(p, s.from, s.to, 1)
	}
	return p
}

// testRuntime defines the globals that `test`/`expect` lower into. It is
// appended to the production prelude to form testPrelude. These are test-only
// JS helpers — not kernel intrinsics.
const testRuntime = `
const __tests = [];
let __cur = null;
const __test = (name, thunk) => { __tests.push({ name: name, thunk: thunk }); };
const __expect = (m) => { __cur.push({ pass: m.pass, label: m.label, got: m.got, expected: m.expected }); };
const __runTests = () => __tests.map((t) => {
  __cur = [];
  let error = null;
  try { t.thunk(); } catch (e) { error = String(e); }
  return { name: t.name, expects: __cur, error: error };
});
`

var testPrelude = prelude + testRuntime

// Compile parses, type-checks, and emits JavaScript for src. A type error aborts
// before emission, so emitted code is always well-typed.
func Compile(src string) (string, error) {
	m, err := parse.Module(src)
	if err != nil {
		return "", err
	}
	if _, err := types.Check(m); err != nil {
		return "", err
	}
	return Module(m)
}

// Module emits JavaScript for an already-parsed module (no type checking).
func Module(m *ast.Module) (string, error) {
	e := &emitter{}
	var b strings.Builder
	b.WriteString(prelude)
	for _, d := range m.Decls {
		s, err := e.decl(d)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// ImportBinding rebinds names exported by the module FromID into the local scope
// of the module being emitted.
type ImportBinding struct {
	FromID string
	Names  []string // sigil names (values and/or constructors)
}

// LinkedModule is one node of a resolved import graph, ready to emit. Each is
// wrapped in its own IIFE so non-public top-level bindings can't collide across
// modules; the prelude (intrinsics, runtime) is shared and emitted once.
type LinkedModule struct {
	ID      string // JS-safe, unique within the bundle
	AST     *ast.Module
	Imports []ImportBinding
	Exports []string // sigil names this module exposes to importers
}

// Bundle emits a single npm-free JS program for a topologically ordered set of
// linked modules (dependencies before dependents). Each module becomes
//
//	const __m_<id> = (() => { <imports>; <decls>; return { <exports> }; })();
//
// so module scopes are isolated and imports are explicit re-bindings.
func Bundle(mods []LinkedModule, env *peval.Env) (string, error) {
	return bundle(mods, env, prelude, false)
}

// BundleDev is Bundle with the HMR-instrumented dev prelude.
func BundleDev(mods []LinkedModule, env *peval.Env) (string, error) {
	return bundle(mods, env, devPrelude, false)
}

// BundleTest is Bundle with the test prelude; test declarations are emitted as
// __test registrations and run by __runTests().
func BundleTest(mods []LinkedModule, env *peval.Env) (string, error) {
	return bundle(mods, env, testPrelude, true)
}

func bundle(mods []LinkedModule, env *peval.Env, pre string, test bool) (string, error) {
	sheet := newCSSSheet()
	var mb strings.Builder // module bodies (populate the sheet as a side effect)
	for _, mod := range mods {
		fmt.Fprintf(&mb, "const __m_%s = (() => {\n", mod.ID)
		for _, imp := range mod.Imports {
			for _, n := range imp.Names {
				fmt.Fprintf(&mb, "const %s = __m_%s.%s;\n", mangle(n), imp.FromID, mangle(n))
			}
		}
		e := &emitter{sheet: sheet, penv: env, test: test}
		for _, d := range mod.AST.Decls {
			s, err := e.decl(d)
			if err != nil {
				return "", err
			}
			mb.WriteString(s)
			mb.WriteByte('\n')
		}
		mb.WriteString("return {")
		for i, n := range mod.Exports {
			if i > 0 {
				mb.WriteString(", ")
			}
			fmt.Fprintf(&mb, "%s: %s", mangle(n), mangle(n))
		}
		mb.WriteString("};\n})();\n")
	}

	var b strings.Builder
	b.WriteString(pre)
	// Install the extracted atomic stylesheet once, before any module mounts.
	if !sheet.empty() {
		fmt.Fprintf(&b, "__installStyles(%s);\n", strconv.Quote(sheet.css()))
	}
	b.WriteString(mb.String())
	return b.String(), nil
}

type emitter struct {
	tmp   int
	sheet *cssSheet  // accumulates extracted atomic CSS rules (nil = no extraction)
	penv  *peval.Env // partial-evaluator env for style folding (nil = no extraction)
	test  bool       // emit test declarations (testDecl) instead of dropping them
}

// cssSheet collects the atomic CSS rules extracted from static styles. Identical
// property:value pairs share one class (atomic, dead-code-free).
type cssSheet struct {
	order  []string          // class names, insertion order
	rules  map[string]string // class name -> "prop:value"
	byDecl map[string]string // "prop:value" -> class name (dedup)
}

func newCSSSheet() *cssSheet {
	return &cssSheet{rules: map[string]string{}, byDecl: map[string]string{}}
}

// class returns the (deduplicated) atomic class name for a property:value pair,
// registering the rule on first sight.
func (s *cssSheet) class(prop, val string) string {
	decl := prop + ":" + val
	if c, ok := s.byDecl[decl]; ok {
		return c
	}
	h := fnv.New32a()
	h.Write([]byte(decl))
	c := "s" + strconv.FormatUint(uint64(h.Sum32()), 16)
	s.byDecl[decl] = c
	s.rules[c] = decl
	s.order = append(s.order, c)
	return c
}

func (s *cssSheet) css() string {
	var b strings.Builder
	for _, c := range s.order {
		fmt.Fprintf(&b, ".%s{%s}", c, s.rules[c])
	}
	return b.String()
}

func (s *cssSheet) empty() bool { return len(s.order) == 0 }

func (e *emitter) fresh() string {
	e.tmp++
	return fmt.Sprintf("__t%d", e.tmp)
}

// mangle maps a sigil name to a JS identifier. User names are prefixed with $ to
// avoid clashing with JS keywords/globals; __intrinsics pass through.
func mangle(name string) string {
	if strings.HasPrefix(name, "__") {
		return name
	}
	return "$" + name
}

func (e *emitter) decl(d ast.Decl) (string, error) {
	switch d := d.(type) {
	case *ast.TypeDecl:
		return e.typeDecl(d), nil
	case *ast.LetDecl:
		if d.Name == "" {
			tmp := e.fresh()
			body, err := e.expr(d.Body)
			if err != nil {
				return "", err
			}
			stmts := bindPattern(d.Pat, tmp)
			return fmt.Sprintf("const %s = %s;\n%s", tmp, body, strings.Join(stmts, "\n")), nil
		}
		val, err := e.value(d.Params, d.Body)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("const %s = %s;", mangle(d.Name), val), nil
	case *ast.TestDecl:
		return e.testDecl(d)
	default:
		return "", fmt.Errorf("cannot emit %T", d)
	}
}

// testDecl emits a __test registration, or "" in a non-test build (so test
// declarations are dropped from build/serve/dev bundles).
func (e *emitter) testDecl(d *ast.TestDecl) (string, error) {
	if !e.test {
		return "", nil
	}
	var b strings.Builder
	for _, s := range d.Body {
		js, err := e.testStmt(s)
		if err != nil {
			return "", err
		}
		b.WriteString(js)
		b.WriteByte(' ')
	}
	return fmt.Sprintf("__test(%s, () => { %s});", strconv.Quote(d.Name), b.String()), nil
}

func (e *emitter) testStmt(s ast.TestStmt) (string, error) {
	switch s := s.(type) {
	case *ast.TestLet:
		v, err := e.expr(s.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("const %s = %s;", mangle(s.Name), v), nil
	case *ast.TestExpect:
		x, err := e.expr(s.X)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("__expect(%s);", x), nil
	case *ast.TestRun:
		x, err := e.expr(s.X)
		if err != nil {
			return "", err
		}
		return x + ";", nil
	default:
		return "", fmt.Errorf("cannot emit test statement %T", s)
	}
}

// typeDecl emits a const for each data constructor.
func (e *emitter) typeDecl(d *ast.TypeDecl) string {
	if d.Record != nil {
		return "" // record types have no runtime constructors
	}
	var b strings.Builder
	for _, v := range d.Variants {
		if v.Arg == nil {
			fmt.Fprintf(&b, "const %s = { $: %q };\n", mangle(v.Name), v.Name)
		} else {
			fmt.Fprintf(&b, "const %s = ($0) => ({ $: %q, _0: $0 });\n", mangle(v.Name), v.Name)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// value emits a binding's right-hand side: a curried function if it has params,
// otherwise the body expression.
func (e *emitter) value(params []ast.Param, body ast.Expr) (string, error) {
	if len(params) == 0 {
		return e.expr(body)
	}
	return e.lambda(params, body)
}

func (e *emitter) lambda(params []ast.Param, body ast.Expr) (string, error) {
	if len(params) == 0 {
		return e.expr(body)
	}
	name, stmts := e.param(params[0])
	inner, err := e.lambda(params[1:], body)
	if err != nil {
		return "", err
	}
	if len(stmts) == 0 {
		// A record literal starting with '{' is ambiguous with a JS block; wrap in
		// parens so the engine sees it as an expression.
		body := inner
		if strings.HasPrefix(inner, "{") {
			body = "(" + inner + ")"
		}
		return fmt.Sprintf("(%s) => %s", name, body), nil
	}
	return fmt.Sprintf("(%s) => { %s return %s; }", name, strings.Join(stmts, " "), inner), nil
}

// param returns the JS parameter name and any binding statements needed to
// destructure it.
func (e *emitter) param(p ast.Param) (string, []string) {
	switch p := p.(type) {
	case ast.VarParam:
		return mangle(p.Name), nil
	case ast.WildParam:
		return e.fresh(), nil
	case ast.PatParam:
		arg := e.fresh()
		return arg, bindPattern(p.Pat, arg)
	case ast.RecordParam:
		arg := e.fresh()
		var stmts []string
		for _, f := range p.Fields {
			access := fmt.Sprintf("%s.%s", arg, f.Name)
			if f.Default != nil {
				def, _ := e.expr(f.Default)
				stmts = append(stmts, fmt.Sprintf("const %s = (%s !== undefined) ? %s : (%s);", mangle(f.Name), access, access, def))
			} else {
				stmts = append(stmts, fmt.Sprintf("const %s = %s;", mangle(f.Name), access))
			}
		}
		return arg, stmts
	default:
		return e.fresh(), nil
	}
}

func (e *emitter) expr(ex ast.Expr) (string, error) {
	switch ex := ex.(type) {
	case *ast.IntLit:
		return ex.Raw, nil
	case *ast.FloatLit:
		return ex.Raw, nil
	case *ast.StrLit:
		return strconv.Quote(ex.Value), nil
	case *ast.Interp:
		parts := []string{`""`}
		for _, p := range ex.Parts {
			s, err := e.expr(p)
			if err != nil {
				return "", err
			}
			if _, ok := p.(*ast.StrLit); ok {
				parts = append(parts, s)
			} else {
				parts = append(parts, "__str("+s+")")
			}
		}
		return "(" + strings.Join(parts, " + ") + ")", nil
	case *ast.Var:
		if ex.Name == "true" || ex.Name == "false" {
			return ex.Name, nil
		}
		return mangle(ex.Name), nil
	case *ast.Ctor:
		return mangle(ex.Name), nil
	case *ast.Unit:
		return "null", nil
	case *ast.Tuple:
		return e.list(ex.Elems)
	case *ast.ListLit:
		return e.list(ex.Elems)
	case *ast.RecordLit:
		var fs []string
		for _, f := range ex.Fields {
			v, err := e.expr(f.Value)
			if err != nil {
				return "", err
			}
			fs = append(fs, fmt.Sprintf("%s: %s", f.Name, v))
		}
		return "{ " + strings.Join(fs, ", ") + " }", nil
	case *ast.Lambda:
		return e.lambda(ex.Params, ex.Body)
	case *ast.App:
		fn, err := e.expr(ex.Fn)
		if err != nil {
			return "", err
		}
		arg, err := e.expr(ex.Arg)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s)(%s)", fn, arg), nil
	case *ast.Field:
		recv, err := e.expr(ex.Recv)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s).%s", recv, ex.Name), nil
	case *ast.Binop:
		return e.binop(ex)
	case *ast.Unop:
		x, err := e.expr(ex.X)
		if err != nil {
			return "", err
		}
		if ex.Op == token.MINUS {
			return "(-" + x + ")", nil
		}
		return "(!" + x + ")", nil
	case *ast.If:
		c, err := e.expr(ex.Cond)
		if err != nil {
			return "", err
		}
		t, err := e.expr(ex.Then)
		if err != nil {
			return "", err
		}
		f, err := e.expr(ex.Else)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s ? %s : %s)", c, t, f), nil
	case *ast.Match:
		return e.match(ex)
	case *ast.Let:
		return e.letExpr(ex)
	case *ast.Effect:
		var stmts []string
		for _, s := range ex.Stmts {
			js, err := e.expr(s)
			if err != nil {
				return "", err
			}
			stmts = append(stmts, js)
		}
		body := strings.Join(stmts, "; ")
		if body != "" {
			body += ";"
		}
		return "(() => { " + body + " })", nil
	default:
		return "", fmt.Errorf("cannot emit expression %T", ex)
	}
}

func (e *emitter) list(elems []ast.Expr) (string, error) {
	var ps []string
	for _, el := range elems {
		// Compile-time CSS extraction: if a list element partial-evaluates to a
		// static `__style "prop" "val"`, hoist it to an atomic class and emit a
		// class-add instead of an inline-style attr. Non-foldable styles fall
		// through to the normal path (the __style runtime sets them inline).
		if e.sheet != nil && e.penv != nil {
			if prop, val, ok := peval.AsStyle(e.penv.Eval(el)); ok {
				cls := e.sheet.class(prop, val)
				ps = append(ps, fmt.Sprintf("__addClass(%s)", strconv.Quote(cls)))
				continue
			}
		}
		s, err := e.expr(el)
		if err != nil {
			return "", err
		}
		ps = append(ps, s)
	}
	return "[" + strings.Join(ps, ", ") + "]", nil
}

func (e *emitter) binop(b *ast.Binop) (string, error) {
	l, err := e.expr(b.L)
	if err != nil {
		return "", err
	}
	r, err := e.expr(b.R)
	if err != nil {
		return "", err
	}
	switch b.Op {
	case token.PLUS:
		return fmt.Sprintf("(%s + %s)", l, r), nil
	case token.MINUS:
		return fmt.Sprintf("(%s - %s)", l, r), nil
	case token.STAR:
		return fmt.Sprintf("(%s * %s)", l, r), nil
	case token.SLASH:
		return fmt.Sprintf("Math.trunc(%s / %s)", l, r), nil
	case token.PERCENT:
		return fmt.Sprintf("(%s %% %s)", l, r), nil
	case token.LT:
		return fmt.Sprintf("(%s < %s)", l, r), nil
	case token.GT:
		return fmt.Sprintf("(%s > %s)", l, r), nil
	case token.LE:
		return fmt.Sprintf("(%s <= %s)", l, r), nil
	case token.GE:
		return fmt.Sprintf("(%s >= %s)", l, r), nil
	case token.EQEQ:
		return fmt.Sprintf("__eq(%s, %s)", l, r), nil
	case token.NEQ:
		return fmt.Sprintf("(!__eq(%s, %s))", l, r), nil
	case token.ANDAND:
		return fmt.Sprintf("(%s && %s)", l, r), nil
	case token.OROR:
		return fmt.Sprintf("(%s || %s)", l, r), nil
	case token.CONCAT:
		return fmt.Sprintf("(%s + %s)", l, r), nil
	case token.PIPEFWD:
		return fmt.Sprintf("(%s)(%s)", r, l), nil
	default:
		return "", fmt.Errorf("cannot emit operator %s", b.Op)
	}
}

func (e *emitter) letExpr(l *ast.Let) (string, error) {
	var decl string
	if l.Name == "" {
		tmp := e.fresh()
		body, err := e.expr(l.Body)
		if err != nil {
			return "", err
		}
		binds := bindPattern(l.Pat, tmp)
		decl = fmt.Sprintf("const %s = %s; %s", tmp, body, strings.Join(binds, " "))
	} else {
		val, err := e.value(l.Params, l.Body)
		if err != nil {
			return "", err
		}
		decl = fmt.Sprintf("const %s = %s;", mangle(l.Name), val)
	}
	in, err := e.expr(l.In)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(() => { %s return %s; })()", decl, in), nil
}

func (e *emitter) match(m *ast.Match) (string, error) {
	scrut, err := e.expr(m.Scrut)
	if err != nil {
		return "", err
	}
	subj := e.fresh()
	var arms strings.Builder
	for _, arm := range m.Arms {
		cond, binds := compilePattern(arm.Pat, subj)
		body, err := e.expr(arm.Body)
		if err != nil {
			return "", err
		}
		bindStr := strings.Join(binds, " ")
		if arm.Guard != nil {
			g, err := e.expr(arm.Guard)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&arms, "if (%s) { %s if (%s) { return %s; } } ", cond, bindStr, g, body)
		} else {
			fmt.Fprintf(&arms, "if (%s) { %s return %s; } ", cond, bindStr, body)
		}
	}
	return fmt.Sprintf("((%s) => { %sthrow new Error(\"non-exhaustive match\"); })(%s)", subj, arms.String(), scrut), nil
}

// compilePattern returns a JS boolean test and the binding statements for pat
// against the value at path.
func compilePattern(p ast.Pattern, path string) (cond string, binds []string) {
	switch p := p.(type) {
	case ast.WildPat:
		return "true", nil
	case ast.VarPat:
		return "true", []string{fmt.Sprintf("const %s = %s;", mangle(p.Name), path)}
	case ast.IntPat:
		return fmt.Sprintf("%s === %s", path, p.Raw), nil
	case ast.FloatPat:
		return fmt.Sprintf("%s === %s", path, p.Raw), nil
	case ast.StrPat:
		return fmt.Sprintf("%s === %s", path, strconv.Quote(p.Value)), nil
	case ast.TuplePat:
		return combinePatterns(p.Elems, path)
	case ast.ListPat:
		conds := []string{fmt.Sprintf("%s.length === %d", path, len(p.Elems))}
		sub, subBinds := combinePatterns(p.Elems, path)
		if sub != "true" {
			conds = append(conds, sub)
		}
		return strings.Join(conds, " && "), subBinds
	case ast.RecordPat:
		var conds []string
		var allBinds []string
		for _, f := range p.Fields {
			sub := f.Pat
			if sub == nil {
				sub = ast.VarPat{Name: f.Name}
			}
			c, bs := compilePattern(sub, fmt.Sprintf("%s.%s", path, f.Name))
			if c != "true" {
				conds = append(conds, c)
			}
			allBinds = append(allBinds, bs...)
		}
		if len(conds) == 0 {
			return "true", allBinds
		}
		return strings.Join(conds, " && "), allBinds
	case ast.CtorPat:
		conds := []string{fmt.Sprintf("%s.$ === %q", path, p.Name)}
		var allBinds []string
		if len(p.Args) == 1 {
			c, bs := compilePattern(p.Args[0], path+"._0")
			if c != "true" {
				conds = append(conds, c)
			}
			allBinds = append(allBinds, bs...)
		}
		return strings.Join(conds, " && "), allBinds
	default:
		return "true", nil
	}
}

// combinePatterns compiles a sequence of element patterns indexed positionally
// into path[0], path[1], ...
func combinePatterns(elems []ast.Pattern, path string) (cond string, binds []string) {
	var conds []string
	for i, el := range elems {
		sub := fmt.Sprintf("%s[%d]", path, i)
		c, bs := compilePattern(el, sub)
		if c != "true" {
			conds = append(conds, c)
		}
		binds = append(binds, bs...)
	}
	if len(conds) == 0 {
		return "true", binds
	}
	return strings.Join(conds, " && "), binds
}

// bindPattern produces irrefutable binding statements (used for let-bindings and
// destructuring parameters, where the type checker guarantees the shape).
func bindPattern(p ast.Pattern, path string) []string {
	_, binds := compilePattern(p, path)
	return binds
}
