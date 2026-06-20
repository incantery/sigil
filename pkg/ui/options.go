package ui

import "github.com/incantery/sigil/pkg/ir"

// Option configures a parent primitive. Options are passed alongside child
// Components in a primitive's variadic; the primitive distinguishes them by
// type and applies them to the produced ir.Node.
type Option interface {
	applyTo(*ir.Node)
}

type optionFn func(*ir.Node)

func (f optionFn) applyTo(n *ir.Node) { f(n) }

// Horizontal lays a Stack's children along the x-axis.
func Horizontal() Option {
	return optionFn(func(n *ir.Node) { n.Props["axis"] = "horizontal" })
}

// Vertical lays a Stack's children along the y-axis. Default for Stack.
func Vertical() Option {
	return optionFn(func(n *ir.Node) { n.Props["axis"] = "vertical" })
}

// Gap sets the inter-child spacing in semantic units (the renderer maps to px).
func Gap(units int) Option {
	return optionFn(func(n *ir.Node) { n.Props["gap"] = units })
}

// OnClick attaches a declarative Action to a node's click event. The Action
// is serialized into the IR (and on the HTML target, into a data attribute);
// the runtime applies it client-side without a server round-trip.
func OnClick(a ir.Action) Option {
	return optionFn(func(n *ir.Node) { n.Handlers["click"] = a })
}

// OnInput attaches an Action to a node's input event (every keystroke for text
// inputs). Usually you don't reach for this directly — TextInput wires its own
// on-input handler so the bound cell stays current.
func OnInput(a ir.Action) Option {
	return optionFn(func(n *ir.Node) { n.Handlers["input"] = a })
}

// Placeholder sets a text input's placeholder prop. Renderers map it to the
// platform-native placeholder (the `placeholder` attribute on HTML inputs).
func Placeholder(s string) Option {
	return optionFn(func(n *ir.Node) { n.Props["placeholder"] = s })
}
