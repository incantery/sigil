package parser

import (
	"strings"
	"testing"
)

// TestParseSyntax covers the v0 surface: curried declarations, optional
// type ascriptions (consumed but not stored), inline vs indented bodies,
// and the relevant error paths.
func TestParseSyntax(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantErr   string // substring; "" means must succeed
		checkArgs []string
	}{
		{
			name: "view no args",
			src: `view Hello =
  text "ok"`,
			checkArgs: []string{"Hello"},
		},
		{
			name: "view one arg",
			src: `view Greet -> who =
  text "hi"`,
			checkArgs: []string{"Greet", "who"},
		},
		{
			name: "view many args",
			src: `view Profile -> name -> age -> bio =
  text "ok"`,
			checkArgs: []string{"Profile", "name", "age", "bio"},
		},
		{
			name: "type annotations are swallowed",
			src: `view P -> name : String -> age : Int =
  text "ok"`,
			checkArgs: []string{"P", "name", "age"},
		},
		{
			name:      "inline body",
			src:       `view Empty = text "nothing"`,
			checkArgs: []string{"Empty"},
		},
		{
			name: "missing equals",
			src: `view Bad name
  text "x"`,
			wantErr: "expected `->` or `=`",
		},
		{
			name: "arrow in invocation",
			src: `view X =
  title -> "x"`,
			wantErr: "unexpected `->`",
		},
		{
			name: "equals after kind on invocation line",
			src: `view X =
  title = "x"`,
			wantErr: "unexpected `=`",
		},
		{
			name: "arg without ident after arrow",
			src: `view X -> =
  text "x"`,
			wantErr: "expected arg name after `->`",
		},
		{
			name: "trailing comment after decl",
			src: `view Hello = // greeting page
  text "ok"`,
			checkArgs: []string{"Hello"},
		},
		{
			name: "stack with flag and kwarg",
			src: `view X =
  stack horizontal gap=1
    text "ok"`,
			checkArgs: []string{"X"},
		},
		{
			name: "negative int kwarg",
			src: `view X =
  stack gap=-3
    text "ok"`,
			checkArgs: []string{"X"},
		},
		{
			name: "duplicate kwarg",
			src: `view X =
  stack gap=1 gap=2
    text "ok"`,
			wantErr: "duplicate kwarg",
		},
		{
			name: "kwarg with no value",
			src: `view X =
  stack gap=`,
			wantErr: "expected value",
		},
		{
			name: "state decl int",
			src: `view C =
  state count = 0
  text count`,
			checkArgs: []string{"C"},
		},
		{
			name: "state decl string",
			src: `view C =
  state name = "alice"
  text name`,
			checkArgs: []string{"C"},
		},
		{
			name: "state decl with type ascription",
			src: `view C =
  state count : Int = 5
  text count`,
			checkArgs: []string{"C"},
		},
		{
			name: "handler with set",
			src: `view C =
  state count = 0
  button "reset" on click { count = 0 }`,
			checkArgs: []string{"C"},
		},
		{
			name: "handler with binop",
			src: `view C =
  state count = 0
  button "+" on click { count = count + 1 }`,
			checkArgs: []string{"C"},
		},
		{
			name: "handler missing brace",
			src: `view C =
  state count = 0
  button "+" on click count = 1`,
			wantErr: "expected `{`",
		},
		{
			name: "handler unclosed",
			src: `view C =
  state count = 0
  button "+" on click { count = 0`,
			wantErr: "expected `}`",
		},
		{
			name: "compound += sugar",
			src: `view C =
  state count = 0
  button "+" on click { count += 1 }`,
			checkArgs: []string{"C"},
		},
		{
			name: "compound -= sugar",
			src: `view C =
  state count = 0
  button "-" on click { count -= 1 }`,
			checkArgs: []string{"C"},
		},
		{
			name: "list literal state",
			src: `view C =
  state items = [0, 1, 2]
  text "hi"`,
			checkArgs: []string{"C"},
		},
		{
			name: "empty list literal",
			src: `view C =
  state items = []
  text "hi"`,
			checkArgs: []string{"C"},
		},
		{
			name: "list literal missing close",
			src: `view C =
  state items = [0, 1`,
			wantErr: "expected `,` or `]`",
		},
		{
			name: "method call statement",
			src: `view C =
  state items = [0]
  button "+" on click { items.append(0) }`,
			checkArgs: []string{"C"},
		},
		{
			name: "method call with cell ref arg",
			src: `view C =
  state items = [0]
  for item in items
    button "x" on click { items.remove(item) }`,
			checkArgs: []string{"C"},
		},
		{
			name: "method call missing parens",
			src: `view C =
  state items = [0]
  button "+" on click { items.append }`,
			wantErr: "expected `(`",
		},
		{
			name: "method call unclosed",
			src: `view C =
  state items = [0]
  button "+" on click { items.append(0 }`,
			wantErr: "expected `,` or `)`",
		},
		{
			name: "unary not toggle",
			src: `view C =
  state expanded = false
  button "t" on click { expanded = !expanded }`,
			checkArgs: []string{"C"},
		},
		{
			name: "input primitive",
			src: `view C =
  state name = ""
  input name placeholder="type something"`,
			checkArgs: []string{"C"},
		},
		{
			name: "component variadic param",
			src: `component my-card -> heading -> *body =
  card`,
		},
		{
			name: "splice body line",
			src: `component my-card -> *body =
  card
    *body`,
		},
		{
			name: "splice with extra args rejected",
			src: `component my-card -> *body =
  card
    *body extra`,
			wantErr: "splice line takes no arguments",
		},
		{
			name: "variadic on view rejected",
			src: `view X -> *body =
  text "x"`,
			wantErr: "variadic params are only valid on component decls",
		},
		{
			name: "splice in argument position rejected",
			src: `view X =
  card *body`,
			wantErr: "argument-position spread is not supported",
		},
		{
			name: "structured list state with field decls",
			src: `view T =
  state items = []
    label : String
    done : Bool = false
  text "ok"`,
			checkArgs: []string{"T"},
		},
		{
			name: "dotted ident in expression",
			src: `view T =
  state items = []
    done : Bool = false
  for item in items
    if item.done
      text "yes"`,
			checkArgs: []string{"T"},
		},
		{
			name: "field assignment in handler",
			src: `view T =
  state items = []
    done : Bool = false
  for item in items
    button "x" on click { item.done = true }`,
			checkArgs: []string{"T"},
		},
		{
			name: "field decl missing type",
			src: `view T =
  state items = []
    label
  text "ok"`,
			wantErr: "expected `:` after field name",
		},
		{
			name: "deeply nested dotted ref in expression",
			src: `view T =
  state p = 0
  text p.stats.hp.value`,
			checkArgs: []string{"T"},
		},
		{
			name: "nested field assignment in handler",
			src: `view T =
  state p = 0
  button "x" on click { p.stats.hp = 5 }`,
			checkArgs: []string{"T"},
		},
		{
			name: "nested field compound assignment",
			src: `view T =
  state p = 0
  button "x" on click { p.stats.hp += 1 }`,
			checkArgs: []string{"T"},
		},
		{
			name: "method call still wins over field chain",
			src: `view T =
  state items = [0]
  button "x" on click { items.append(0) }`,
			checkArgs: []string{"T"},
		},
		{
			name: "query no args",
			src: `query Ping = Bool
view App = text "ok"`,
		},
		{
			name: "query with typed args",
			src: `query GetPokemon -> id : Int = Pokemon
view App = text "ok"`,
		},
		{
			name: "command with typed args",
			src: `command CatchPokemon -> id : Int -> name : String = Pokemon
view App = text "ok"`,
		},
		{
			name: "query untyped arg rejected",
			src: `query Bad -> id = Pokemon
view App = text "ok"`,
			wantErr: "must be typed",
		},
		{
			name: "query missing return type",
			src: `query Bad -> id : Int
view App = text "ok"`,
			wantErr: "expected `=`",
		},
		{
			name: "query no name",
			src: `query
view App = text "ok"`,
			wantErr: "expected query name",
		},
		{
			name: "op call expression in assignment",
			src: `query GetCount = Int
view App =
  state n = 0
  button "load" on click { n = GetCount() }`,
		},
		{
			name: "op call expression with args",
			src: `query GetThing -> id : Int = Int
view App =
  state n = 0
  button "load" on click { n = GetThing(1) }`,
		},
		{
			name: "op call statement (fire and forget)",
			src: `command Refresh = Bool
view App =
  button "refresh" on click { Refresh() }`,
		},
		{
			name: "import without alias",
			src: `import github.com/seth/pokedex/types
view App =
  text "ok"`,
		},
		{
			name: "import with alias",
			src: `import github.com/seth/pokedex/types as t
view App =
  text "ok"`,
		},
		{
			name:    "import without path",
			src:     `import`,
			wantErr: "expected module path",
		},
		{
			name: "import with garbage after path",
			src: `import github.com/seth/pokedex types
view App = text "x"`,
			wantErr: "unexpected content",
		},
		{
			name: "import as without alias",
			src: `import github.com/seth/pokedex as
view App = text "x"`,
			wantErr: "expected alias",
		},
		{
			name: "icons decl with web target",
			src: `icons Lucide =
  web "./icons/web"
view App = text "ok"`,
		},
		{
			name: "icons decl multi-target",
			src: `icons Brand =
  web "./icons/web/brand"
  ios "./icons/ios/brand"
view App = text "ok"`,
		},
		{
			name:    "icons decl missing name",
			src:     `icons =`,
			wantErr: "expected icon-set name",
		},
		{
			name:    "icons decl missing =",
			src:     `icons Foo`,
			wantErr: "expected `=`",
		},
		{
			name: "icons decl body unquoted path",
			src: `icons Foo =
  web ./icons/web`,
			wantErr: "expected quoted folder path",
		},
		{
			name: "backend decl simple",
			src: `backend Api =
  url "http://localhost:8080"
  auth none
view App = text "ok"`,
		},
		{
			name: "backend decl with bearer + token path",
			src: `session Auth =
  state token : String?

backend Api =
  url "https://example.com"
  auth bearer
  token from Auth.token
view App = text "ok"`,
		},
		{
			name: "backend decl same-origin url",
			src: `backend Api =
  url same-origin
  auth none
view App = text "ok"`,
		},
		{
			name: "backend decl url neither string nor same-origin",
			src: `backend Api =
  url localhost`,
			wantErr: "expected quoted URL or `same-origin`",
		},
		{
			name:    "backend decl missing name",
			src:     `backend =`,
			wantErr: "expected backend name",
		},
		{
			name: "backend decl unknown binding",
			src: `backend Api =
  whatever "x"`,
			wantErr: "unknown backend binding",
		},
		{
			name: "session decl with state",
			src: `session Auth =
  state token : String?
view App = text "ok"`,
		},
		{
			name: "command with invalidates",
			src: `query GetTeamSize = Int
command CatchPokemon -> id : Int = Bool invalidates GetTeamSize
view App = text "ok"`,
		},
		{
			name: "command with multi invalidates",
			src: `query GetA = Int
query GetB = Int
command Foo = Bool invalidates GetA GetB
view App = text "ok"`,
		},
		{
			name:    "query rejects invalidates",
			src:     `query Foo = Bool invalidates GetX`,
			wantErr: "only `command`",
		},
		{
			name:    "invalidates with no names",
			src:     `command Foo = Bool invalidates`,
			wantErr: "at least one query",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.src)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %q", tc.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.checkArgs == nil {
				return
			}
			if got.Kind != "view" {
				t.Fatalf("want view, got %q", got.Kind)
			}
			if len(got.Args) != len(tc.checkArgs) {
				t.Fatalf("want %d args, got %d (%v)",
					len(tc.checkArgs), len(got.Args), got.Args)
			}
			for i, want := range tc.checkArgs {
				if got.Args[i].String != want {
					t.Fatalf("arg %d: want %q, got %q", i, want, got.Args[i].String)
				}
			}
		})
	}
}

