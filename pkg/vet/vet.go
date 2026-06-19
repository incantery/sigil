// Package vet runs warning-level static checks over a parsed Sigil AST.
//
// `sigil check` covers errors (parse + lower fails compile). vet adds
// non-fatal hints — declared-but-unused state cells, declared-but-never-
// invoked user components, etc. Findings carry the same shape as a
// compiler Diagnostic (with Severity="warning") so editor tooling and
// `sigil vet --json` can surface them through the existing channel.
//
// Rules are intentionally narrow at v0; expand only when a real false
// negative shows up (so the noise/signal ratio stays high). Add a new
// rule = add a small walker + an entry in the runner.
package vet

import (
	"fmt"
	"strings"

	"github.com/incantery/mako/pkg/lang/ast"
	"github.com/incantery/mako/pkg/lang/diag"
)

// Run walks the AST and returns a slice of warnings. The root may be a
// bare `view`, a `module` of decls, or — if parse partially failed —
// any subtree shape; Run treats anything unrecognized as a no-op
// rather than asserting structure.
func Run(root *ast.Node) []*diag.Diagnostic {
	if root == nil {
		return nil
	}

	// One pass: collect declarations + usages for every kind of name
	// we care about, then derive findings.
	c := newCollector()
	c.walk(root)

	var out []*diag.Diagnostic
	out = append(out, c.unusedStates()...)
	out = append(out, c.unusedComponents()...)
	out = append(out, c.storylessComponents()...)
	return out
}

// collector is the per-file accumulator: declarations + usages.
type collector struct {
	stateDecls     map[string]ast.Pos // state name → decl position
	stateUses      map[string]bool    // any reference (any kind)
	componentDecls map[string]ast.Pos // component name → decl position
	componentUses  map[string]bool    // call sites
	// loopVars track for-loop iteration variables so we don't flag
	// `for item in items` as `item` being an unused state cell — it
	// isn't a state, it's a binding.
	loopVars map[string]bool
	// Story tracking for the coverage hint: which components are
	// invoked from at least one story body. The rule only fires when
	// the file declares stories at all — a project that hasn't adopted
	// stories shouldn't get one hint per component.
	storyCount         int
	inStory            bool
	storyComponentUses map[string]bool
}

func newCollector() *collector {
	return &collector{
		stateDecls:         map[string]ast.Pos{},
		stateUses:          map[string]bool{},
		componentDecls:     map[string]ast.Pos{},
		componentUses:      map[string]bool{},
		loopVars:           map[string]bool{},
		storyComponentUses: map[string]bool{},
	}
}

// walk dispatches each node. Different node shapes contribute to
// declarations vs usages; some carry both (e.g. an invocation might
// reference a state cell AND be a use of a user component).
func (c *collector) walk(n *ast.Node) {
	if n == nil || n.Kind == "__error__" {
		return
	}
	switch n.Kind {
	case "view", "__root__", "module":
		c.walkChildren(n)
	case "state":
		c.recordStateDecl(n)
		c.walkChildren(n)
	case "component":
		c.recordComponentDecl(n)
		c.walkChildren(n)
	case "story":
		c.storyCount++
		prev := c.inStory
		c.inStory = true
		c.walkChildren(n)
		c.inStory = prev
	case "for":
		c.recordForUse(n)
		c.walkChildren(n)
	case "if":
		c.recordIfUse(n)
		c.walkChildren(n)
	case "assign":
		c.recordAssignUse(n)
		c.walkChildren(n)
	case "method_call":
		c.recordMethodCallUse(n)
		c.walkChildren(n)
	case "ref":
		c.recordRefUse(n)
	case "lit", "binop:+", "binop:-", "not", "list_lit",
		"splice", "field_decl", "theme", "tone_binding", "test":
		// Either no name-bearing args, or args reference loop vars /
		// step verbs (vet doesn't analyze tests yet). Recurse.
		c.walkChildren(n)
	default:
		// Anything else is an invocation: head ident might be a user
		// component, args might reference state cells.
		c.recordInvocation(n)
		c.walkChildren(n)
	}
}

func (c *collector) walkChildren(n *ast.Node) {
	for _, child := range n.Children {
		c.walk(child)
	}
	for _, h := range n.Handlers {
		c.walk(h)
	}
}

func (c *collector) recordStateDecl(n *ast.Node) {
	if len(n.Args) == 0 || n.Args[0].Kind != ast.ValueIdent {
		return
	}
	c.stateDecls[n.Args[0].String] = n.Args[0].Pos
}

func (c *collector) recordComponentDecl(n *ast.Node) {
	if len(n.Args) == 0 || n.Args[0].Kind != ast.ValueIdent {
		return
	}
	name := n.Args[0].String
	c.componentDecls[name] = n.Args[0].Pos
	// Treat each param name as a "loop var"-ish binding so refs to
	// it inside the component body don't get flagged as state uses.
	for i := 1; i < len(n.Args); i++ {
		if n.Args[i].Kind == ast.ValueIdent {
			c.loopVars[n.Args[i].String] = true
		}
	}
}

