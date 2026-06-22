package parse

import (
	"strings"
	"testing"

	"github.com/incantery/sigil/internal/ast"
)

func TestParseModule(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string // ast.Dump output (trimmed)
	}{
		{
			name: "value binding",
			src:  "let x = 1",
			want: "(let x 1)",
		},
		{
			name: "function with params",
			src:  "let add a b = a + b",
			want: "(let add a b (+ a b))",
		},
		{
			name: "operator precedence and associativity",
			src:  "let r = 1 + 2 * 3 - 4",
			want: "(let r (- (+ 1 (* 2 3)) 4))",
		},
		{
			name: "pipe is loosest, application tighter than binop",
			src:  "let r = xs |> map f + g y",
			want: "(let r (|> xs (+ (app map f) (app g y))))",
		},
		{
			name: "application left-assoc and field access",
			src:  "let r = List.map f xs",
			want: "(let r (app (app (. List map) f) xs))",
		},
		{
			name: "tuple destructuring binding",
			src:  "let (a, b) = cell 0",
			want: "(let (tuple a b) (app cell 0))",
		},
		{
			name: "lambda and record param with default",
			src:  "let button { label, click = fun () -> unit } = label",
			want: "(let button {label click=(fun unit unit)} label)",
		},
		{
			name: "if as expression",
			src:  "let r = if c then 1 else 2",
			want: "(let r (if c 1 2))",
		},
		{
			name: "block-form if with indented branches",
			src:  "let r =\n  if c then\n    1\n  else\n    2",
			want: "(let r (if c 1 2))",
		},
		{
			name: "block with nested lets",
			src:  "let greet name =\n  let g = name\n  g",
			want: "(let greet name (let g name g))",
		},
		{
			name: "match with constructors",
			src:  "let f t =\n  match t with\n  | Home -> 1\n  | Away n -> n",
			want: "(let f t (match t (Home -> 1) ((Away n) -> n)))",
		},
		{
			name: "adt type decl multiline",
			src:  "type Tab =\n  | Home\n  | Profile\n  | Settings",
			want: "(type Tab Home Profile Settings)",
		},
		{
			name: "adt with payloads inline",
			src:  "type Shape = Circle of Float | Rect of (Float, Float)",
			want: "(type Shape (Circle Float) (Rect (tuple Float Float)))",
		},
		{
			name: "record type decl",
			src:  "type Theme = { surface : String, primary : String }",
			want: "(type Theme (record (surface String) (primary String)))",
		},
		{
			name: "list literal with trailing comma",
			src:  "let xs = [1, 2, 3,]",
			want: "(let xs (list 1 2 3))",
		},
		{
			name: "record literal with pun",
			src:  "let r = { click, gap = 1 }",
			want: "(let r {click=click gap=1})",
		},
		{
			name: "pub and rec",
			src:  "pub let rec loop n = loop n",
			want: "(pub-let-rec loop n (app loop n))",
		},
		{
			name: "effect block single statement",
			src:  "let h = effect { __set r 1 }",
			want: "(let h (effect (app (app __set r) 1)))",
		},
		{
			name: "effect block multiple statements",
			src:  "let h = effect { __set a 1; __set b 2 }",
			want: "(let h (effect (app (app __set a) 1) (app (app __set b) 2)))",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Module(tt.src)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			got := strings.TrimSpace(ast.Dump(m))
			if got != tt.want {
				t.Errorf("AST mismatch\n src:  %q\n got:  %s\n want: %s", tt.src, got, tt.want)
			}
		})
	}
}

func TestParseImports(t *testing.T) {
	src := `import "github.com/incantery/sigil/std/ui" (Card, Stack)
import "github.com/incantery/sigil/std/style" as Style
import "github.com/incantery/sigil/std/list"
let x = 1`
	m, err := Module(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	got := strings.TrimSpace(ast.Dump(m))
	want := `(import "github.com/incantery/sigil/std/ui" (Card Stack))
(import "github.com/incantery/sigil/std/style" as Style)
(import "github.com/incantery/sigil/std/list")
(let x 1)`
	if got != want {
		t.Errorf("imports mismatch\n got:\n%s\n want:\n%s", got, want)
	}
}

func TestParseInterpolation(t *testing.T) {
	m, err := Module(`let g name = "hello, ${name ()}!"`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	got := strings.TrimSpace(ast.Dump(m))
	want := `(let g name (interp "hello, " (app name unit) "!"))`
	if got != want {
		t.Errorf("interp mismatch\n got:  %s\n want: %s", got, want)
	}
}

func TestParseTestDecl(t *testing.T) {
	src := `test "reverse swaps ends" {
  let xs = [1, 2];
  __set c 1;
  expect (eq xs [2, 1])
}`
	m, err := Module(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(m.Decls) != 1 {
		t.Fatalf("got %d decls, want 1", len(m.Decls))
	}
	td, ok := m.Decls[0].(*ast.TestDecl)
	if !ok {
		t.Fatalf("decl is %T, want *ast.TestDecl", m.Decls[0])
	}
	if td.Name != "reverse swaps ends" {
		t.Errorf("name = %q, want %q", td.Name, "reverse swaps ends")
	}
	if len(td.Body) != 3 {
		t.Fatalf("got %d stmts, want 3", len(td.Body))
	}
	if _, ok := td.Body[0].(*ast.TestLet); !ok {
		t.Errorf("stmt 0 is %T, want *ast.TestLet", td.Body[0])
	}
	if _, ok := td.Body[1].(*ast.TestRun); !ok {
		t.Errorf("stmt 1 is %T, want *ast.TestRun", td.Body[1])
	}
	if _, ok := td.Body[2].(*ast.TestExpect); !ok {
		t.Errorf("stmt 2 is %T, want *ast.TestExpect", td.Body[2])
	}
}

// TestEchoProgram parses the canonical Echo example end to end.
func TestEchoProgram(t *testing.T) {
	src := `import "github.com/incantery/sigil/std/ui" (card, stack, button, text, title)
import "github.com/incantery/sigil/std/reactive" (cell)

let echo () =
  let (name, setName) = cell "alice"
  card [
    title "Echo",
    text "hello, ${name ()}",
    stack { horizontal = true, gap = 1 } [
      button { label = "alice", click = fun () -> setName "alice" },
      button { label = "bob", click = fun () -> setName "bob" },
    ],
  ]`
	m, err := Module(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(m.Imports) != 2 {
		t.Fatalf("want 2 imports, got %d", len(m.Imports))
	}
	if len(m.Decls) != 1 {
		t.Fatalf("want 1 decl, got %d", len(m.Decls))
	}
	// Spot-check it parsed into the expected top-level shape.
	out := ast.Dump(m)
	for _, frag := range []string{"(let echo unit", "(let (tuple name setName) (app cell \"alice\")", "horizontal=true"} {
		if !strings.Contains(out, frag) {
			t.Errorf("expected dump to contain %q\n full:\n%s", frag, out)
		}
	}
}
