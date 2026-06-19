package emit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// run compiles src, evaluates entry against the emitted module, and returns the
// exported JS value.
func run(t *testing.T, src, entry string) any {
	t.Helper()
	js, err := Compile(src)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	vm := goja.New()
	v, err := vm.RunString(js + "\n;(" + entry + ")")
	if err != nil {
		t.Fatalf("JS runtime error: %v\n--- emitted ---\n%s", err, js)
	}
	return v.Export()
}

func TestRun(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		entry string
		want  string
	}{
		{
			name:  "arithmetic precedence",
			src:   "let main () = 1 + 2 * 3",
			entry: "$main(null)",
			want:  "7",
		},
		{
			name:  "recursion: factorial",
			src:   "let rec fact n = if n == 0 then 1 else n * fact (n - 1)",
			entry: "$fact(5)",
			want:  "120",
		},
		{
			name:  "match over ADT",
			src:   "type Tab = Home | Away\nlet label t =\n  match t with\n  | Home -> \"h\"\n  | Away -> \"a\"",
			entry: "$label($Away)",
			want:  "a",
		},
		{
			name:  "match Option with payload",
			src:   "let main () =\n  match Some 3 with\n  | Some n -> n + 1\n  | None -> 0",
			entry: "$main(null)",
			want:  "4",
		},
		{
			name:  "curried application",
			src:   "let add a b = a + b",
			entry: "$add(2)(5)",
			want:  "7",
		},
		{
			name:  "list literal",
			src:   "let main () = [1, 2, 3]",
			entry: "$main(null)",
			want:  "[1 2 3]",
		},
		{
			name:  "string interpolation",
			src:   "let greet name = \"hi ${name}, ${1} time\"",
			entry: "$greet(\"bob\")",
			want:  "hi bob, 1 time",
		},
		{
			name:  "pipe operator",
			src:   "let main () = 5 |> fun x -> x + 1",
			entry: "$main(null)",
			want:  "6",
		},
		{
			name:  "record param with default applied",
			src:   "let box { a, b = 10 } = a + b",
			entry: "$box({a: 5})",
			want:  "15",
		},
		{
			name:  "record param with default overridden",
			src:   "let box { a, b = 10 } = a + b",
			entry: "$box({a: 5, b: 20})",
			want:  "25",
		},
		{
			name:  "tuple destructuring in block let",
			src:   "let main () =\n  let (a, b) = (1, 2)\n  a + b",
			entry: "$main(null)",
			want:  "3",
		},
		{
			name:  "structural equality",
			src:   "let main () = (1, 2) == (1, 2)",
			entry: "$main(null)",
			want:  "true",
		},
		{
			name:  "field access",
			src:   "let main () =\n  let r = { x = 41, y = 1 }\n  r.x + r.y",
			entry: "$main(null)",
			want:  "42",
		},
		{
			name:  "match with guard",
			src:   "let sign n =\n  match n with\n  | 0 -> \"zero\"\n  | x if x > 0 -> \"pos\"\n  | _ -> \"neg\"",
			entry: "$sign(-4)",
			want:  "neg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fmt.Sprintf("%v", run(t, tt.src, tt.entry))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCompileRejectsIllTyped confirms emission is gated behind the type checker.
func TestCompileRejectsIllTyped(t *testing.T) {
	if _, err := Compile("let f x = x + true"); err == nil {
		t.Fatal("expected type error to block compilation")
	}
}

// TestReactiveRuntime exercises the signal graph directly: an effect tracks the
// cells it reads and re-runs when any of them is set.
func TestReactiveRuntime(t *testing.T) {
	driver := `
let captured = 0;
const r = __cell(10);
__effect(() => { captured = __get(r); });
const before = captured;     // effect ran once: 10
__set(r)(99);                // re-runs the effect
const after = captured;      // 99
[before, after]`
	vm := goja.New()
	v, err := vm.RunString(Runtime() + driver)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	got := fmt.Sprintf("%v", v.Export())
	if got != "[10 99]" {
		t.Errorf("reactivity: got %v, want [10 99]", got)
	}
}

const counterSrc = `let counter () =
  let r = __cell 0
  __elem "div" [] [
    __text (fun () -> "count: ${__get r}"),
    __elem "button" [ __on "click" (fun e -> effect { __set r (__get r + 1) }) ] [
      __text (fun () -> "+")
    ]
  ]`

// TestCompileCounter confirms the raw-intrinsic counter compiles cleanly.
func TestCompileCounter(t *testing.T) {
	js, err := Compile(counterSrc)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	for _, want := range []string{"__cell", "__elem", "__on", "(() => {"} {
		if !strings.Contains(js, want) {
			t.Errorf("emitted JS missing %q", want)
		}
	}
}
