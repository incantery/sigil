package types

import "github.com/incantery/sigil/internal/ast"

// matchType is the structural record every `expect` argument must produce.
func matchType() *TRecord {
	return &TRecord{Fields: map[string]Type{
		"pass":     tBool,
		"label":    tString,
		"got":      tString,
		"expected": tString,
	}}
}

// checkTest type-checks a `test "name" { ... }` declaration. Its body is an
// effect context: each statement is inferred in a child scope, `let` bindings
// extend the scope for subsequent statements, and every `expect` argument must
// unify with the Match record shape.
func (c *Checker) checkTest(td *ast.TestDecl, parent *env) error {
	scope := newEnv(parent)
	for _, s := range td.Body {
		switch s := s.(type) {
		case *ast.TestLet:
			c.enterLevel()
			t, err := c.infer(s.Value, scope)
			if err != nil {
				return err
			}
			c.exitLevel()
			scope.set(s.Name, c.generalize(t))
		case *ast.TestExpect:
			t, err := c.infer(s.X, scope)
			if err != nil {
				return err
			}
			if err := c.unify(t, matchType(), s.Pos); err != nil {
				return err
			}
		case *ast.TestRun:
			if _, err := c.infer(s.X, scope); err != nil {
				return err
			}
		}
	}
	return nil
}