// recordForUse: `for item in items` — items is the list cell (use);
// item is a loop var (not a state cell).
func (c *collector) recordForUse(n *ast.Node) {
	if len(n.Args) >= 1 && n.Args[0].Kind == ast.ValueIdent {
		c.loopVars[n.Args[0].String] = true
	}
	if len(n.Args) >= 3 && n.Args[2].Kind == ast.ValueIdent {
		c.markStateUse(n.Args[2].String)
	}
}

func (c *collector) recordIfUse(n *ast.Node) {
	if len(n.Args) >= 1 && n.Args[0].Kind == ast.ValueIdent {
		c.markStateUse(n.Args[0].String)
	}
}

func (c *collector) recordAssignUse(n *ast.Node) {
	if len(n.Args) >= 1 && n.Args[0].Kind == ast.ValueIdent {
		// LHS may be `name` or `name.field` — strip the field.
		name := stripDot(n.Args[0].String)
		c.markStateUse(name)
	}
}

func (c *collector) recordMethodCallUse(n *ast.Node) {
	if len(n.Args) >= 1 && n.Args[0].Kind == ast.ValueIdent {
		c.markStateUse(stripDot(n.Args[0].String))
	}
}

// recordRefUse: bare ident in expression position — could be a state,
// could be a loop var. Loop vars take precedence.
func (c *collector) recordRefUse(n *ast.Node) {
	if len(n.Args) == 0 || n.Args[0].Kind != ast.ValueIdent {
		return
	}
	c.markStateUse(stripDot(n.Args[0].String))
}

// recordInvocation handles a generic invocation line: head ident might
// be a user component (mark as use), and each positional/kwarg/handler
// arg might reference a state cell.
func (c *collector) recordInvocation(n *ast.Node) {
	// Component invocation: head is the component name.
	c.componentUses[n.Kind] = true
	if c.inStory {
		c.storyComponentUses[n.Kind] = true
	}
	// Args may reference state cells (e.g. `text count`, `input draft`,
	// `iframe src=current_url`).
	for _, a := range n.Args {
		if a.Kind == ast.ValueIdent {
			c.markStateUse(stripDot(a.String))
		}
		// String literals with `${ident}` interpolation reference state.
		if a.Kind == ast.ValueString {
			c.markInterpUses(a.String)
		}
	}
	for _, v := range n.Kwargs {
		if v.Kind == ast.ValueIdent {
			c.markStateUse(stripDot(v.String))
		}
		if v.Kind == ast.ValueString {
			c.markInterpUses(v.String)
		}
	}
}

// markStateUse marks `name` as referenced — but only if it isn't a
// loop var (or a component param, which we treat the same way).
func (c *collector) markStateUse(name string) {
	if name == "" {
		return
	}
	if c.loopVars[name] {
		return
	}
	c.stateUses[name] = true
}

// markInterpUses extracts `${ident}` substrings and marks each as a
// state use. Mirrors the parseInterp logic from lower without
// re-importing it (the lower package isn't a clean dep for vet).
func (c *collector) markInterpUses(s string) {
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			j := i + 2
			for j < len(s) && s[j] != '}' {
				j++
			}
			if j < len(s) {
				ident := s[i+2 : j]
				c.markStateUse(stripDot(ident))
				i = j + 1
				continue
			}
		}
		i++
	}
}

// stripDot returns the head of a dotted reference: `item.label` →
// `item`. Used to test sub-cell refs against the state-decl table.
func stripDot(s string) string {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i]
	}
	return s
}

func (c *collector) unusedStates() []*diag.Diagnostic {
	var out []*diag.Diagnostic
	for name, pos := range c.stateDecls {
		if c.stateUses[name] {
			continue
		}
		out = append(out, &diag.Diagnostic{
			Stage:    "vet",
			Severity: "warning",
			Line:     pos.Line,
			Col:      pos.Col,
			Message:  fmt.Sprintf("state %q is declared but never used", name),
		})
	}
	return out
}

// storylessComponents flags components no story exercises — but only
// in files that declare stories at all, so adopting the first story is
// what opts a project into coverage hints.
func (c *collector) storylessComponents() []*diag.Diagnostic {
	if c.storyCount == 0 {
		return nil
	}
	var out []*diag.Diagnostic
	for name, pos := range c.componentDecls {
		if c.storyComponentUses[name] {
			continue
		}
		out = append(out, &diag.Diagnostic{
			Stage:      "vet",
			Severity:   "warning",
			Line:       pos.Line,
			Col:        pos.Col,
			Message:    fmt.Sprintf("component %q has no story", name),
			Suggestion: fmt.Sprintf("add `story \"...\" = %s ...` showing a representative state", name),
		})
	}
	return out
}

func (c *collector) unusedComponents() []*diag.Diagnostic {
	var out []*diag.Diagnostic
	for name, pos := range c.componentDecls {
		if c.componentUses[name] {
			continue
		}
		out = append(out, &diag.Diagnostic{
			Stage:    "vet",
			Severity: "warning",
			Line:     pos.Line,
			Col:      pos.Col,
			Message:  fmt.Sprintf("component %q is declared but never invoked", name),
		})
	}
	return out
}
