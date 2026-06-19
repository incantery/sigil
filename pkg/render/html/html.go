// Package html renders Sigil IR to HTML.
//
// In M2 the output is a static document plus a tiny client runtime — no
// websocket, no server session. Interactive primitives embed declarative
// Actions in data-attributes; the runtime applies them in-browser.
//
// Styling pipeline: writeNode emits *semantic* HTML (button / section /
// h1-h3 / hr / svg / …) decorated only by `class="..."` tokens that the
// resolver derives from style.SpecFor(node). There is no inline `style`
// construction here for design tokens — that's the whole point of the
// L51 lift. Two exceptions remain, and they're load-bearing rather than
// hacks: (1) the bar's reactive fill width is a per-instance computed
// value, not a token; (2) iframe width/height accept literal pixel
// values from source. Both are runtime data, not design decisions.
package html

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/incantery/mako/pkg/ir"
	"github.com/incantery/mako/pkg/style"
	"github.com/incantery/mako/pkg/ui"
)

// Render walks an IR node and writes HTML to w. The styling is
// class-based; the active stylesheet must be emitted separately (see
// WriteDoc). Tests that don't need styling can call Render directly —
// the markup is well-formed regardless of whether the stylesheet
// reaches the consumer.
//
// Icons emitted via `Render` resolve to `<use>` references with no
// backing <defs> block (since Render doesn't see the document's
// IconSets). That's fine for structure-only tests; pages that need
// rendered icons go through WriteDoc.
func Render(w io.Writer, n ir.Node) error {
	var b strings.Builder
	r := newResolver(nil)
	writeNode(&b, n, r)
	_, err := io.WriteString(w, b.String())
	return err
}

// Compile lowers a Component to a full Document (root node + cell snapshot).
// Renderers use this when they need both the tree and the initial cell map.
//
// Holds the package-level render lock so concurrent calls don't interleave
// M2's package-level cell registry. (Replaced by session-scoped state in M4.)
func Compile(c ui.Component) ir.Document {
	ui.LockRender()
	defer ui.UnlockRender()
	root := c.Build(ui.NewContext())
	cells := ui.SnapshotCells()
	return ir.Document{Root: root, Cells: cells}
}

// RenderString returns HTML for a Component. Useful in tests; does NOT include
// the cell registry or runtime, so it can't drive an interactive page on its
// own. Use WritePage for that.
func RenderString(c ui.Component) string {
	doc := Compile(c)
	var b strings.Builder
	r := newResolver(doc.IconSets)
	writeNode(&b, doc.Root, r)
	return b.String()
}

// iconUse emits the `<svg class="s-icon"><use href="#..."/></svg>`
// fragment that references one project-declared icon. The resolver's
// useIcon() records the (set, name) pair so the document-level
// <defs> block can hoist the actual SVG content once. If the set or
// name isn't registered, the renderer emits a small fallback bullet
// so the page doesn't break visually — the lowerer will already
// have errored on the unknown name, so this branch is only reached
// when the resolver was constructed without the document's
// IconSets (e.g. test-only Render calls).
func iconUse(r *resolver, set, name string) string {
	id, ok := r.useIcon(set, name)
	if !ok {
		return `<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="1.4" fill="currentColor"/></svg>`
	}
	return fmt.Sprintf(`<svg aria-hidden="true"><use href="#%s"/></svg>`, id)
}

// titleLevel returns the semantic heading element (h1/h2/h3) for a
// title's size kwarg. Defaults to h2 when no size is given — h1 is
// reserved for the page-level title (size=lg).
func titleLevel(n ir.Node) string {
	switch propStr(n, "size") {
	case "lg":
		return "h1"
	case "sm":
		return "h3"
	default:
		return "h2"
	}
}

// classAttr is the resolver helper, returning ` class="a b c"` or "".
// Kept inline (not a renderer method) so the writeNode switch stays
// flat and readable.
func classAttr(r *resolver, s style.Spec) string {
	return r.classAttr(r.classes(s))
}

