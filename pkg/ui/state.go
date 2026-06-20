package ui

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/incantery/sigil/pkg/ir"
)

// Cell[T] is a reactive state cell. The author calls State(initial) to create
// one; the runtime keeps a live copy keyed by Cell.id and re-applies bound
// props when the value changes.
//
// In M2 (pure-client mode) cells live entirely in the browser. In M4 the same
// Cell type will route through a server session — the author-facing API is
// designed to be identical in both modes.
type Cell[T any] struct {
	id      string
	initial T
}

// ID is the stable identifier the runtime uses to find the cell.
func (c *Cell[T]) ID() string { return c.id }

// Initial is the value the cell starts at.
func (c *Cell[T]) Initial() T { return c.initial }

// --- M2 registry --------------------------------------------------------
//
// For M2 cell ids are generated from a package-level counter, and each
// freshly-created cell registers its initial value in a package-level map.
// Renderers snapshot the map and reset it per request. This is wrong for
// multi-session servers and will be replaced by session-scoped state in M4 —
// flagged here so we remember to rip it out.
//
// snapshotMu also serializes whole renders so concurrent requests don't
// interleave cell creation. Acceptable for a demo / single-user dev loop;
// not acceptable past M2.

var (
	cellMu       sync.Mutex
	cellCounter  atomic.Uint64
	cellRegistry = map[string]any{}

	// snapshotMu wraps an entire build+snapshot operation so two concurrent
	// renders can't see each other's cells. Held by render.html.WritePage.
	snapshotMu sync.Mutex
)

// State creates a new reactive Cell with the given initial value. Call this
// from a component-building function:
//
//	count := ui.State(0)
//	return ui.Stack(count.Format(), ui.Button("+", ui.OnClick(ui.Add(count, 1))))
func State[T any](initial T) *Cell[T] {
	id := fmt.Sprintf("c%d", cellCounter.Add(1))
	cellMu.Lock()
	cellRegistry[id] = initial
	cellMu.Unlock()
	return &Cell[T]{id: id, initial: initial}
}

// SnapshotCells returns and clears the package-level registry, and resets the
// cell-id counter. Renderers call this after walking a component tree to
// gather the initial state map for the runtime.
func SnapshotCells() map[string]any {
	cellMu.Lock()
	defer cellMu.Unlock()
	out := cellRegistry
	cellRegistry = map[string]any{}
	cellCounter.Store(0)
	return out
}

// LockRender / UnlockRender bracket a full render so concurrent requests
// don't interleave cell creation while M2's package-level registry exists.
func LockRender()   { snapshotMu.Lock() }
func UnlockRender() { snapshotMu.Unlock() }

// --- Actions ------------------------------------------------------------

// Set produces an Action that overwrites the cell with v.
func (c *Cell[T]) Set(v T) ir.Action {
	return ir.Action{
		Kind:   "set",
		CellID: c.id,
		Args:   map[string]any{"value": v},
	}
}

// Numeric is the constraint for cells that support arithmetic actions.
type Numeric interface {
	~int | ~int32 | ~int64 | ~float32 | ~float64
}

// Add produces an "add delta" Action against a numeric cell. Free function
// because Go generics can't constrain methods on Cell[T any] independently.
func Add[T Numeric](c *Cell[T], delta T) ir.Action {
	return ir.Action{
		Kind:   "add",
		CellID: c.id,
		Args:   map[string]any{"delta": delta},
	}
}

// Toggle flips a boolean cell.
func Toggle(c *Cell[bool]) ir.Action {
	return ir.Action{Kind: "toggle", CellID: c.id}
}

// --- Bindings -----------------------------------------------------------

// Format returns a Component that renders the cell's current value as text
// and keeps that text in sync as the cell changes.
func (c *Cell[T]) Format() Component {
	return component{build: func(ctx *Context) ir.Node {
		n := newNode(ir.KindText)
		n.ID = ctx.id()
		n.Props["text"] = fmt.Sprintf("%v", c.initial)
		n.Bindings["text"] = ir.BindingRef{CellID: c.id}
		return n
	}}
}

// --- List cells ---------------------------------------------------------

// ListCell[T] is an ordered list of child Cell[T] values. The list itself is
// registered as a normal cell whose value is the ordered slice of child cell
// ids — that's what the runtime mutates on append/remove. Each child cell
// stays addressable on its own, so per-row actions and bindings reference the
// child cell directly and the list's order only matters for DOM positioning.
type ListCell[T any] struct {
	id       string
	children []*Cell[T]
}

// ID returns the parent cell id (the one that holds the ordered child id list).
func (l *ListCell[T]) ID() string { return l.id }

// Children returns the current child cells. Renderers iterate this in For;
// authors generally don't touch it.
func (l *ListCell[T]) Children() []*Cell[T] { return l.children }

// ListState creates a list cell with one child cell per initial value.
func ListState[T any](initial ...T) *ListCell[T] {
	l := &ListCell[T]{
		id: fmt.Sprintf("c%d", cellCounter.Add(1)),
	}
	for _, v := range initial {
		l.children = append(l.children, State(v))
	}
	childIDs := make([]string, len(l.children))
	for i, c := range l.children {
		childIDs[i] = c.id
	}
	cellMu.Lock()
	cellRegistry[l.id] = childIDs
	cellMu.Unlock()
	return l
}

// AppendItem produces an Action that grows the list by one. The runtime
// generates a fresh client-side cell id (prefixed `r` to avoid colliding with
// server-rendered `c` ids) and clones the For's template row.
func AppendItem[T any](l *ListCell[T], defaultValue T) ir.Action {
	return ir.Action{
		Kind:   "append_item",
		CellID: l.id,
		Args:   map[string]any{"value": defaultValue},
	}
}

// RemoveItem produces an Action that removes the given child from the list
// (and its DOM row). The child's cell stays in the registry; cleanup is a
// later concern.
func RemoveItem[T any](l *ListCell[T], child *Cell[T]) ir.Action {
	return ir.Action{
		Kind:   "remove_item",
		CellID: l.id,
		Args:   map[string]any{"target": child.id},
	}
}

// sentinelCell makes a cell whose id is the literal "$ITEM" placeholder used
// in For's template row. The runtime substitutes the placeholder for a real
// cell id when cloning the template on append. Not registered in the cell
// registry — it's a render-time tag, not a real cell.
func sentinelCell[T any]() *Cell[T] {
	return &Cell[T]{id: "$ITEM"}
}
