package format

import (
	"strings"
	"testing"

	"github.com/incantery/sigil/pkg/lang/parser"
)

// TestRoundtrip parses then formats and verifies the output is a fixed
// point under a second format pass (idempotence). Picks examples that
// exercise every formatting path.
func TestRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "counter with sugar re-sugar",
			src: `view Counter =
  state count = 0
  card
    title "Counter"
    stack horizontal gap=1
      button "-" on click { count -= 1 }
      text count
      button "+" on click { count += 1 }
      button "reset" on click { count = 0 }
`,
		},
		{
			name: "interpolation + toggle",
			src: `view D =
  state expanded = false
  state count = 0
  card
    button "t" on click { expanded = !expanded }
    text "count: ${count}"
    if expanded
      text "hi"
`,
		},
		{
			name: "list literal + for + method call",
			src: `view L =
  state items = [0, 0, 0]
  card
    for item in items
      button "x" on click { items.remove(item) }
    button "+" on click { items.append(0) }
`,
		},
		{
			name: "structured list with field decls + dotted access",
			src: `view T =
  state items = []
    label : String
    done : Bool = false
  state draft = ""
  card
    for item in items
      stack horizontal gap=1
        button "toggle" on click { item.done = !item.done }
        if item.done
          text "✓"
        text item.label
`,
		},
		{
			name: "theme decl with extends + tone bindings",
			src: `theme dawn extends light =
  primary = "#ff6b35" on "#0a0a0a"
  accent = "#0f766e" on "#ffffff"
view X =
  button "x" tone=primary
`,
		},
		{
			name: "component with variadic + splice",
			src: `component named-counter -> count =
  stack horizontal gap=1
    button "-" on click { count -= 1 }
    text count
    button "+" on click { count += 1 }
component my-card -> heading -> *body =
  card
    title heading
    *body
view App =
  state apples = 5
  my-card "Counters"
    named-counter apples
`,
		},
		{
			name: "code block verbatim body",
			src: `view Docs =
  text "Example:"
  code
    view Counter =
      state count = 0
      button "+" on click { count += 1 }
  text "Done."
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			once := Source(root)
			root2, err := parser.Parse(once)
			if err != nil {
				t.Fatalf("re-parse: %v\nsource:\n%s", err, once)
			}
			twice := Source(root2)
			if once != twice {
				t.Fatalf("not idempotent:\nfirst:\n%s\nsecond:\n%s",
					once, twice)
			}
		})
	}
}

// TestResugarsCompound verifies that the desugared `cell = cell + N` AST
// the parser emits round-trips back to the sugary `cell += N` form.
func TestResugarsCompound(t *testing.T) {
	src := `view C =
  state n = 0
  button "x" on click { n = n + 5 }
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := Source(root)
	if !strings.Contains(got, "n += 5") {
		t.Fatalf("expected n += 5 in output; got:\n%s", got)
	}
	if strings.Contains(got, "n = n + 5") {
		t.Fatalf("desugared form leaked into output; got:\n%s", got)
	}
}

// TestStoryFormatting verifies `story "<name>" =` round-trips through
// the canonical printer like a test decl does.
func TestStoryFormatting(t *testing.T) {
	src := `story "Empty card" =
  card
    title "Nothing yet"
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := Source(root)
	if got != src {
		t.Fatalf("want canonical form unchanged:\nwant:\n%s\ngot:\n%s", src, got)
	}
}
