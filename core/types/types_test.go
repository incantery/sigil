package types

import (
	"strings"
	"testing"

	"github.com/incantery/mako/core/ast"
	"github.com/incantery/mako/core/parse"
)

// check parses and type-checks src, returning the name->type map.
func check(t *testing.T, src string) map[string]string {
	t.Helper()
	got, err := Check(mustParse(t, src))
	if err != nil {
		t.Fatalf("type error: %v", err)
	}
	return got
}

func mustParse(t *testing.T, src string) *ast.Module {
	t.Helper()
	m, err := parse.Module(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return m
}

func TestInfer(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "identity is polymorphic",
			src:  "let id x = x",
			want: map[string]string{"id": "a -> a"},
		},
		{
			name: "const",
			src:  "let k x y = x",
			want: map[string]string{"k": "a -> b -> a"},
		},
		{
			name: "let-polymorphism across decls",
			src:  "let id x = x\nlet a = id 1\nlet b = id true",
			want: map[string]string{"id": "a -> a", "a": "Int", "b": "Bool"},
		},
		{
			name: "arithmetic constrains to Int",
			src:  "let inc x = x + 1",
			want: map[string]string{"inc": "Int -> Int"},
		},
		{
			name: "if expression",
			src:  "let pick c = if c then 1 else 2",
			want: map[string]string{"pick": "Bool -> Int"},
		},
		{
			name: "list and tuple",
			src:  "let xs = [1, 2, 3]\nlet p = (1, true)",
			want: map[string]string{"xs": "List Int", "p": "(Int, Bool)"},
		},
		{
			name: "empty list generalizes",
			src:  "let e = []",
			want: map[string]string{"e": "List a"},
		},
		{
			name: "builtin Option constructors",
			src:  "let wrap x = Some x\nlet none = None",
			want: map[string]string{"wrap": "a -> Option a", "none": "Option a"},
		},
		{
			name: "recursion: factorial",
			src:  "let rec fact n = if n == 0 then 1 else n * fact (n - 1)",
			want: map[string]string{"fact": "Int -> Int"},
		},
		{
			name: "user ADT constructor types",
			src:  "type Shape = Circle of Float | Rect of (Float, Float)\nlet area s = s",
			want: map[string]string{"area": "a -> a"},
		},
		{
			name: "match over ADT",
			src:  "type Tab = Home | Away\nlet label t =\n  match t with\n  | Home -> 1\n  | Away -> 2",
			want: map[string]string{"label": "Tab -> Int"},
		},
		{
			name: "match binds constructor payload",
			src:  "type Box = Boxed of Int\nlet open b =\n  match b with\n  | Boxed n -> n",
			want: map[string]string{"open": "Box -> Int"},
		},
		{
			name: "pipe operator",
			src:  "let r = 5 |> fun x -> x + 1",
			want: map[string]string{"r": "Int"},
		},
		{
			name: "tuple destructuring binding",
			src:  "let (a, b) = (1, true)",
			want: map[string]string{"a": "Int", "b": "Bool"},
		},
		{
			name: "record literal and field access",
			src:  "let r = { x = 1, y = true }\nlet n = r.x",
			want: map[string]string{"r": "{x: Int, y: Bool}", "n": "Int"},
		},
		{
			name: "string interpolation is String",
			src:  "let g name = \"hi ${name}, you are ${1}\"",
			want: map[string]string{"g": "a -> String"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := check(t, tt.src)
			for name, want := range tt.want {
				if got[name] != want {
					t.Errorf("%s: got %q, want %q", name, got[name], want)
				}
			}
		})
	}
}

// counterSrc is the raw-intrinsic M1 counter (before the M2 stdlib makes it pretty).
const counterSrc = `let counter () =
  let r = __cell 0
  __elem "div" [] [
    __text (fun () -> "count: ${__get r}"),
    __elem "button" [ __on "click" (fun e -> effect { __set r (__get r + 1) }) ] [
      __text (fun () -> "+")
    ]
  ]`

func TestIntrinsics(t *testing.T) {
	got := check(t, counterSrc)
	if got["counter"] != "Unit -> Node" {
		t.Errorf("counter: got %q, want %q", got["counter"], "Unit -> Node")
	}
}

func TestReactiveStructure(t *testing.T) {
	src := `let view () =
  let items = __cell ["a", "b"]
  let show = __cell true
  __elem "ul" [] [
    __each (fun () -> __get items) (fun x -> __elem "li" [] [ __text (fun () -> x) ]),
    __when (fun () -> __get show) (fun () -> __text (fun () -> "shown"))
  ]`
	got := check(t, src)
	if got["view"] != "Unit -> Node" {
		t.Errorf("view: got %q, want %q", got["view"], "Unit -> Node")
	}
}

func TestEffectContext(t *testing.T) {
	// __set inside effect { } is allowed.
	if _, err := Check(mustParse(t, "let h r = effect { __set r 1 }")); err != nil {
		t.Errorf("effect block should typecheck: %v", err)
	}
	// __set outside any effect { } is rejected.
	_, err := Check(mustParse(t, "let bad r = __set r 1"))
	if err == nil || !strings.Contains(err.Error(), "outside an effect") {
		t.Errorf("expected effect-context error, got: %v", err)
	}
	// A read (__get) is pure and allowed anywhere.
	if _, err := Check(mustParse(t, "let v r = __get r")); err != nil {
		t.Errorf("read should be pure: %v", err)
	}
}

func TestTypeErrors(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantSub string // substring expected in the error
	}{
		{
			name:    "add bool to int",
			src:     "let f x = x + true",
			wantSub: "mismatch",
		},
		{
			name:    "unbound variable",
			src:     "let f x = y",
			wantSub: "unbound variable",
		},
		{
			name:    "non-exhaustive match misses a constructor",
			src:     "type Tab = Home | Away | Settings\nlet f t =\n  match t with\n  | Home -> 1\n  | Away -> 2",
			wantSub: "missing Settings",
		},
		{
			name:    "if branches must agree",
			src:     "let f c = if c then 1 else true",
			wantSub: "mismatch",
		},
		{
			name:    "occurs check (infinite type)",
			src:     "let f x = x x",
			wantSub: "infinite type",
		},
		{
			name:    "constructor arity",
			src:     "type Box = Boxed of Int\nlet f b =\n  match b with\n  | Boxed -> 0",
			wantSub: "takes 1 argument",
		},
		{
			name:    "field on non-record",
			src:     "let f = 1\nlet g = f.x",
			wantSub: "non-record",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m, err := parse.Module(tt.src)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			_, err = Check(m)
			if err == nil {
				t.Fatalf("expected type error containing %q, got none", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantSub)
			}
		})
	}
}