// TestStoryDecl covers the `story "<name>" =` declaration: it shares
// the quoted-name shape with `test` but its body is an ordinary view
// body (component invocations, optional state).
func TestStoryDecl(t *testing.T) {
	t.Run("indented body", func(t *testing.T) {
		got, err := Parse(`story "Empty card" =
  card
    title "Nothing here"
`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Kind != "story" {
			t.Fatalf("want story, got %q", got.Kind)
		}
		if len(got.Args) != 1 || got.Args[0].String != "Empty card" {
			t.Fatalf("want name arg %q, got %v", "Empty card", got.Args)
		}
		if len(got.Children) != 1 || got.Children[0].Kind != "card" {
			t.Fatalf("want one card child, got %v", got.Children)
		}
	})

	t.Run("inline body", func(t *testing.T) {
		got, err := Parse(`story "Hello" = text "hi"`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Children) != 1 || got.Children[0].Kind != "text" {
			t.Fatalf("want inline text child, got %v", got.Children)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		_, err := Parse(`story =
  card`)
		if err == nil || !strings.Contains(err.Error(), "expected quoted story name") {
			t.Fatalf("want quoted-name error, got %v", err)
		}
	})

	t.Run("missing equals", func(t *testing.T) {
		_, err := Parse(`story "x"
  card`)
		if err == nil || !strings.Contains(err.Error(), "expected `=` after story name") {
			t.Fatalf("want missing-= error, got %v", err)
		}
	})
}
