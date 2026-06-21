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

func TestSemanticTokensEncoding(t *testing.T) {
	// Two lines exercise single-line deltas and the cross-line reset.
	//   line 1: "let x = 1"   LET kw@0:0 len3, x var@0:4 len1, = op@0:6 len1, 1 num@0:8 len1
	//   line 2: "let y = x"   LET kw@1:0 len3, y var@1:4 len1, = op@1:6 len1, x var@1:8 len1
	data := SemanticTokens("let x = 1\nlet y = x\n")
	want := []uint{
		0, 0, 3, 6, 0, // let     (keyword=6)
		0, 4, 1, 4, 0, // x        (variable=4)
		0, 2, 1, 7, 0, // =        (operator=7)
		0, 2, 1, 8, 0, // 1        (number=8)
		1, 0, 3, 6, 0, // let      (deltaLine=1, absolute col 0)
		0, 4, 1, 4, 0, // y        (variable=4)
		0, 2, 1, 7, 0, // =        (operator=7)
		0, 2, 1, 4, 0, // x (use)  (variable=4)
	}
	if len(data) != len(want) {
		t.Fatalf("len(data) = %d, want %d\n got: %v", len(data), len(want), data)
	}
	for i := range want {
		if data[i] != want[i] {
			t.Fatalf("data[%d] = %d, want %d\n got:  %v\n want: %v", i, data[i], want[i], data, want)
		}
	}
}

func TestSemanticTokensRolesAndKinds(t *testing.T) {
	// type/enumMember/function/string coverage in one line each; just check the
	// tokenType (4th of each 5-tuple) for the identifiers/strings of interest.
	data := SemanticTokens("type C = Red\nlet greet n = \"hi\"\n")
	// Collect (tokenType) values; we only assert presence of the right indices.
	types := map[uint]bool{}
	for i := 3; i < len(data); i += 5 {
		types[data[i]] = true
	}
	for _, idx := range []uint{0 /*type C*/, 1 /*enumMember Red*/, 2 /*function greet*/, 9 /*string "hi"*/} {
		if !types[idx] {
			t.Errorf("expected a token of legend type %d in %v", idx, data)
		}
	}
}

func TestSemanticTokensParseErrorEmpty(t *testing.T) {
	if data := SemanticTokens("let x = ("); len(data) != 0 {
		t.Errorf("parse error should yield empty data, got %v", data)
	}
}
