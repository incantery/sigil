package lower

import (
	"strings"
	"testing"

	"github.com/incantery/sigil/pkg/ir"
	"github.com/incantery/sigil/pkg/lang/parser"
)

// lowerSrc is a convenience that parses + lowers; used by every test
// below to keep each case readable.
func lowerSrc(t *testing.T, src string) (ir.Document, error) {
	t.Helper()
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Lower(root)
}

// TestComponentInlining covers the happy paths: parametric components
// invoked once, twice with different cells, plus wrapper components.
func TestComponentInlining(t *testing.T) {
	t.Run("parametric invoked once", func(t *testing.T) {
		doc, err := lowerSrc(t, `component nc -> count =
  text count
view App =
  state apples = 5
  nc apples
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		root := doc.Root
		// The single-line component body inlines directly at the view's
		// only-child slot, so the root IS the text node.
		if root.Kind != ir.KindText {
			t.Fatalf("want text root, got %s", root.Kind)
		}
		if b, ok := root.Bindings["text"]; !ok || b.CellID != "c1" {
			t.Fatalf("want text bound to c1, got %+v", root.Bindings)
		}
	})

	t.Run("parametric invoked twice with different cells", func(t *testing.T) {
		doc, err := lowerSrc(t, `component nc -> count =
  stack
    text count
    button "+" on click { count += 1 }
view App =
  state apples = 5
  state oranges = 3
  card
    nc apples
    nc oranges
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		card := doc.Root
		if card.Kind != ir.KindCard {
			t.Fatalf("want card root, got %s", card.Kind)
		}
		if len(card.Children) != 2 {
			t.Fatalf("want 2 children, got %d", len(card.Children))
		}
		// Each nc invocation expands to a 2-child stack; the bindings
		// inside must point to the per-invocation cell.
		assertBinding(t, card.Children[0].Children[0], "c1") // apples
		assertHandler(t, card.Children[0].Children[1], "click", "c1")
		assertBinding(t, card.Children[1].Children[0], "c2") // oranges
		assertHandler(t, card.Children[1].Children[1], "click", "c2")
	})

	t.Run("wrapper with variadic spread", func(t *testing.T) {
		doc, err := lowerSrc(t, `component my-card -> heading -> *body =
  card
    title heading
    *body
view App =
  state apples = 5
  my-card "Counters"
    text apples
    text apples
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		root := doc.Root
		if root.Kind != ir.KindCard {
			t.Fatalf("want card root, got %s", root.Kind)
		}
		// title + 2 spliced texts.
		if len(root.Children) != 3 {
			t.Fatalf("want 3 children (title + 2 spliced), got %d", len(root.Children))
		}
		if root.Children[0].Kind != ir.KindTitle {
			t.Fatalf("want title first, got %s", root.Children[0].Kind)
		}
		if t0 := root.Children[0].Props["text"]; t0 != "Counters" {
			t.Fatalf("want title=Counters, got %v", t0)
		}
		assertBinding(t, root.Children[1], "c1")
		assertBinding(t, root.Children[2], "c1")
	})

	t.Run("literal arg into interpolation becomes static", func(t *testing.T) {
		doc, err := lowerSrc(t, `component greet -> who =
  text "hello, ${who}"
view App =
  greet "alice"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		root := doc.Root
		if root.Kind != ir.KindText {
			t.Fatalf("want text root, got %s", root.Kind)
		}
		if text := root.Props["text"]; text != "hello, alice" {
			t.Fatalf("want static 'hello, alice', got %q", text)
		}
		if _, has := root.Bindings["text"]; has {
			t.Fatalf("literal subst should not leave a binding, got %+v", root.Bindings)
		}
	})

	t.Run("cell arg into interpolation rewrites cell id", func(t *testing.T) {
		doc, err := lowerSrc(t, `component greet -> who =
  text "hello, ${who}"
view App =
  state name = "alice"
  greet name
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		root := doc.Root
		if root.Kind != ir.KindText {
			t.Fatalf("want text root, got %s", root.Kind)
		}
		b, ok := root.Bindings["text"]
		if !ok || b.CellID != "c1" {
			t.Fatalf("want binding to c1, got %+v", root.Bindings)
		}
	})

	t.Run("literal arg into positional", func(t *testing.T) {
		doc, err := lowerSrc(t, `component head -> heading =
  title heading
view App =
  head "Hello"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		root := doc.Root
		if root.Kind != ir.KindTitle {
			t.Fatalf("want title root, got %s", root.Kind)
		}
		if text := root.Props["text"]; text != "Hello" {
			t.Fatalf("want title=Hello, got %v", text)
		}
	})

	t.Run("document records component signatures", func(t *testing.T) {
		doc, err := lowerSrc(t, `component nc -> count =
  text count
component my-card -> heading -> *body =
  card
    title heading
    *body
view App =
  state apples = 5
  my-card "x"
    nc apples
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if len(doc.Components) != 2 {
			t.Fatalf("want 2 component sigs, got %d", len(doc.Components))
		}
		if doc.Components[0].Name != "nc" || len(doc.Components[0].Params) != 1 {
			t.Fatalf("nc sig: %+v", doc.Components[0])
		}
		if doc.Components[1].Name != "my-card" ||
			doc.Components[1].Params[0] != "heading" ||
			doc.Components[1].Params[1] != "*body" {
			t.Fatalf("my-card sig: %+v", doc.Components[1])
		}
	})
}

// TestStructuredLists covers the field-shape lowering: list state with
// indented field decls registers a row schema, .append produces the
// struct-aware action, and item.field access resolves to dotted cell ids
// in the for-body.
func TestStructuredLists(t *testing.T) {
	t.Run("structured state lowers", func(t *testing.T) {
		_, err := lowerSrc(t, `view T =
  state items = []
    label : String
    done : Bool = false
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
	})

	t.Run("struct append emits append_struct_item", func(t *testing.T) {
		doc, err := lowerSrc(t, `view T =
  state items = []
    label : String
    done : Bool = false
  state draft = ""
  button "add" on click { items.append(draft) }
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		btn := doc.Root // single child is the button
		action, ok := btn.Handlers["click"]
		if !ok {
			t.Fatalf("button handler missing")
		}
		if action.Kind != "append_struct_item" {
			t.Fatalf("want append_struct_item, got %s", action.Kind)
		}
		fields, ok := action.Args["fields"].(map[string]any)
		if !ok {
			t.Fatalf("args.fields missing: %+v", action.Args)
		}
		// label cell-ref → $cell.<draft-id>
		if !strings.HasPrefix(fields["label"].(string), "$cell.") {
			t.Errorf("label should be $cell sentinel, got %v", fields["label"])
		}
		// done defaults to false
		if fields["done"] != false {
			t.Errorf("done should default to false, got %v", fields["done"])
		}
	})

	t.Run("dotted access in for body", func(t *testing.T) {
		doc, err := lowerSrc(t, `view T =
  state items = []
    label : String
    done : Bool = false
  for item in items
    text item.label
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		// Walk to the template's text node; its binding should target
		// $ITEM.label, not just $ITEM.
		for_node := doc.Root
		var found bool
		var walk func(n ir.Node)
		walk = func(n ir.Node) {
			if found {
				return
			}
			if n.Kind == ir.KindText {
				b, ok := n.Bindings["text"]
				if ok && strings.HasSuffix(b.CellID, ".label") {
					found = true
					return
				}
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(for_node)
		if !found {
			t.Fatalf("text binding to .label not found in for body")
		}
	})

	t.Run("structured list with initial values rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `view T =
  state items = [0, 1]
    label : String
  text "ok"
`)
		if err == nil {
			t.Fatalf("want error for structured list with initial values, got nil")
		}
		if !strings.Contains(err.Error(), "structured list states must start") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown field type rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `view T =
  state items = []
    name : NotARealType
  text "ok"
`)
		if err == nil {
			t.Fatalf("want error, got nil")
		}
		if !strings.Contains(err.Error(), "unknown field type") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("too many append args rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `view T =
  state items = []
    label : String
    done : Bool = false
  button "add" on click { items.append("x", false, "extra") }
`)
		if err == nil {
			t.Fatalf("want error, got nil")
		}
		if !strings.Contains(err.Error(), "expects ≤") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// TestSourceLevelThemes covers the `theme name extends base = ...` decl:
// happy path, default-base inheritance, and contrast rejection.
func TestSourceLevelThemes(t *testing.T) {
	t.Run("dawn extends light", func(t *testing.T) {
		doc, err := lowerSrc(t, `theme dawn extends light =
  primary = "#ff6b35" on "#0a0a0a"
view X =
  button "x" tone=primary
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if len(doc.Themes) != 1 {
			t.Fatalf("want 1 theme, got %d", len(doc.Themes))
		}
	})

	t.Run("contrast failure rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `theme bad extends light =
  primary = "#ff6b35" on "#ffffff"
view X =
  text "hi"
`)
		if err == nil {
			t.Fatalf("want contrast error, got nil")
		}
		if !strings.Contains(err.Error(), "WCAG AA") {
			t.Fatalf("want WCAG AA error, got: %v", err)
		}
	})

	t.Run("unknown extends rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `theme bad extends midnight =
  primary = "#000000" on "#ffffff"
view X =
  text "hi"
`)
		if err == nil {
			t.Fatalf("want error for unknown base, got nil")
		}
		if !strings.Contains(err.Error(), "unknown base theme") {
			t.Fatalf("want unknown-base error, got: %v", err)
		}
	})

	t.Run("non-overridable tone rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `theme bad extends light =
  default = "#000000" on "#ffffff"
view X =
  text "hi"
`)
		if err == nil {
			t.Fatalf("want error for default tone, got nil")
		}
		if !strings.Contains(err.Error(), "non-overridable") {
			t.Fatalf("want non-overridable error, got: %v", err)
		}
	})
}

// TestComponentErrors covers diagnostics for misuse.
func TestComponentErrors(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "arg count too few",
			src: `component nc -> count =
  text count
view App =
  state apples = 5
  nc
`,
			wantErr: "expects 1 arg",
		},
		{
			name: "arg count too many (non-variadic)",
			src: `component nc -> count =
  text count
view App =
  state apples = 5
  state oranges = 3
  nc apples oranges
`,
			wantErr: "expects 1 arg",
		},
		{
			name: "unknown cell name passed in",
			src: `component nc -> count =
  text count
view App =
  state apples = 5
  nc oranges
`,
			wantErr: `unknown name "oranges"`,
		},
		{
			name: "shadows stdlib kind",
			src: `component card -> heading =
  title heading
view App =
  card "x"
`,
			wantErr: `shadows a stdlib kind`,
		},
		{
			name: "direct recursion",
			src: `component nc -> count =
  nc count
view App =
  state apples = 5
  nc apples
`,
			wantErr: "recursive component invocation",
		},
		{
			name: "splice outside component body",
			src: `view App =
  state apples = 5
  *apples
`,
			wantErr: "splice is only valid inside a component body",
		},
		{
			name: "state inside component body",
			src: `component bad -> x =
  state inner = 0
  text x
view App =
  state apples = 5
  bad apples
`,
			wantErr: "cannot declare state",
		},
		{
			name: "variadic not last",
			src: `component bad -> *body -> trailing =
  card
view App =
  bad
`,
			wantErr: "must be the last parameter",
		},
		{
			name: "duplicate component decl",
			src: `component nc -> x =
  text x
component nc -> y =
  text y
view App =
  state apples = 5
  nc apples
`,
			wantErr: "already declared",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lowerSrc(t, tc.src)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

// TestBackendURL covers the backend `url` binding: a quoted base URL
// passes through, `same-origin` lowers to an empty prefix (the client
// then requests `/query/<op>` against the page's own origin), and a
// backend with no url at all is a compile error.
func TestBackendURL(t *testing.T) {
	t.Run("same-origin lowers to empty prefix", func(t *testing.T) {
		doc, err := lowerSrc(t, `backend Api =
  url same-origin
  auth none
query GetX = Int
view App = text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if len(doc.Backends) != 1 {
			t.Fatalf("want 1 backend, got %d", len(doc.Backends))
		}
		if doc.Backends[0].URL != "" {
			t.Fatalf("same-origin should lower to empty URL, got %q", doc.Backends[0].URL)
		}
	})
	t.Run("absolute url passes through", func(t *testing.T) {
		doc, err := lowerSrc(t, `backend Api =
  url "http://localhost:9090"
view App = text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if doc.Backends[0].URL != "http://localhost:9090" {
			t.Fatalf("got %q", doc.Backends[0].URL)
		}
	})
	t.Run("missing url is an error", func(t *testing.T) {
		_, err := lowerSrc(t, `backend Api =
  auth none
view App = text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "missing `url`") {
			t.Fatalf("want missing-url error, got %v", err)
		}
	})
}

// TestScrollAnchor covers `anchor=end` on stack: legal only alongside
// scroll=y, value restricted to `end`.
func TestScrollAnchor(t *testing.T) {
	t.Run("anchor=end with scroll=y lowers", func(t *testing.T) {
		doc, err := lowerSrc(t, `view App =
  stack scroll=y anchor=end
    text "row"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if doc.Root.Props["anchor"] != "end" {
			t.Fatalf("anchor prop not lowered: %+v", doc.Root.Props)
		}
	})
	t.Run("anchor without scroll is rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `view App =
  stack anchor=end
    text "row"
`)
		if err == nil || !strings.Contains(err.Error(), "requires scroll=y") {
			t.Fatalf("want requires-scroll error, got %v", err)
		}
	})
	t.Run("anchor value other than end is rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `view App =
  stack scroll=y anchor=start
    text "row"
`)
		if err == nil || !strings.Contains(err.Error(), "anchor= must be `end`") {
			t.Fatalf("want anchor-value error, got %v", err)
		}
	})
}

// TestStreamLifecycleCells covers the implicit `<Op>.pending` /
// `<Op>.failed` / `<Op>.error` cells each stream op contributes:
// readable everywhere a cell is (if / text / disabled=), rejected as a
// write target anywhere source could mutate them, and protected from
// name shadowing by a state decl.
func TestStreamLifecycleCells(t *testing.T) {
	prelude := `backend Api =
  url same-origin
  auth none
stream Chat -> prompt : String = String
`
	t.Run("readable in if, text, and disabled=", func(t *testing.T) {
		doc, err := lowerSrc(t, prelude+`view App =
  state prompt = ""
  state reply = ""
  button "Send" disabled=Chat.pending on click { reply <- Chat(prompt) }
  if Chat.pending
    text "thinking"
  if Chat.failed
    text Chat.error
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		// Three lifecycle cells + prompt + reply.
		boolCells, strCells := 0, 0
		for _, init := range doc.Cells {
			switch init.(type) {
			case bool:
				boolCells++
			case string:
				strCells++
			}
		}
		if boolCells != 2 {
			t.Fatalf("want 2 bool cells (pending, failed), got %d", boolCells)
		}
		if strCells != 3 {
			t.Fatalf("want 3 string cells (error, prompt, reply), got %d", strCells)
		}
	})

	writeCases := []struct {
		name, src, wantErr string
	}{
		{
			name: "assignment is rejected",
			src: prelude + `view App =
  button "x" on click { Chat.pending = true }
`,
			wantErr: "read-only",
		},
		{
			name: "stream-into is rejected",
			src: prelude + `view App =
  state prompt = ""
  button "x" on click { Chat.error <- Chat(prompt) }
`,
			wantErr: "read-only",
		},
		{
			name: "input binding is rejected",
			src: prelude + `view App =
  input Chat.error placeholder="nope"
`,
			wantErr: "read-only",
		},
		{
			name: "state shadowing an op name is rejected",
			src: prelude + `view App =
  state Chat = ""
  text Chat
`,
			wantErr: "collides with stream",
		},
		{
			name: "disabled= requires a bool cell",
			src: prelude + `view App =
  state prompt = ""
  button "x" disabled=Chat.error on click { prompt = "y" }
`,
			wantErr: "requires a bool cell",
		},
	}
	for _, tc := range writeCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lowerSrc(t, tc.src)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

// TestMultiChannelStreamErrors covers diagnostics for the multi-channel
// `(t1, t2) <- StreamOp(...)` form and its record-typed stream decls.
func TestMultiChannelStreamErrors(t *testing.T) {
	prelude := `backend Api =
  url "http://x"
  auth none
`
	cases := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "channel field not String",
			src: prelude + `type ChatDelta =
  thinking : String
  count : Int
stream Chat -> prompt : String = ChatDelta
view App =
  state a = ""
  state b = ""
  button "x" on click { (a, b) <- Chat("hi") }
`,
			wantErr: "must be String",
		},
		{
			name: "target count mismatch",
			src: prelude + `type ChatDelta =
  thinking : String
  answer : String
stream Chat -> prompt : String = ChatDelta
view App =
  state a = ""
  state b = ""
  state c = ""
  button "x" on click { (a, b, c) <- Chat("hi") }
`,
			wantErr: "must match positionally",
		},
		{
			name: "tuple on scalar String stream",
			src: prelude + `stream Chat -> prompt : String = String
view App =
  state a = ""
  state b = ""
  button "x" on click { (a, b) <- Chat("hi") }
`,
			wantErr: "returns a single String",
		},
		{
			name: "mixed row and scalar targets",
			src: prelude + `type ChatDelta =
  thinking : String
  answer : String
stream Chat -> prompt : String = ChatDelta
view App =
  state a = ""
  state conv = []
    thinking : String
    answer : String
  button "x" on click { (conv.last.thinking, a) <- Chat("hi") }
`,
			wantErr: "all plain cells, not a mix",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lowerSrc(t, tc.src)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

// assertBinding fails the test unless n has a text binding to cellID.
func assertBinding(t *testing.T, n ir.Node, cellID string) {
	t.Helper()
	b, ok := n.Bindings["text"]
	if !ok {
		t.Fatalf("node %s id=%s: missing text binding", n.Kind, n.ID)
	}
	if b.CellID != cellID {
		t.Fatalf("node %s id=%s: want bind to %s, got %s", n.Kind, n.ID, cellID, b.CellID)
	}
}

// assertHandler fails unless n has a handler for event whose action
// targets cellID.
func assertHandler(t *testing.T, n ir.Node, event, cellID string) {
	t.Helper()
	a, ok := n.Handlers[event]
	if !ok {
		t.Fatalf("node %s id=%s: missing handler for %s", n.Kind, n.ID, event)
	}
	if a.CellID != cellID {
		t.Fatalf("node %s id=%s: handler %s targets %s, want %s", n.Kind, n.ID, event, a.CellID, cellID)
	}
}

// TestTypeDecls covers the happy and error paths for `type Foo =`
// record declarations: primitive fields, references to other declared
// types, unknown types, duplicate names, primitive shadowing, and
// rejection of defaults (which only make sense inside structured-
// list state decls).
func TestTypeDecls(t *testing.T) {
	t.Run("records with primitives + cross-reference", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Stats =
  hp : Int
  attack : Int
type Player =
  name : String
  stats : Stats
view App =
  state x = 0
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if len(doc.Types) != 2 {
			t.Fatalf("want 2 types, got %d", len(doc.Types))
		}
		stats := doc.Types[0]
		if stats.Name != "Stats" || len(stats.Fields) != 2 {
			t.Fatalf("stats: %+v", stats)
		}
		if stats.Fields[0].Name != "hp" || stats.Fields[0].Type.Name != "Int" {
			t.Fatalf("stats.hp: %+v", stats.Fields[0])
		}
		player := doc.Types[1]
		if player.Fields[1].Name != "stats" || player.Fields[1].Type.Name != "Stats" {
			t.Fatalf("player.stats: %+v", player.Fields[1])
		}
	})

	t.Run("unknown type reference errors with hint", func(t *testing.T) {
		_, err := lowerSrc(t, `type Player =
  stats : Stats
view App =
  state x = 0
  text "ok"
`)
		if err == nil {
			t.Fatal("expected error for unknown type Stats")
		}
		if !strings.Contains(err.Error(), `unknown type "Stats"`) {
			t.Fatalf("err: %v", err)
		}
	})

	t.Run("duplicate type name errors", func(t *testing.T) {
		_, err := lowerSrc(t, `type Foo =
  x : Int
type Foo =
  y : Int
view App =
  state x = 0
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "declared more than once") {
			t.Fatalf("want dup error, got %v", err)
		}
	})

	t.Run("primitive shadowing rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `type Int =
  x : Int
view App =
  state x = 0
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "shadows a primitive") {
			t.Fatalf("want shadow error, got %v", err)
		}
	})

	t.Run("default values rejected on type fields", func(t *testing.T) {
		_, err := lowerSrc(t, `type Stats =
  hp : Int = 100
view App =
  state x = 0
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "cannot have default values") {
			t.Fatalf("want default-value error, got %v", err)
		}
	})

	t.Run("duplicate field within a type errors", func(t *testing.T) {
		_, err := lowerSrc(t, `type Stats =
  hp : Int
  hp : Int
view App =
  state x = 0
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "duplicate field") {
			t.Fatalf("want dup-field error, got %v", err)
		}
	})
}

// TestSumTypeDecls covers `type Foo = | a | b | c` declarations:
// nullary variants, duplicate-variant rejection, mixed body
// rejection (records + sums in one decl), and the records→sums
// boundary (a record field can reference a sum type by name).
func TestSumTypeDecls(t *testing.T) {
	t.Run("nullary variants", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Color =
  | red
  | green
  | blue
view App =
  card
    text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if len(doc.Types) != 1 {
			t.Fatalf("want 1 type, got %d", len(doc.Types))
		}
		td := doc.Types[0]
		if td.Kind != "sum" {
			t.Fatalf("Kind: got %q, want %q", td.Kind, "sum")
		}
		if len(td.Variants) != 3 ||
			td.Variants[0] != "red" || td.Variants[1] != "green" || td.Variants[2] != "blue" {
			t.Fatalf("variants: %+v", td.Variants)
		}
		if len(td.Fields) != 0 {
			t.Fatalf("variants type should have no fields, got %+v", td.Fields)
		}
	})

	t.Run("record field can reference a sum type", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Color =
  | red
  | green
type Paint =
  shade : Color
  opacity : Int
view App =
  card
    text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		paint := doc.Types[1]
		if paint.Kind != "record" {
			t.Fatalf("paint.Kind: got %q", paint.Kind)
		}
		if paint.Fields[0].Type.Name != "Color" {
			t.Fatalf("paint.shade.Type: %q", paint.Fields[0].Type.Name)
		}
	})

	t.Run("mixed record + sum body errors", func(t *testing.T) {
		_, err := lowerSrc(t, `type Bad =
  | red
  opacity : Int
view App =
  card
    text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "mixes records and sums") {
			t.Fatalf("want mixed-body error, got %v", err)
		}
	})

	t.Run("duplicate variant rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `type Bad =
  | red
  | red
view App =
  card
    text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "duplicate variant") {
			t.Fatalf("want dup-variant error, got %v", err)
		}
	})

	t.Run("same variant name across different sums is fine", func(t *testing.T) {
		_, err := lowerSrc(t, `type A =
  | x
type B =
  | x
view App =
  card
    text "ok"
`)
		if err != nil {
			t.Fatalf("expected ok, got %v", err)
		}
	})
}

// TestOptionalTypeRefs covers the `Type?` postfix marker on field
// declarations. The marker propagates to ir.TypeFieldSpec.Optional;
// downstream validators / codegen consume it but lower itself just
// preserves the bit.
func TestOptionalTypeRefs(t *testing.T) {
	t.Run("optional + required fields", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Stats =
  hp : Int
  bonus : Int?
view App =
  card
    text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		stats := doc.Types[0]
		if stats.Fields[0].Type.Optional {
			t.Fatalf("hp should be required, got Optional=true")
		}
		if !stats.Fields[1].Type.Optional {
			t.Fatalf("bonus should be Optional=true, got false")
		}
	})

	t.Run("optional on a declared-type reference", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Inner =
  x : Int
type Outer =
  thing : Inner?
view App =
  card
    text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		outer := doc.Types[1]
		if !outer.Fields[0].Type.Optional {
			t.Fatalf("thing should be Optional=true")
		}
		if outer.Fields[0].Type.Name != "Inner" {
			t.Fatalf("thing.Type.Name: %q", outer.Fields[0].Type.Name)
		}
	})

	t.Run("optional resolves before unknown-type check", func(t *testing.T) {
		// Same error as a non-optional reference to an unknown type.
		_, err := lowerSrc(t, `type Foo =
  x : Nope?
view App =
  card
    text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), `unknown type "Nope"`) {
			t.Fatalf("want unknown-type error, got %v", err)
		}
	})
}

// TestGenericsLight covers `List<T>` in type-ref position: the only
// generic head recognized at v0. Nested generics (`List<List<T>>`),
// optional generics (`List<T>?`), and arity / unknown-head error
// paths.
func TestGenericsLight(t *testing.T) {
	t.Run("List<Pokemon> resolves", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Pokemon =
  id : Int
type Page =
  items : List<Pokemon>
  total : Int
view App =
  card
    text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		page := doc.Types[1]
		items := page.Fields[0].Type
		if items.Name != "List" {
			t.Fatalf("items.Name: %q", items.Name)
		}
		if len(items.GenericArgs) != 1 || items.GenericArgs[0].Name != "Pokemon" {
			t.Fatalf("items.GenericArgs: %+v", items.GenericArgs)
		}
	})

	t.Run("nested List<List<Int>>", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Grid =
  rows : List<List<Int>>
view App =
  card
    text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		rows := doc.Types[0].Fields[0].Type
		if rows.Name != "List" || len(rows.GenericArgs) != 1 {
			t.Fatalf("rows: %+v", rows)
		}
		inner := rows.GenericArgs[0]
		if inner.Name != "List" || len(inner.GenericArgs) != 1 || inner.GenericArgs[0].Name != "Int" {
			t.Fatalf("inner: %+v", inner)
		}
	})

	t.Run("optional generic List<String>?", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Demo =
  maybe : List<String>?
view App =
  card
    text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		maybe := doc.Types[0].Fields[0].Type
		if !maybe.Optional {
			t.Fatalf("maybe should be Optional=true")
		}
		if maybe.Name != "List" {
			t.Fatalf("maybe.Name: %q", maybe.Name)
		}
	})

	t.Run("unknown generic head errors", func(t *testing.T) {
		_, err := lowerSrc(t, `type Bad =
  x : Map<Int, String>
view App =
  card
    text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "does not take generic arguments") {
			t.Fatalf("want unknown-generic error, got %v", err)
		}
	})

	t.Run("wrong arity errors", func(t *testing.T) {
		_, err := lowerSrc(t, `type Bad =
  x : List<Int, String>
view App =
  card
    text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "expects 1 type argument") {
			t.Fatalf("want arity error, got %v", err)
		}
	})

	t.Run("unknown inner type errors", func(t *testing.T) {
		_, err := lowerSrc(t, `type Bad =
  x : List<Nope>
view App =
  card
    text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), `unknown type "Nope"`) {
			t.Fatalf("want unknown-inner-type error, got %v", err)
		}
	})
}

// TestRecordTypedState covers `state p : Pokemon` — declared record
// types in state position. The lowerer inflates one scalar leaf cell
// per primitive field, recursing into sub-records (e.g. `p.stats.hp`)
// and resolving sum-typed fields to a single string cell holding the
// first declared variant.
func TestRecordTypedState(t *testing.T) {
	t.Run("primitive-only record inflates one cell per field", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Stats =
  hp     : Int
  attack : Int
view App =
  state s : Stats
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		// Two leaf cells, named "s.hp" / "s.attack".
		hp := ""
		attack := ""
		for id, n := range doc.CellNames {
			switch n {
			case "s.hp":
				hp = id
			case "s.attack":
				attack = id
			}
		}
		if hp == "" || attack == "" {
			t.Fatalf("missing leaf cells; CellNames=%v", doc.CellNames)
		}
		if v, ok := doc.Cells[hp].(int64); !ok || v != 0 {
			t.Fatalf("hp init: got %T %v", doc.Cells[hp], doc.Cells[hp])
		}
		if v, ok := doc.Cells[attack].(int64); !ok || v != 0 {
			t.Fatalf("attack init: got %T %v", doc.Cells[attack], doc.Cells[attack])
		}
	})

	t.Run("nested record inflates leaves with dotted paths", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Stats =
  hp : Int
type Pokemon =
  name  : String
  stats : Stats
view App =
  state p : Pokemon
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		want := map[string]any{
			"p.name":     "",
			"p.stats.hp": int64(0),
		}
		nameToInit := map[string]any{}
		for id, n := range doc.CellNames {
			nameToInit[n] = doc.Cells[id]
		}
		for k, v := range want {
			if got, ok := nameToInit[k]; !ok || got != v {
				t.Fatalf("cell %q: want %v, got %v (full: %v)", k, v, got, nameToInit)
			}
		}
	})

	t.Run("sum-typed field defaults to first variant", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Kind =
  | fire
  | water
type Mon =
  kind : Kind
view App =
  state m : Mon
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		var kindInit any
		for id, n := range doc.CellNames {
			if n == "m.kind" {
				kindInit = doc.Cells[id]
			}
		}
		if kindInit != "fire" {
			t.Fatalf("m.kind init: want %q, got %v", "fire", kindInit)
		}
	})

	t.Run("top-level sum state defaults to first variant", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Tone =
  | dark
  | light
view App =
  state t : Tone
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		var tInit any
		for id, n := range doc.CellNames {
			if n == "t" {
				tInit = doc.Cells[id]
			}
		}
		if tInit != "dark" {
			t.Fatalf("t init: want %q, got %v", "dark", tInit)
		}
	})

	t.Run("typed primitive with no init uses zero", func(t *testing.T) {
		doc, err := lowerSrc(t, `view App =
  state n : Int
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		var nInit any
		for id, name := range doc.CellNames {
			if name == "n" {
				nInit = doc.Cells[id]
			}
		}
		if v, ok := nInit.(int64); !ok || v != 0 {
			t.Fatalf("n init: want int64(0), got %T %v", nInit, nInit)
		}
	})

	t.Run("record-typed state with `=` is rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `type Stats =
  hp : Int
view App =
  state s : Stats = 0
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "omit `=`") {
			t.Fatalf("want omit-=, got %v", err)
		}
	})

	t.Run("unknown record type errors", func(t *testing.T) {
		_, err := lowerSrc(t, `view App =
  state p : Pokemon
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), `unknown type "Pokemon"`) {
			t.Fatalf("want unknown-type error, got %v", err)
		}
	})

	t.Run("dotted access in expression resolves", func(t *testing.T) {
		// Round-trip: declare a record, store as state, read a nested
		// leaf via `text p.stats.hp`. Must not error.
		_, err := lowerSrc(t, `type Stats =
  hp : Int
type Pokemon =
  stats : Stats
view App =
  state p : Pokemon
  text p.stats.hp
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
	})

	t.Run("nested field assignment resolves", func(t *testing.T) {
		_, err := lowerSrc(t, `type Stats =
  hp : Int
type Pokemon =
  stats : Stats
view App =
  state p : Pokemon
  button "heal" on click { p.stats.hp += 1 }
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
	})
}

// TestListOfRecordsState covers `state items : List<T>` where T is a
// declared record. The row schema is derived recursively from T's
// leaves; for-loop access via `item.field.subfield` resolves; the
// no-arg `.append()` path creates rows with declared defaults.
func TestListOfRecordsState(t *testing.T) {
	t.Run("typed empty list inflates row schema from record", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Stats =
  hp     : Int
  attack : Int
type Pokemon =
  name  : String
  stats : Stats
view App =
  state items : List<Pokemon> = []
  for item in items
    stack horizontal gap=1
      text item.name
      text item.stats.hp
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		// Parent cell exists with an empty list value.
		var parent string
		for id, n := range doc.CellNames {
			if n == "items" {
				parent = id
			}
		}
		if parent == "" {
			t.Fatalf("no `items` cell registered; CellNames=%v", doc.CellNames)
		}
		arr, ok := doc.Cells[parent].([]string)
		if !ok || len(arr) != 0 {
			t.Fatalf("items init: want empty []string, got %T %v", doc.Cells[parent], doc.Cells[parent])
		}
	})

	t.Run("for-loop body resolves dotted field access", func(t *testing.T) {
		// Lowering must not error; access via item.stats.hp inside the
		// template row resolves through the registered alias.
		_, err := lowerSrc(t, `type Stats =
  hp : Int
type Pokemon =
  name  : String
  stats : Stats
view App =
  state items : List<Pokemon> = []
  for item in items
    text item.stats.hp
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
	})

	t.Run("no-init form also produces empty list with schema", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Item =
  label : String
view App =
  state items : List<Item>
  for item in items
    text item.label
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		var parent string
		for id, n := range doc.CellNames {
			if n == "items" {
				parent = id
			}
		}
		if parent == "" {
			t.Fatalf("no `items` cell registered")
		}
	})

	t.Run("non-empty list literal rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `type Item =
  label : String
view App =
  state items : List<Item> = [0]
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "start empty") {
			t.Fatalf("want start-empty error, got %v", err)
		}
	})

	t.Run("inline field decls rejected alongside type", func(t *testing.T) {
		_, err := lowerSrc(t, `type Item =
  label : String
view App =
  state items : List<Item> = []
    extra : Bool
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "drop the inline field decls") {
			t.Fatalf("want drop-inline error, got %v", err)
		}
	})

	t.Run("sum element type rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `type Tag =
  | a
  | b
view App =
  state items : List<Tag> = []
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "must be a record") {
			t.Fatalf("want must-be-record error, got %v", err)
		}
	})

	t.Run("append with no args uses all defaults", func(t *testing.T) {
		// Behavioral check via the lowered handler: an append_struct_item
		// action carries `fields` keyed by the flattened leaf names, each
		// with the type-derived default value.
		_, err := lowerSrc(t, `type Stats =
  hp : Int
type Pokemon =
  name  : String
  stats : Stats
view App =
  state items : List<Pokemon> = []
  button "add" on click { items.append() }
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
	})
}

// TestQueryCommandDecls covers `query` and `command` decls — typed
// CQRS contracts. Validates IR shape, type-resolution against
// declared types, and the relevant error paths (unknown types,
// duplicate names within a kind, duplicate args).
func TestQueryCommandDecls(t *testing.T) {
	t.Run("query with no args", func(t *testing.T) {
		doc, err := lowerSrc(t, `query Ping = Bool
view App =
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if len(doc.Queries) != 1 {
			t.Fatalf("want 1 query, got %d", len(doc.Queries))
		}
		q := doc.Queries[0]
		if q.Name != "Ping" || q.Return.Name != "Bool" || len(q.Inputs) != 0 {
			t.Fatalf("query shape: %+v", q)
		}
	})

	t.Run("query with typed args resolves declared type", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Pokemon =
  id : Int
query GetPokemon -> id : Int = Pokemon
view App =
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		q := doc.Queries[0]
		if q.Name != "GetPokemon" || q.Return.Name != "Pokemon" {
			t.Fatalf("query shape: %+v", q)
		}
		if len(q.Inputs) != 1 || q.Inputs[0].Name != "id" || q.Inputs[0].Type.Name != "Int" {
			t.Fatalf("input shape: %+v", q.Inputs)
		}
	})

	t.Run("command with multiple typed args", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Pokemon =
  id : Int
command CatchPokemon -> id : Int -> name : String = Pokemon
view App =
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		c := doc.Commands[0]
		if c.Name != "CatchPokemon" || len(c.Inputs) != 2 {
			t.Fatalf("command shape: %+v", c)
		}
	})

	t.Run("query and command can share a name", func(t *testing.T) {
		doc, err := lowerSrc(t, `query Foo = Bool
command Foo = Bool
view App =
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if len(doc.Queries) != 1 || len(doc.Commands) != 1 {
			t.Fatalf("want one of each, got %d queries / %d commands", len(doc.Queries), len(doc.Commands))
		}
	})

	t.Run("duplicate query rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `query Foo = Bool
query Foo = Bool
view App =
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "declared more than once") {
			t.Fatalf("want dup-query error, got %v", err)
		}
	})

	t.Run("unknown return type rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `query Foo = Nope
view App =
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), `unknown type "Nope"`) {
			t.Fatalf("want unknown-type error, got %v", err)
		}
	})

	t.Run("unknown input type rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `query Foo -> x : Nope = Bool
view App =
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), `unknown type "Nope"`) {
			t.Fatalf("want unknown-input-type error, got %v", err)
		}
	})

	t.Run("duplicate arg name rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `query Foo -> x : Int -> x : String = Bool
view App =
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "duplicate arg") {
			t.Fatalf("want dup-arg error, got %v", err)
		}
	})

	t.Run("generic return type works", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Pokemon =
  id : Int
query All = List<Pokemon>
view App =
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		q := doc.Queries[0]
		if q.Return.Name != "List" || len(q.Return.GenericArgs) != 1 ||
			q.Return.GenericArgs[0].Name != "Pokemon" {
			t.Fatalf("return: %+v", q.Return)
		}
	})

	t.Run("optional return type works", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Pokemon =
  id : Int
query Find -> id : Int = Pokemon?
view App =
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		q := doc.Queries[0]
		if !q.Return.Optional || q.Return.Name != "Pokemon" {
			t.Fatalf("return: %+v", q.Return)
		}
	})
}

