// Package ui is the author-facing API. Apps import this package and (in v1)
// nothing else. Everything else — IR, renderers, runtime — is plumbing.
package ui

import "github.com/incantery/sigil/pkg/ir"

// Component is the unit of authoring. Components don't render eagerly; they
// produce IR when Build is called by the compiler. This lets the framework
// thread a Context (state registry, handler registry, path) through the tree.
type Component interface {
	Build(ctx *Context) ir.Node
}

// Context flows down the tree during a render pass. In v1 it carries the
// component path used to derive stable IDs. Later it will host the state cell
// registry, handler registry, and renderer hints.
type Context struct {
	path []string
}

// NewContext constructs a fresh root Context. Renderers call this once per
// render pass.
func NewContext() *Context { return &Context{} }

// child returns a new Context one level deeper in the tree. The path is the
// only thing that varies between siblings so that stable IDs are stable.
func (c *Context) child(seg string) *Context {
	next := make([]string, len(c.path)+1)
	copy(next, c.path)
	next[len(c.path)] = seg
	return &Context{path: next}
}

// id derives a stable id for the current path. Renderers and the diff use
// this as the patch key.
func (c *Context) id() string {
	if len(c.path) == 0 {
		return "/"
	}
	out := ""
	for _, seg := range c.path {
		out += "/" + seg
	}
	return out
}

// Build implements Component for a raw ir.Node so renderers can treat
// already-lowered nodes uniformly. Authors don't use this directly.
type rawNode struct{ n ir.Node }

func (r rawNode) Build(*Context) ir.Node { return r.n }

// FromIR wraps a raw IR node as a Component. Useful for testing.
func FromIR(n ir.Node) Component { return rawNode{n: n} }
