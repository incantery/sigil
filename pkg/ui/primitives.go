package ui

import (
	"fmt"

	"github.com/incantery/mako/pkg/ir"
)

// newNode is the constructor every primitive funnels through. It seeds the
// maps so options and bindings can write without nil-checks.
func newNode(kind ir.Kind) ir.Node {
	return ir.Node{
		Kind:     kind,
		Props:    map[string]any{},
		Handlers: map[string]ir.Action{},
		Bindings: map[string]ir.BindingRef{},
	}
}

// splitParts splits a primitive's variadic into options and child components.
// Strings convert to Text() so callers can write ui.Card("hi", ui.Button(...)).
func splitParts(parts []any) (opts []Option, kids []Component) {
	for _, p := range parts {
		switch v := p.(type) {
		case Option:
			opts = append(opts, v)
		case Component:
			kids = append(kids, v)
		case string:
			kids = append(kids, Text(v))
		default:
			// Programmer error — fail loudly at build time, not silently at render.
			panic(fmt.Sprintf("sigil/ui: unexpected argument of type %T", p))
		}
	}
	return
}

// build is the shared lowering step: apply options, recurse children with a
// child Context so stable IDs are stable.
func build(ctx *Context, n ir.Node, opts []Option, kids []Component) ir.Node {
	n.ID = ctx.id()
	for _, o := range opts {
		o.applyTo(&n)
	}
	for i, k := range kids {
		n.Children = append(n.Children, k.Build(ctx.child(fmt.Sprintf("%d", i))))
	}
	return n
}

// component is the lazy wrapper every primitive returns. We defer building
// until the compiler walks the tree so Context can thread through.
type component struct {
	build func(*Context) ir.Node
}

func (c component) Build(ctx *Context) ir.Node { return c.build(ctx) }

// --- Primitives ---

// Stack is the layout primitive. Default axis is vertical; use Horizontal()
// to flip. Stack is intentionally the only layout primitive — Grid, Group,
// and Spacer are sugar over it.
func Stack(parts ...any) Component {
	opts, kids := splitParts(parts)
	return component{build: func(ctx *Context) ir.Node {
		n := newNode(ir.KindStack)
		if _, set := propOf(opts, "axis"); !set {
			n.Props["axis"] = "vertical"
		}
		return build(ctx, n, opts, kids)
	}}
}

// Card is a content container with visual affordance (border, padding). It
// has no semantic effect beyond grouping — but the renderer styles it.
func Card(parts ...any) Component {
	opts, kids := splitParts(parts)
	return component{build: func(ctx *Context) ir.Node {
		return build(ctx, newNode(ir.KindCard), opts, kids)
	}}
}

// Title is a top-level heading. The renderer maps it to <h1> or equivalent.
func Title(s string) Component {
	return component{build: func(ctx *Context) ir.Node {
		n := newNode(ir.KindTitle)
		n.Props["text"] = s
		n.ID = ctx.id()
		return n
	}}
}

// Text is an inline text run.
func Text(s string) Component {
	return component{build: func(ctx *Context) ir.Node {
		n := newNode(ir.KindText)
		n.Props["text"] = s
		n.ID = ctx.id()
		return n
	}}
}

// TextInput is a single-line text input two-way bound to a string Cell. Every
// keystroke fires an input action that writes the input's current value into
// the cell; cell changes from elsewhere reflect back into the input (the
// runtime skips writes that would no-op so the user's caret stays put).
func TextInput(c *Cell[string], opts ...Option) Component {
	return component{build: func(ctx *Context) ir.Node {
		n := newNode(ir.KindTextInput)
		n.ID = ctx.id()
		n.Props["value"] = c.Initial()
		n.Bindings["value"] = ir.BindingRef{CellID: c.ID()}
		// Auto-wired: every input event sets the cell to the input's current
		// value. The "$event.value" sentinel is resolved by the runtime at
		// dispatch time against the actual input element.
		n.Handlers["input"] = ir.Action{
			Kind:   "set",
			CellID: c.ID(),
			Args:   map[string]any{"value": "$event.value"},
		}
		for _, o := range opts {
			o.applyTo(&n)
		}
		return n
	}}
}

// Button is an interactive primitive. The first string arg is the label; any
// remaining args are Options (e.g. OnClick).
func Button(parts ...any) Component {
	var label string
	var rest []any
	for _, p := range parts {
		if s, ok := p.(string); ok && label == "" {
			label = s
			continue
		}
		rest = append(rest, p)
	}
	opts, kids := splitParts(rest)
	return component{build: func(ctx *Context) ir.Node {
		n := newNode(ir.KindButton)
		n.Props["label"] = label
		return build(ctx, n, opts, kids)
	}}
}

// propOf peeks whether an option set wrote a given prop. Used so primitives
// can apply defaults only when the author didn't override.
func propOf(opts []Option, key string) (any, bool) {
	probe := ir.Node{Props: map[string]any{}}
	for _, o := range opts {
		o.applyTo(&probe)
	}
	v, ok := probe.Props[key]
	return v, ok
}
