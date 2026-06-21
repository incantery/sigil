package analysis

import (
	"testing"

	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/parse"
)

func roleAt(t *testing.T, roles map[ast.Pos]Role, line, col int) Role {
	t.Helper()
	r, ok := roles[ast.Pos{Line: line, Col: col}]
	if !ok {
		t.Fatalf("no role recorded at %d:%d", line, col)
	}
	return r
}

func TestSemanticRoles(t *testing.T) {
	src := "type Color = Red\n" + // Color@1:6 type, Red@1:14 enumMember
		"let inc n = n\n" + //   inc@2:5 function, n@2:9 parameter, n@2:13 variable(use)
		"let r = inc\n" + //     r@3:5 variable, inc@3:9 function(use, in funcNames)
		"let g x = x.field\n" + // g@4:5 function, x@4:7 parameter, x@4:11 variable, field@4:13 property
		"let h c = match c with | Some y -> y\n" // Some@5:26 enumMember, y@5:31 variable
	m, err := parse.Module(src)
	if err != nil {
		t.Fatal(err)
	}
	roles := SemanticRoles(m)

	cases := []struct {
		line, col int
		want      Role
		what      string
	}{
		{1, 6, RoleType, "Color (type decl)"},
		{1, 14, RoleEnumMember, "Red (variant)"},
		{2, 5, RoleFunction, "inc (fn decl)"},
		{2, 9, RoleParameter, "n (param)"},
		{2, 13, RoleVariable, "n (use)"},
		{3, 5, RoleVariable, "r (value decl)"},
		{3, 9, RoleFunction, "inc (fn use)"},
		{4, 11, RoleVariable, "x (param use in x.field)"},
		{4, 13, RoleProperty, "field (access)"},
		{5, 26, RoleEnumMember, "Some (ctor pattern)"},
		{5, 31, RoleVariable, "y (pattern binder in Some y)"},
	}
	for _, c := range cases {
		if got := roleAt(t, roles, c.line, c.col); got != c.want {
			t.Errorf("%s @ %d:%d = role %d, want %d", c.what, c.line, c.col, got, c.want)
		}
	}
}

func TestCollectFunctionNames(t *testing.T) {
	m, _ := parse.Module("let inc n = n\nlet v = 1\n")
	fns := collectFunctionNames(m)
	if !fns["inc"] {
		t.Error("inc (has params) should be a function name")
	}
	if fns["v"] {
		t.Error("v (no params) should not be a function name")
	}
}
