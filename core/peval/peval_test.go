package peval

import (
	"testing"

	"github.com/incantery/mako/core/ast"
	"github.com/incantery/mako/core/parse"
)

// envFrom builds a peval Env from module source.
func envFrom(t *testing.T, src string) *Env {
	t.Helper()
	m, err := parse.Module(src)
	if err != nil {
		t.Fatalf("parse module: %v", err)
	}
	env := NewEnv()
	env.AddModule(m)
	return env
}

// evalExpr parses and partially evaluates a standalone expression.
func evalExpr(t *testing.T, env *Env, src string) ast.Expr {
	t.Helper()
	e, err := parse.Expr(src)
	if err != nil {
		t.Fatalf("parse expr %q: %v", src, err)
	}
	return env.Eval(e)
}

// wantStyle asserts the reduced expr is `__style "prop" "value"`.
func wantStyle(t *testing.T, env *Env, src, prop, value string) {
	t.Helper()
	gp, gv, ok := AsStyle(evalExpr(t, env, src))
	if !ok {
		t.Fatalf("%q did not reduce to a __style literal", src)
	}
	if gp != prop || gv != value {
		t.Fatalf("%q reduced to __style %q %q, want %q %q", src, gp, gv, prop, value)
	}
}

func TestInlineValueBinding(t *testing.T) {
	env := envFrom(t, `let s4 = "1rem"
pub let p v = __style "padding" v`)
	// p s4 must inline the function and the value to a ground __style.
	wantStyle(t, env, "p s4", "padding", "1rem")
}

func TestInlineLiteralArg(t *testing.T) {
	env := envFrom(t, `pub let p v = __style "padding" v`)
	wantStyle(t, env, `p "8px"`, "padding", "8px")
}

func TestMultiParamInline(t *testing.T) {
	env := envFrom(t, `pub let raw prop val = __style prop val`)
	wantStyle(t, env, `raw "color" "red"`, "color", "red")
}

func TestNestedInline(t *testing.T) {
	// p delegates to raw; both must inline.
	env := envFrom(t, `let raw prop val = __style prop val
let sky = "#38bdf8"
pub let bg c = raw "background-color" c`)
	wantStyle(t, env, "bg sky", "background-color", "#38bdf8")
}

// TestMatchReduction folds a match over a known nullary constructor — the core
// of typed style tokens (space S4 -> "1rem").
func TestMatchReduction(t *testing.T) {
	env := envFrom(t, `type Space = S0 | S4 | S8
let space s = match s with
  | S0 -> "0"
  | S4 -> "1rem"
  | S8 -> "2rem"
pub let p s = __style "padding" (space s)`)
	wantStyle(t, env, "p S4", "padding", "1rem")
	wantStyle(t, env, "p S8", "padding", "2rem")
}

// TestMatchPayloadBinding folds a match that binds a constructor payload.
func TestMatchPayloadBinding(t *testing.T) {
	env := envFrom(t, `type Pad = Px of String
let render p =
  match p with
  | Px v -> v
pub let pad p = __style "padding" (render p)`)
	wantStyle(t, env, `pad (Px "3px")`, "padding", "3px")
}

// TestIrreducibleLeftIntact confirms an expression peval can't fold (a cell read)
// is returned without being mangled, so dynamic styles fall back to runtime.
func TestIrreducibleLeftIntact(t *testing.T) {
	env := envFrom(t, `pub let p v = __style "padding" v`)
	got := evalExpr(t, env, "p (__get c)")
	if _, _, ok := AsStyle(got); ok {
		t.Fatalf("dynamic style should not reduce to a literal")
	}
}

// TestUnknownVarUntouched confirms a free variable with no definition survives.
func TestUnknownVarUntouched(t *testing.T) {
	env := NewEnv()
	if got := evalExpr(t, env, "mystery"); got == nil {
		t.Fatal("expected an expression back")
	}
}