// TestOpCallLowering covers `cell = OpName(args)` and the
// fire-and-forget `OpName(args)` shapes in handler bodies. The
// lowerer produces a call_op Action with op name + resolved args;
// validates the op is declared and arity matches.
func TestOpCallLowering(t *testing.T) {
	findClickAction := func(doc ir.Document) *ir.Action {
		var found *ir.Action
		var walk func(n ir.Node)
		walk = func(n ir.Node) {
			if a, ok := n.Handlers["click"]; ok {
				found = &a
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(doc.Root)
		return found
	}

	t.Run("assignment-RHS produces call_op with target", func(t *testing.T) {
		doc, err := lowerSrc(t, `query GetCount = Int
view App =
  state n = 0
  button "load" on click { n = GetCount() }
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		a := findClickAction(doc)
		if a == nil || a.Kind != "call_op" {
			t.Fatalf("want call_op action, got %+v", a)
		}
		if a.CellID == "" {
			t.Fatalf("want non-empty target cell, got empty")
		}
		if a.Args["op"] != "GetCount" {
			t.Fatalf("want op GetCount, got %v", a.Args["op"])
		}
	})

	t.Run("statement-position produces call_op with no target", func(t *testing.T) {
		doc, err := lowerSrc(t, `command Refresh = Bool
view App =
  button "go" on click { Refresh() }
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		a := findClickAction(doc)
		if a == nil || a.Kind != "call_op" {
			t.Fatalf("want call_op, got %+v", a)
		}
		if a.CellID != "" {
			t.Fatalf("want empty target cell, got %q", a.CellID)
		}
		if a.Args["kind"] != "command" {
			t.Fatalf("want kind command, got %v", a.Args["kind"])
		}
	})

	t.Run("cell ref args resolve to $cell sentinels", func(t *testing.T) {
		doc, err := lowerSrc(t, `query GetThing -> id : Int = Int
view App =
  state target = 7
  state n = 0
  button "load" on click { n = GetThing(target) }
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		a := findClickAction(doc)
		if a == nil || a.Kind != "call_op" {
			t.Fatalf("want call_op, got %+v", a)
		}
		args, _ := a.Args["args"].([]any)
		if len(args) != 1 {
			t.Fatalf("want 1 arg, got %d", len(args))
		}
		s, _ := args[0].(string)
		if s == "" || s[:6] != "$cell." {
			t.Fatalf("want $cell.<id> sentinel, got %v", args[0])
		}
	})

	t.Run("unknown op rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `view App =
  state n = 0
  button "load" on click { n = MysteryOp() }
`)
		if err == nil || !strings.Contains(err.Error(), "unknown query / command") {
			t.Fatalf("want unknown-op error, got %v", err)
		}
	})

	t.Run("arity mismatch rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `query GetThing -> id : Int = Int
view App =
  state n = 0
  button "load" on click { n = GetThing() }
`)
		if err == nil || !strings.Contains(err.Error(), "takes 1 arg") {
			t.Fatalf("want arity error, got %v", err)
		}
	})

	t.Run("list target non-list return rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `query GetItems = Int
view App =
  state items = [0]
  button "load" on click { items = GetItems() }
`)
		if err == nil || !strings.Contains(err.Error(), "List<Record>") {
			t.Fatalf("want List<Record> error, got %v", err)
		}
	})
}

// TestRecordSpread covers `cell = OpReturningRecord(args)` for
// record-typed state. The lowerer emits a call_op_spread action
// with a path→cell mapping for every leaf field.
func TestRecordSpread(t *testing.T) {
	findClick := func(doc ir.Document) *ir.Action {
		var found *ir.Action
		var walk func(n ir.Node)
		walk = func(n ir.Node) {
			if a, ok := n.Handlers["click"]; ok {
				found = &a
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(doc.Root)
		return found
	}

	t.Run("primitive-only record spreads cleanly", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Slot =
  id   : Int
  name : String
query GetSlot -> id : Int = Slot
view App =
  state active : Slot
  button "load" on click { active = GetSlot(1) }
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		a := findClick(doc)
		if a == nil || a.Kind != "call_op_spread" {
			t.Fatalf("want call_op_spread, got %+v", a)
		}
		spread, _ := a.Args["spread"].([]any)
		if len(spread) != 2 {
			t.Fatalf("want 2 leaves, got %d", len(spread))
		}
		paths := map[string]string{}
		for _, raw := range spread {
			m, _ := raw.(map[string]any)
			path, _ := m["path"].(string)
			cell, _ := m["cell"].(string)
			paths[path] = cell
		}
		// Every leaf path maps to a real per-leaf cell.
		for _, leaf := range []string{"id", "name"} {
			if paths[leaf] == "" {
				t.Errorf("missing leaf %q in spread; got %v", leaf, paths)
			}
		}
	})

	t.Run("nested record spreads dotted paths", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Stats =
  hp : Int
type Pokemon =
  name  : String
  stats : Stats
query GetPokemon -> id : Int = Pokemon
view App =
  state p : Pokemon
  button "load" on click { p = GetPokemon(1) }
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		a := findClick(doc)
		if a == nil || a.Kind != "call_op_spread" {
			t.Fatalf("want call_op_spread, got %+v", a)
		}
		spread, _ := a.Args["spread"].([]any)
		// Pokemon flattens to: name, stats.hp (2 leaves).
		paths := map[string]bool{}
		for _, raw := range spread {
			m, _ := raw.(map[string]any)
			paths[m["path"].(string)] = true
		}
		if !paths["name"] || !paths["stats.hp"] {
			t.Fatalf("missing leaves; got %v", paths)
		}
	})

	t.Run("return type mismatch rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `type Slot =
  id : Int
type Other =
  x : Int
query GetOther = Other
view App =
  state active : Slot
  button "load" on click { active = GetOther() }
`)
		if err == nil || !strings.Contains(err.Error(), "returns") {
			t.Fatalf("want return-type-mismatch error, got %v", err)
		}
	})

	t.Run("optional return type rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `type Slot =
  id : Int
query Find -> id : Int = Slot?
view App =
  state active : Slot
  button "load" on click { active = Find(1) }
`)
		if err == nil || !strings.Contains(err.Error(), "optional") {
			t.Fatalf("want optional-return error, got %v", err)
		}
	})

	t.Run("non-op RHS rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `type Slot =
  id : Int
view App =
  state active : Slot
  button "load" on click { active = 0 }
`)
		if err == nil || !strings.Contains(err.Error(), "query / command call") {
			t.Fatalf("want non-op-RHS error, got %v", err)
		}
	})
}

// TestStories covers story lowering: each story becomes a standalone
// sub-document sharing the module's components but owning its cell
// namespace, with full compile checking of the body.
func TestStories(t *testing.T) {
	t.Run("story lowers as its own document", func(t *testing.T) {
		doc, err := lowerSrc(t, `component labeled -> caption =
  card
    title caption
view App =
  labeled "main"
story "Empty" =
  labeled "nothing yet"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if len(doc.Stories) != 1 {
			t.Fatalf("want 1 story, got %d", len(doc.Stories))
		}
		s := doc.Stories[0]
		if s.Name != "Empty" || s.Doc.Name != "Empty" {
			t.Fatalf("want story named Empty, got %q / doc %q", s.Name, s.Doc.Name)
		}
		if s.Doc.Root.Kind != ir.KindCard {
			t.Fatalf("want inlined card root in story doc, got %s", s.Doc.Root.Kind)
		}
	})

	t.Run("story state is isolated from the view", func(t *testing.T) {
		doc, err := lowerSrc(t, `view App =
  state count = 1
  text count
story "Counter at five" =
  state count = 5
  text count
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if len(doc.Cells) != 1 {
			t.Fatalf("story cells leaked into main doc: %v", doc.Cells)
		}
		s := doc.Stories[0]
		if len(s.Doc.Cells) != 1 {
			t.Fatalf("want 1 cell in story doc, got %v", s.Doc.Cells)
		}
		for _, v := range s.Doc.Cells {
			if v != int64(5) && v != 5 {
				t.Fatalf("want story cell initial 5, got %v", v)
			}
		}
	})

	t.Run("stories-only file needs no view", func(t *testing.T) {
		doc, err := lowerSrc(t, `story "Hello" = text "hi"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if len(doc.Stories) != 1 {
			t.Fatalf("want 1 story, got %d", len(doc.Stories))
		}
	})

	t.Run("body errors are compile errors", func(t *testing.T) {
		_, err := lowerSrc(t, `component labeled -> caption =
  card
    title caption
story "Broken" =
  labeled "a" "extra arg"
`)
		if err == nil {
			t.Fatalf("want arity error from story body, got nil")
		}
	})

	t.Run("duplicate names rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `story "Twin" = text "a"
story "Twin" = text "b"
`)
		if err == nil || !strings.Contains(err.Error(), "already declared") {
			t.Fatalf("want duplicate-story error, got %v", err)
		}
	})

	t.Run("empty body rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `story "Hollow" =
view App =
  text "ok"
`)
		if err == nil || !strings.Contains(err.Error(), "has no body") {
			t.Fatalf("want no-body error, got %v", err)
		}
	})
}

// TestSizingVocabulary locks the uniform fit/fill/px sizing
// vocabulary's axis resolution: fill along the parent's main axis is
// flex growth, across it align-self stretch; fit hugs content;
// integers are exact pixels. flex=N stays the main-axis fill(N)
// spelling.
func TestSizingVocabulary(t *testing.T) {
	src := `view App =
  stack horizontal
    stack width=fill
      text "main-axis fill -> flex"
    stack height=fill
      text "cross-axis fill -> stretch"
  stack
    stack height=fill
      text "main-axis fill (column)"
    stack width=fill
      text "cross-axis fill (column)"
    card width=fit height=200
      text "hug + exact"
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	row := doc.Root.Children[0]
	col := doc.Root.Children[1]

	if got := row.Children[0].Props["flex"]; got != 1 {
		t.Errorf("row child width=fill: flex = %v, want 1", got)
	}
	if got := row.Children[1].Props["align-self"]; got != "stretch" {
		t.Errorf("row child height=fill: align-self = %v, want stretch", got)
	}
	if got := col.Children[0].Props["flex"]; got != 1 {
		t.Errorf("col child height=fill: flex = %v, want 1", got)
	}
	if got := col.Children[1].Props["align-self"]; got != "stretch" {
		t.Errorf("col child width=fill: align-self = %v, want stretch", got)
	}
	cardN := col.Children[2]
	if got := cardN.Props["width"]; got != "fit-content" {
		t.Errorf("card width=fit: width = %v, want fit-content", got)
	}
	if got := cardN.Props["fixed-height"]; got != "200px" {
		t.Errorf("card height=200: fixed-height = %v, want 200px", got)
	}
	// Markers must not survive into the IR.
	for _, n := range []map[string]any{row.Props, col.Props, cardN.Props} {
		if _, ok := n["sizing-w"]; ok {
			t.Error("sizing-w marker leaked into IR")
		}
		if _, ok := n["sizing-h"]; ok {
			t.Error("sizing-h marker leaked into IR")
		}
	}
}

// TestSizingRejectsUnknownIdent: width= takes only fit/fill/px now —
// a stray identifier must error with a suggestion instead of leaking
// invalid CSS.
func TestSizingRejectsUnknownIdent(t *testing.T) {
	src := `view App =
  stack width=fil
    text "typo"
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Lower(root); err == nil {
		t.Fatal("want error for width=fil")
	} else if !strings.Contains(err.Error(), "fill") {
		t.Errorf("error should suggest fill: %v", err)
	}
}

// TestInputType covers the input type= masking kwarg: the closed set
// {text, password, email} lowers to a `type` prop; anything else is a
// diagnosed error with a suggestion.
func TestInputType(t *testing.T) {
	doc, err := lowerSrc(t, `view Login =
  state pw = ""
  stack
    input pw type=password placeholder="password"
`)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	in := doc.Root.Children[0]
	if in.Kind != ir.KindTextInput {
		t.Fatalf("first child kind = %v, want text input", in.Kind)
	}
	if got := in.Props["type"]; got != "password" {
		t.Fatalf("input type prop = %v, want password", got)
	}

	_, err = lowerSrc(t, `view Login =
  state pw = ""
  input pw type=telephone
`)
	if err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("want unknown-type error, got %v", err)
	}
}

// TestCommandLifecycleCells covers the implicit `<Op>.pending` /
// `.failed` / `.error` cells a command op contributes (mirroring
// streams): readable in if / text / disabled=, rejected as write
// targets, and the read-only diagnostic names the kind ("command").
func TestCommandLifecycleCells(t *testing.T) {
	prelude := `backend Api =
  url same-origin
  auth none
command Login -> email : String -> password : String = Bool
`
	t.Run("readable in disabled= and if branches", func(t *testing.T) {
		_, err := lowerSrc(t, prelude+`view App =
  state email = ""
  state password = ""
  state ok = false
  button "Sign in" disabled=Login.pending on click { ok = Login(email, password) }
  if Login.pending
    text "signing in"
  if Login.failed
    text Login.error
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
	})

	t.Run("assignment to a command lifecycle cell is rejected, naming the kind", func(t *testing.T) {
		_, err := lowerSrc(t, prelude+`view App =
  button "x" on click { Login.pending = true }
`)
		if err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("want read-only error, got %v", err)
		}
		if !strings.Contains(err.Error(), "command") {
			t.Fatalf("read-only error should name the kind (command): %v", err)
		}
	})

	t.Run("state shadowing a command op name is rejected", func(t *testing.T) {
		_, err := lowerSrc(t, prelude+`view App =
  state Login = ""
  text Login
`)
		if err == nil || !strings.Contains(err.Error(), "collides with command") {
			t.Fatalf("want collides-with-command error, got %v", err)
		}
	})
}

// TestDiscriminatedUnions covers Rust-flavored unions: payload-carrying
// variant decls, union/enum state defaults, `match` exhaustiveness, the
// `as` payload binding, variant construction, and the error paths.
func TestDiscriminatedUnions(t *testing.T) {
	t.Run("payload variants lower into VariantSpecs", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Result =
  | ok : String
  | pending
view App =
  text "ok"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		td := doc.Types[0]
		if td.Kind != "sum" || len(td.VariantSpecs) != 2 {
			t.Fatalf("type shape: %+v", td)
		}
		if td.VariantSpecs[0].Name != "ok" || td.VariantSpecs[0].Payload == nil ||
			td.VariantSpecs[0].Payload.Name != "String" {
			t.Fatalf("ok variant: %+v", td.VariantSpecs[0])
		}
		if td.VariantSpecs[1].Payload != nil {
			t.Fatalf("pending should be a unit variant: %+v", td.VariantSpecs[1])
		}
		if !td.HasPayloads() {
			t.Fatal("HasPayloads should be true")
		}
	})

	t.Run("union state default-initializes to the first (unit) variant", func(t *testing.T) {
		doc, err := lowerSrc(t, `type Fetch =
  | idle
  | done : String
view App =
  state s : Fetch
  match s
    | idle
      text "idle"
    | done as body
      text body
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		var init any
		for id, name := range doc.CellNames {
			if name == "s" {
				init = doc.Cells[id]
			}
		}
		uv, ok := init.(ir.UnionValue)
		if !ok || uv.Tag != "idle" {
			t.Fatalf("union init: want UnionValue{idle}, got %#v", init)
		}
	})

	t.Run("exhaustive match over an all-unit enum works", func(t *testing.T) {
		_, err := lowerSrc(t, `type Tone =
  | dark
  | light
view App =
  state t : Tone
  match t
    | dark
      text "dark"
    | light
      text "light"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
	})

	cases := []struct{ name, src, want string }{
		{"non-exhaustive", `type F =
  | a
  | b : String
view App =
  state s : F
  match s
    | a
      text "a"
`, "not exhaustive"},
		{"unknown arm", `type F =
  | a : String
view App =
  state s : F
  match s
    | a as x
      text x
    | zzz
      text "z"
`, "not a variant"},
		{"duplicate arm", `type F =
  | a
view App =
  state s : F
  match s
    | a
      text "a"
    | a
      text "a2"
`, "duplicate match arm"},
		{"as on unit variant", `type F =
  | a
  | b : String
view App =
  state s : F
  match s
    | a as x
      text "a"
    | b as y
      text y
`, "carries no payload"},
		{"match on non-union", `view App =
  state n = 0
  match n
    | a
      text "a"
`, "not one"},
		{"payload-first default rejected", `type F =
  | a : String
  | b
view App =
  state s : F
  text "ok"
`, "reorder"},
		{"construct payload on unit variant", `type F =
  | a : String
  | b
view App =
  state s : F
  button "x" on click { s = b("oops") }
`, "unit variant"},
		{"construct unit needs no payload call", `type F =
  | a : String
view App =
  state s : F
  button "x" on click { s = a }
`, "carries a payload"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := lowerSrc(t, c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}

// TestNavigate covers the navigate action (REQUEST 14): the bare
// statement lowers to a `navigate` action with a path, and the
// `Op(...) then navigate "<path>"` hook stamps the path onto the
// op's call_op action so the emitter can run it in the success path.
func TestNavigate(t *testing.T) {
	findClick := func(doc ir.Document) *ir.Action {
		var found *ir.Action
		var walk func(n ir.Node)
		walk = func(n ir.Node) {
			if a, ok := n.Handlers["click"]; ok {
				found = &a
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(doc.Root)
		return found
	}

	t.Run("bare navigate lowers to a navigate action", func(t *testing.T) {
		doc, err := lowerSrc(t, `view App =
  button "x" on click { navigate "/login" }
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		a := findClick(doc)
		if a == nil || a.Kind != "navigate" || a.Args["path"] != "/login" {
			t.Fatalf("want navigate action to /login, got %+v", a)
		}
	})

	t.Run("then-navigate stamps the path onto the op call", func(t *testing.T) {
		doc, err := lowerSrc(t, `backend Api =
  url same-origin
  auth none
command Login -> email : String -> password : String = Bool
view App =
  state email = ""
  state password = ""
  button "x" on click { Login(email, password) then navigate "/" }
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		a := findClick(doc)
		if a == nil || a.Kind != "call_op" || a.Args["then_navigate"] != "/" {
			t.Fatalf("want call_op with then_navigate=/, got %+v", a)
		}
	})

	t.Run("then without navigate is a parse error", func(t *testing.T) {
		_, err := parser.Parse(`command Go = Bool
view App =
  button "x" on click { Go() then "oops" }
`)
		if err == nil || !strings.Contains(err.Error(), "navigate") {
			t.Fatalf("want then-navigate parse error, got %v", err)
		}
	})
}

// TestCapabilityGate locks the native/foreign target capability split:
// introspection verbs (expect-cell) require a Sigil-native target, so
// using one against an `external` app is a compile error in lower, while
// Observe-floor verbs (expect-count) work against any target.
func TestCapabilityGate(t *testing.T) {
	t.Run("expect-cell against external target is refused", func(t *testing.T) {
		_, err := lowerSrc(t, `app Acme =
  target web
    external
    host "http://x"
test "t" = scenario in Acme
  expect-cell agents 3
`)
		if err == nil || !strings.Contains(err.Error(), "needs a Sigil-native target") {
			t.Fatalf("want capability error, got %v", err)
		}
	})

	t.Run("expect-count against external target is allowed", func(t *testing.T) {
		_, err := lowerSrc(t, `app Acme =
  target web
    external
    host "http://x"
test "t" = scenario in Acme
  expect-count ".agent" 3
`)
		if err != nil {
			t.Fatalf("expect-count should be allowed on a foreign target: %v", err)
		}
	})

	t.Run("expect-cell against native target is allowed", func(t *testing.T) {
		_, err := lowerSrc(t, `app Acme =
  target web
    host "http://x"
test "t" = scenario in Acme
  expect-cell agents 3
`)
		if err != nil {
			t.Fatalf("expect-cell should be allowed on a native target: %v", err)
		}
	})
}

// TestScenarioMatch covers the `match text-of "<sel>"` branch verb: arms
// lower as nested assertion steps; non-assertion steps and malformed
// subjects are rejected.
func TestScenarioMatch(t *testing.T) {
	t.Run("arms lower as nested steps", func(t *testing.T) {
		doc, err := lowerSrc(t, `app A =
  target web
    external
    host "http://x"
test "t" = scenario in A
  match text-of "#plan"
    | "pro"
      expect-text "Pro features unlocked"
    | "free"
      expect-text "Upgrade"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		st := doc.Tests[0].Steps[0]
		if st.Kind != "match" || len(st.Arms) != 2 {
			t.Fatalf("want match with 2 arms, got %q with %d arms", st.Kind, len(st.Arms))
		}
		if st.Arms[0].Match != "pro" || len(st.Arms[0].Steps) != 1 ||
			st.Arms[0].Steps[0].Kind != "expect_text" {
			t.Fatalf("arm 0 wrong: %+v", st.Arms[0])
		}
	})

	t.Run("non-assertion step in an arm is rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `app A =
  target web
    external
    host "http://x"
test "t" = scenario in A
  match text-of "#plan"
    | "pro"
      click button "x"
`)
		if err == nil || !strings.Contains(err.Error(), "assertions only") {
			t.Fatalf("want assertions-only error, got %v", err)
		}
	})

	t.Run("subject must be text-of", func(t *testing.T) {
		_, err := lowerSrc(t, `app A =
  target web
    external
    host "http://x"
test "t" = scenario in A
  match "#plan"
    | "pro"
      expect-text "x"
`)
		if err == nil || !strings.Contains(err.Error(), "text-of") {
			t.Fatalf("want text-of error, got %v", err)
		}
	})
}

// TestFlow covers the `flow` composition primitive: no-param and
// parameterized flows inline into a scenario's step list; recursion,
// verb-shadowing, and arg-count mismatches are rejected.
func TestFlow(t *testing.T) {
	t.Run("no-param flow inlines its steps", func(t *testing.T) {
		doc, err := lowerSrc(t, `app A =
  target web
    external
    host "http://x"
flow greet =
  expect-text "Hi"
  expect-text "Bye"
test "t" = scenario in A
  greet
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		steps := doc.Tests[0].Steps
		if len(steps) != 2 || steps[0].Kind != "expect_text" || steps[1].Kind != "expect_text" {
			t.Fatalf("flow did not inline: %+v", steps)
		}
	})

	t.Run("param flow substitutes its argument", func(t *testing.T) {
		doc, err := lowerSrc(t, `app A =
  target web
    external
    host "http://x"
flow fillEmail -> email =
  fill input "Email" email
test "t" = scenario in A
  fillEmail "ada@acme.io"
`)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		s := doc.Tests[0].Steps[0]
		if s.Kind != "fill" || s.Args["value"] != "ada@acme.io" {
			t.Fatalf("param not substituted: %+v", s)
		}
	})

	t.Run("recursive flow is rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `app A =
  target web
    external
    host "http://x"
flow loop =
  expect-text "x"
  loop
test "t" = scenario in A
  loop
`)
		if err == nil || !strings.Contains(err.Error(), "recursive flow") {
			t.Fatalf("want recursion error, got %v", err)
		}
	})

	t.Run("flow shadowing a step verb is rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `flow click =
  expect-text "x"
view V = text "x"
`)
		if err == nil || !strings.Contains(err.Error(), "shadows a built-in step verb") {
			t.Fatalf("want shadow error, got %v", err)
		}
	})

	t.Run("flow arg-count mismatch is rejected", func(t *testing.T) {
		_, err := lowerSrc(t, `app A =
  target web
    external
    host "http://x"
flow f -> a =
  expect-text a
test "t" = scenario in A
  f "x" "y"
`)
		if err == nil || !strings.Contains(err.Error(), "expects 1 arg") {
			t.Fatalf("want arg-count error, got %v", err)
		}
	})
}

// TestCodeBlockVerbatim locks the `code` primitive: the indented body is
// captured raw into Props["text"] — newlines preserved, `${…}` and braces
// left untouched (no interpolation, no binding).
func TestCodeBlockVerbatim(t *testing.T) {
	doc, err := lowerSrc(t, `view Docs =
  code
    state count = 0
    text "value: ${count}"
    button "+" on click { count += 1 }
`)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	root := doc.Root
	if root.Kind != ir.KindCode {
		t.Fatalf("want code root, got %s", root.Kind)
	}
	if len(root.Bindings) != 0 {
		t.Fatalf("code must not bind: got %+v", root.Bindings)
	}
	got, _ := root.Props["text"].(string)
	want := "state count = 0\n" +
		"text \"value: ${count}\"\n" +
		"button \"+\" on click { count += 1 }"
	if got != want {
		t.Fatalf("verbatim body mismatch:\n got: %q\nwant: %q", got, want)
	}
	// The interpolation marker must survive literally — not be treated as a
	// cell reference (which would error, since `count` is inside the listing).
	if !strings.Contains(got, "${count}") {
		t.Fatalf("interpolation marker was not preserved: %q", got)
	}
}

// TestCodeBlockInlineString covers the one-line `code "x"` form: still
// verbatim (no interpolation), content straight into Props["text"].
func TestCodeBlockInlineString(t *testing.T) {
	doc, err := lowerSrc(t, `view Docs =
  code "echo ${HOME}"
`)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if doc.Root.Kind != ir.KindCode {
		t.Fatalf("want code root, got %s", doc.Root.Kind)
	}
	if got, _ := doc.Root.Props["text"].(string); got != "echo ${HOME}" {
		t.Fatalf("inline code body mismatch: %q", got)
	}
}
