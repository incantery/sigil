package lex

import (
	"strings"
	"testing"

	"github.com/incantery/sigil/core/token"
)

// render flattens a token stream into a compact space-separated string for
// golden comparison (positions omitted).
func render(toks []token.Token) string {
	parts := make([]string, len(toks))
	for i, t := range toks {
		parts[i] = t.String()
	}
	return strings.Join(parts, " ")
}

func TestLex(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "single line decl",
			src:  "let x = 1",
			want: "let IDENT(x) = INT(1) EOF",
		},
		{
			name: "two top-level decls separated by NEWLINE",
			src:  "let a = 1\nlet b = 2\n",
			want: "let IDENT(a) = INT(1) NEWLINE let IDENT(b) = INT(2) EOF",
		},
		{
			name: "indented block emits INDENT/NEWLINE/DEDENT",
			src:  "let f x =\n  a\n  b\n",
			want: "let IDENT(f) IDENT(x) = INDENT IDENT(a) NEWLINE IDENT(b) DEDENT EOF",
		},
		{
			name: "dedent then continue outer block",
			src:  "let f x =\n  a\nlet g = 2\n",
			want: "let IDENT(f) IDENT(x) = INDENT IDENT(a) DEDENT NEWLINE let IDENT(g) = INT(2) EOF",
		},
		{
			name: "brackets suspend layout, commas separate",
			src:  "Card [\n  Title \"Echo\",\n  Text \"hi\",\n]\n",
			want: "UIDENT(Card) [ UIDENT(Title) STRING(1 segs) , UIDENT(Text) STRING(1 segs) , ] EOF",
		},
		{
			name: "operators with maximal munch",
			src:  "a |> f && b == c ++ d",
			want: "IDENT(a) |> IDENT(f) && IDENT(b) == IDENT(c) ++ IDENT(d) EOF",
		},
		{
			name: "field access vs float",
			src:  "List.map 1.5 x.field",
			want: "UIDENT(List) . IDENT(map) FLOAT(1.5) IDENT(x) . IDENT(field) EOF",
		},
		{
			name: "arrow, hole, underscore, wildcard ident",
			src:  "fun _ -> __cell _x",
			want: "fun UNDERSCORE -> HOLE(__cell) IDENT(_x) EOF",
		},
		{
			name: "match arms",
			src:  "match t with\n| Home -> \"h\"\n| Away -> \"a\"\n",
			want: "match IDENT(t) with NEWLINE | UIDENT(Home) -> STRING(1 segs) NEWLINE | UIDENT(Away) -> STRING(1 segs) EOF",
		},
		{
			name: "line comments and blank lines skipped",
			src:  "let a = 1 // trailing\n// whole line\n\nlet b = 2\n",
			want: "let IDENT(a) = INT(1) NEWLINE let IDENT(b) = INT(2) EOF",
		},
		{
			name: "keywords vs uident",
			src:  "let rec pub import as type Some None",
			want: "let rec pub import as type UIDENT(Some) UIDENT(None) EOF",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, err := Lex(tt.src)
			if err != nil {
				t.Fatalf("Lex error: %v", err)
			}
			got := render(toks)
			if got != tt.want {
				t.Errorf("token mismatch\n src:  %q\n got:  %s\n want: %s", tt.src, got, tt.want)
			}
		})
	}
}

func TestInterpolationSegments(t *testing.T) {
	toks, err := Lex(`"hello, ${name ()}!"`)
	if err != nil {
		t.Fatalf("Lex error: %v", err)
	}
	if toks[0].Kind != token.STRING {
		t.Fatalf("want STRING, got %v", toks[0].Kind)
	}
	segs := toks[0].Segments
	want := []token.StrSeg{
		{Lit: "hello, "},
		{Expr: "name ()", IsExpr: true},
		{Lit: "!"},
	}
	if len(segs) != len(want) {
		t.Fatalf("segment count: got %d want %d (%+v)", len(segs), len(want), segs)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Errorf("seg %d: got %+v want %+v", i, segs[i], want[i])
		}
	}
}

func TestLexErrors(t *testing.T) {
	cases := []string{
		"let x =\n\ta",   // tab indentation
		"\"unterminated", // unterminated string
		"a & b",          // lone ampersand
		"\"hi ${oops",    // unterminated interpolation
	}
	for _, src := range cases {
		if _, err := Lex(src); err == nil {
			t.Errorf("expected error for %q, got nil", src)
		}
	}
}
