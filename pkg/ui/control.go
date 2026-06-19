package ui

import (
	"fmt"

	"github.com/incantery/mako/pkg/ir"
)

// If renders its children only when the given boolean cell is truthy. The
// gated subtree actually mounts and unmounts as the cell flips — when false,
// the children live inside an inert <template> element on the HTML target and
// the runtime hoists them into the live DOM on toggle-true.
//
// Cells referenced from inside an initially-closed If are still registered
// (the server walks all children regardless of visibility) and are re-read
// from the current cell registry at mount time so the freshly-mounted subtree
// shows up-to-date values rather than the values that were current at page
// load.
func If(c *Cell[bool], children ...any) Component {
	opts, kids := splitParts(children)
	return component{build: func(ctx *Context) ir.Node {
		n := newNode(ir.KindIf)
		n.ID = ctx.id()
		n.Bindings["visible"] = ir.BindingRef{CellID: c.ID()}
		n.Props["initial"] = c.Initial()
		for _, o := range opts {
			o.applyTo(&n)
		}
		for i, k := range kids {
			n.Children = append(n.Children, k.Build(ctx.child(fmt.Sprintf("%d", i))))
		}
		return n
	}}
}

// For renders one row per child cell in the given ListCell, plus an inert
// template row that the runtime clones on append. The render closure gets the
// child cell directly so actions and bindings target it without index
// arithmetic — adding or removing items doesn't shift any other row's
// identity.
//
// Author pattern:
//
//	items := ui.ListState[int](0, 0, 0)
//	ui.For(items, func(item *ui.Cell[int]) ui.Component {
//	    return ui.Stack(
//	        ui.Button("-", ui.OnClick(ui.Add(item, -1))),
//	        item.Format(),
//	        ui.Button("+", ui.OnClick(ui.Add(item, 1))),
//	        ui.Button("x", ui.OnClick(ui.RemoveItem(items, item))),
//	    )
//	})
func For[T any](l *ListCell[T], render func(*Cell[T]) Component) Component {
	return component{build: func(ctx *Context) ir.Node {
		n := newNode(ir.KindFor)
		n.ID = ctx.id()
		n.Props["cell"] = l.ID()

		// Live rows for each existing child.
		for i, child := range l.children {
			body := render(child).Build(ctx.child(fmt.Sprintf("%d", i)))
			row := ir.Node{
				Kind: ir.KindForItem,
				ID:   fmt.Sprintf("%s/row-%d", n.ID, i),
				Props: map[string]any{
					"cell":   child.ID(),
					"parent": l.ID(),
				},
				Children: []ir.Node{body},
			}
			n.Children = append(n.Children, row)
		}

		// Template row, rendered with the $ITEM sentinel so any actions or
		// bindings inside use that placeholder. The runtime swaps $ITEM for a
		// fresh cell id when cloning on append.
		body := render(sentinelCell[T]()).Build(ctx.child("tmpl"))
		tmpl := ir.Node{
			Kind: ir.KindForItem,
			ID:   fmt.Sprintf("%s/tmpl", n.ID),
			Props: map[string]any{
				"cell":     "$ITEM",
				"parent":   l.ID(),
				"template": true,
			},
			Children: []ir.Node{body},
		}
		n.Children = append(n.Children, tmpl)
		return n
	}}
}