func writeNode(b *strings.Builder, n ir.Node, r *resolver) {
	switch n.Kind {
	case ir.KindText:
		fmt.Fprintf(b, `<span data-sid=%q%s%s>%s</span>`,
			n.ID, classAttr(r, style.SpecFor(n)), bindAttr(n, "text"),
			html.EscapeString(propStr(n, "text")))

	case ir.KindTitle:
		tag := titleLevel(n)
		fmt.Fprintf(b, `<%s data-sid=%q%s%s>%s</%s>`,
			tag, n.ID, classAttr(r, style.SpecFor(n)), bindAttr(n, "text"),
			html.EscapeString(propStr(n, "text")), tag)

	case ir.KindCode:
		// Verbatim monospace block. `<pre>` preserves the source
		// whitespace/newlines; the themed surface comes from the class,
		// the monospace/scroll behavior from the structural `pre` rule.
		fmt.Fprintf(b, `<pre data-sid=%q%s><code>%s</code></pre>`,
			n.ID, classAttr(r, style.SpecFor(n)),
			html.EscapeString(propStr(n, "text")))

	case ir.KindCard:
		fmt.Fprintf(b, `<section data-sid=%q%s>`, n.ID, classAttr(r, style.SpecFor(n)))
		for _, c := range n.Children {
			writeNode(b, c, r)
		}
		b.WriteString(`</section>`)

	case ir.KindDivider:
		// Spec → border-color class. The <hr> gets a structural rule
		// from structuralCSS (border:0; full-width; small block
		// margin) so the class only needs to set the visible edge.
		fmt.Fprintf(b, `<hr data-sid=%q%s>`, n.ID, classAttr(r, style.SpecFor(n)))

	case ir.KindIcon:
		// Icon wrapper is a fixed structural pattern (1em square,
		// inline-flex centered) — that's a renderer concern, not a
		// design token. The s-icon class is in structuralCSS.
		// Tone overrides via Spec's Color slot apply currentColor.
		// The inner SVG is a <use> ref into the document's <defs>
		// block; one symbol per (set, name) pair, hoisted once at
		// the top of body.
		spec := style.SpecFor(n)
		classes := append([]string{"s-icon"}, r.classes(spec)...)
		fmt.Fprintf(b, `<span data-sid=%q%s>%s</span>`,
			n.ID, r.classAttr(classes),
			iconUse(r, propStr(n, "icon-set"), propStr(n, "name")))

	case ir.KindBadge:
		fmt.Fprintf(b, `<span data-sid=%q%s>%s</span>`,
			n.ID, classAttr(r, style.SpecFor(n)),
			html.EscapeString(propStr(n, "text")))

	case ir.KindBar:
		// Bar is two-element: outer track (Spec carries its
		// surface/radius), inner fill (Color tone + reactive width
		// from the bound cell). The fill's width attribute IS a
		// per-instance computed value, not a token — keep as inline
		// style. The runtime updates this width on cell change via
		// the "fill" binding (see codegen.emitBindingWriteInline).
		spec := style.SpecFor(n)
		max, _ := n.Props["max"].(int)
		if max <= 0 {
			max = 100
		}
		initial := 0
		if v, ok := n.Props["initial"].(int64); ok {
			initial = int(v)
		} else if v, ok := n.Props["initial"].(int); ok {
			initial = v
		}
		pct := 0
		if max > 0 {
			pct = (initial * 100) / max
			if pct < 0 {
				pct = 0
			} else if pct > 100 {
				pct = 100
			}
		}
		// Outer track: standard Spec classes + s-bar-track for the
		// fixed-height structural piece.
		outerClasses := append([]string{"s-bar-track"}, r.classes(spec)...)
		// Inner fill: Color slot resolved + s-bar-fill structural.
		// The fill's background comes from the Color tone (the
		// renderer maps Color → bg here because the fill IS a
		// surface; this is the one place Color paints a background).
		fillColor := "accent"
		if c := spec.Color; c != "" {
			fillColor = string(c)
		}
		fillClasses := []string{"s-bar-fill"}
		fillClasses = append(fillClasses, r.simpleClass("barfill", fillColor,
			fmt.Sprintf("background:var(--color-%s-bg);", fillColor)))
		fillAttr := ""
		if ref, ok := n.Bindings["fill"]; ok {
			fillAttr = fmt.Sprintf(` data-sigil-bind-fill=%q data-sigil-fill-max=%q`,
				ref.CellID, fmt.Sprintf("%d", max))
		}
		fmt.Fprintf(b,
			`<div data-sid=%q%s><div data-sid="%s/fill"%s style="width:%d%%"%s></div></div>`,
			n.ID, r.classAttr(outerClasses),
			n.ID, r.classAttr(fillClasses), pct, fillAttr)

	case ir.KindContainer:
		fmt.Fprintf(b, `<div data-sid=%q%s>`, n.ID, classAttr(r, style.SpecFor(n)))
		for _, c := range n.Children {
			writeNode(b, c, r)
		}
		b.WriteString(`</div>`)

	case ir.KindStack:
		fmt.Fprintf(b, `<div data-sid=%q%s>`, n.ID, classAttr(r, style.SpecFor(n)))
		for _, c := range n.Children {
			writeNode(b, c, r)
		}
		b.WriteString(`</div>`)

	case ir.KindButton:
		// Buttons can have an inline icon — render the same
		// s-icon-wrapped <use> ref the standalone icon primitive
		// uses. icon-set + icon prop pair name the qualified icon;
		// the resolver dedupes the symbol into the document's
		// <defs> block.
		iconName, hasIcon := n.Props["icon"].(string)
		iconSet, _ := n.Props["icon-set"].(string)
		iconMarkup := ""
		if hasIcon {
			iconMarkup = fmt.Sprintf(`<span class="s-icon">%s</span>`,
				iconUse(r, iconSet, iconName))
		}
		fmt.Fprintf(b, `<button type="button" data-sid=%q%s%s>%s%s</button>`,
			n.ID, classAttr(r, style.SpecFor(n)), handlerAttr(n, "click"),
			iconMarkup, html.EscapeString(propStr(n, "label")))

	case ir.KindTextInput:
		placeholderAttr := ""
		if p := propStr(n, "placeholder"); p != "" {
			placeholderAttr = fmt.Sprintf(` placeholder="%s"`, html.EscapeString(p))
		}
		inputType := "text"
		if t := propStr(n, "type"); t != "" {
			inputType = t
		}
		fmt.Fprintf(b, `<input type="%s" data-sid=%q%s%s value="%s"%s%s>`,
			html.EscapeString(inputType),
			n.ID,
			classAttr(r, style.SpecFor(n)),
			bindAttr(n, "value"),
			html.EscapeString(propStr(n, "value")),
			placeholderAttr,
			handlerAttr(n, "input"),
		)

	case ir.KindFor:
		cellID := propStr(n, "cell")
		// Vertical list with a small built-in gap. Using a Spec here
		// rather than inline style so the gap is a token reference
		// like every other layout decision.
		spec := style.Spec{Direction: style.DirColumn, Gap: 1}
		fmt.Fprintf(b, `<div data-sid=%q data-sigil-for=%q%s>`,
			n.ID, cellID, classAttr(r, spec))
		for _, c := range n.Children {
			writeNode(b, c, r)
		}
		b.WriteString(`</div>`)

	case ir.KindForItem:
		// A KindForItem is one row in a For. When Props["template"] is true,
		// wrap in an inert <template> so its content is parsed but not
		// rendered — the runtime clones it on append. Otherwise emit a live
		// row div keyed by its cell id; the runtime locates rows by that key
		// for removal and re-indexes their bindings on mount.
		cellID := propStr(n, "cell")
		isTemplate, _ := n.Props["template"].(bool)
		if isTemplate {
			b.WriteString(`<template data-sigil-for-template="">`)
			fmt.Fprintf(b, `<div data-sigil-for-item=%q>`, cellID)
			for _, c := range n.Children {
				writeNode(b, c, r)
			}
			b.WriteString(`</div>`)
			b.WriteString(`</template>`)
		} else {
			fmt.Fprintf(b, `<div data-sid=%q data-sigil-for-item=%q>`, n.ID, cellID)
			for _, c := range n.Children {
				writeNode(b, c, r)
			}
			b.WriteString(`</div>`)
		}

	case ir.KindIFrame:
		// IFrame width/height are literal per-instance values from
		// source (px or fill), not theme tokens — inline style is
		// the right vehicle for runtime data. The visual chrome
		// (border, radius, surface) comes from a Spec.
		height := 600
		if h, ok := n.Props["height"].(int); ok && h > 0 {
			height = h
		}
		widthCSS := "100%"
		if w, ok := n.Props["width"].(int); ok && w > 0 {
			widthCSS = fmt.Sprintf("%dpx", w)
		}
		srcBind := ""
		if bind, ok := n.Bindings["src"]; ok {
			srcBind = fmt.Sprintf(` data-sigil-bind-src=%q`, bind.CellID)
		}
		spec := style.Spec{Surface: "surface", Border: "outline", Radius: "md"}
		fmt.Fprintf(b,
			`<iframe data-sid=%q src=%q%s%s style="width:%s;height:%dpx"></iframe>`,
			n.ID, html.EscapeString(propStr(n, "src")), srcBind,
			classAttr(r, spec), widthCSS, height)

	case ir.KindIf:
		// Initial cell value decides whether we ship a live <div> or an inert
		// <template>. Either way the children are written into the IR-shaped
		// tree; the runtime swaps the wrapper element on cell change.
		cellID := ""
		if bref, ok := n.Bindings["visible"]; ok {
			cellID = bref.CellID
		}
		initial, _ := n.Props["initial"].(bool)
		tag := "template"
		if initial {
			tag = "div"
		}
		fmt.Fprintf(b, `<%s data-sid=%q data-sigil-if=%q>`, tag, n.ID, cellID)
		for _, c := range n.Children {
			writeNode(b, c, r)
		}
		fmt.Fprintf(b, `</%s>`, tag)

	default:
		fmt.Fprintf(b, `<!-- unknown kind: %s -->`, n.Kind)
	}
}

// bindAttr returns ` data-sigil-bind-<prop>="<cellId>"` (plus an optional
// ` data-sigil-bind-<prop>-template="..."` when the binding wraps the cell
// value in a template string).
func bindAttr(n ir.Node, prop string) string {
	bref, ok := n.Bindings[prop]
	if !ok {
		return ""
	}
	out := fmt.Sprintf(` data-sigil-bind-%s=%q`, prop, bref.CellID)
	if bref.Template != "" {
		out += fmt.Sprintf(` data-sigil-bind-%s-template=%q`, prop, bref.Template)
	}
	return out
}

// handlerAttr returns ` data-sigil-on-<event>='<json>'` (HTML-escaped JSON in
// a double-quoted attribute) when n has a handler for that event, else "".
func handlerAttr(n ir.Node, event string) string {
	a, ok := n.Handlers[event]
	if !ok {
		return ""
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return fmt.Sprintf(` data-sigil-on-%s-error=%q`, event, err.Error())
	}
	return fmt.Sprintf(` data-sigil-on-%s=%q`, event, html.EscapeString(string(raw)))
}

func propStr(n ir.Node, k string) string {
	if v, ok := n.Props[k].(string); ok {
		return v
	}
	return ""
}
