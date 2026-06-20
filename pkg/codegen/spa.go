package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/incantery/sigil/pkg/ir"
)

// EmitSPA generates a self-contained JS application that builds its DOM
// imperatively — no server-rendered HTML body needed. The result is an
// IIFE safe for embedding in a <script> tag inside a minimal HTML shell.
//
// classMap maps IR node IDs to space-separated CSS class strings. The
// HTML renderer builds it by walking the IR tree with its style resolver,
// keeping codegen renderer-agnostic. Sub-elements (e.g. bar's fill div)
// use the composite key "nodeID/fill".
func EmitSPA(doc ir.Document, classMap map[string]string) string {
	if ok, reason := Profile(doc); !ok {
		panic("codegen.EmitSPA called on unsupported doc: " + reason)
	}

	e := &spaEmitter{
		doc:           doc,
		classMap:      classMap,
		listChildIDs:  map[string]bool{},
		listParentIDs: map[string]bool{},
	}
	e.scan(doc.Root)

	var b strings.Builder
	e.b = &b
	e.emit()
	return b.String()
}

// spaEmitter holds all state for one SPA code-generation pass.
type spaEmitter struct {
	doc      ir.Document
	classMap map[string]string
	b        *strings.Builder

	// From scan phase
	forSites        []spaForSite
	listChildIDs    map[string]bool
	listParentIDs   map[string]bool
	actionsByParent map[string]map[string]bool
	usesRandomLabel bool

	// During emit
	varN int
	forN int
	ifN  int
}

type spaForSite struct {
	idx          int
	sid          string
	parentCellID string
	initialIDs   []string
	initialVals  map[string]any
	templateRow  *ir.Node
	filterCellID string
}

func (e *spaEmitter) fresh() string {
	v := fmt.Sprintf("e%d", e.varN)
	e.varN++
	return v
}

func (e *spaEmitter) nextIfIdx() int {
	n := e.ifN
	e.ifN++
	return n
}

// forSiteIdx returns the for-site index whose list parent is the given
// cell id, or -1 if the cell isn't rendered by a for-loop. Used by
// list-row streaming to address the right mkrow_/forEl_ helpers.
func (e *spaEmitter) forSiteIdx(parentCellID string) int {
	for _, fs := range e.forSites {
		if fs.parentCellID == parentCellID {
			return fs.idx
		}
	}
	return -1
}

func (e *spaEmitter) w(s string)                    { e.b.WriteString(s) }
func (e *spaEmitter) wf(format string, args ...any) { fmt.Fprintf(e.b, format, args...) }
func (e *spaEmitter) ln(indent, format string, a ...any) {
	e.b.WriteString(indent)
	fmt.Fprintf(e.b, format, a...)
	e.b.WriteByte('\n')
}

// scan walks the IR tree to collect for-sites, list-cell info, and
// determine whether randomLabel is needed.
func (e *spaEmitter) scan(root ir.Node) {
	var handlers []handlerSite

	var walk func(n ir.Node)
	walk = func(n ir.Node) {
		if n.Kind == ir.KindFor {
			parentCellID, _ := n.Props["cell"].(string)
			filterCellID, _ := n.Props["filter-cell"].(string)
			fs := spaForSite{
				idx:          len(e.forSites),
				sid:          n.ID,
				parentCellID: parentCellID,
				initialVals:  map[string]any{},
				filterCellID: filterCellID,
			}
			e.listParentIDs[parentCellID] = true
			for i := range n.Children {
				c := n.Children[i]
				if c.Kind != ir.KindForItem {
					continue
				}
				isTmpl, _ := c.Props["template"].(bool)
				if isTmpl {
					tmplCopy := c
					fs.templateRow = &tmplCopy
					continue
				}
				cellID, _ := c.Props["cell"].(string)
				if cellID != "" {
					fs.initialIDs = append(fs.initialIDs, cellID)
					e.listChildIDs[cellID] = true
					if v, ok := e.doc.Cells[cellID]; ok {
						fs.initialVals[cellID] = v
					}
				}
			}
			e.forSites = append(e.forSites, fs)
			return
		}
		for ev, a := range n.Handlers {
			handlers = append(handlers, handlerSite{sid: n.ID, event: ev, action: a})
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)

	var hasBatch func(a ir.Action) bool
	hasBatch = func(a ir.Action) bool {
		if a.Kind == "create_batch_random" {
			return true
		}
		if a.Kind == "sequence" {
			inner, _ := a.Args["actions"].([]any)
			for _, raw := range inner {
				if ia, ok := raw.(ir.Action); ok && hasBatch(ia) {
					return true
				}
			}
		}
		return false
	}
	for _, h := range handlers {
		if hasBatch(h.action) {
			e.usesRandomLabel = true
			break
		}
	}

	e.actionsByParent = actionsTargetingLists(handlers)
	// Also scan mount actions for list operations.
	for _, a := range e.doc.MountActions {
		e.scanMountAction(a)
	}
}

func (e *spaEmitter) scanMountAction(a ir.Action) {
	if a.Kind == "sequence" {
		inner, _ := a.Args["actions"].([]any)
		for _, raw := range inner {
			if ia, ok := raw.(ir.Action); ok {
				e.scanMountAction(ia)
			}
		}
		return
	}
	if a.Kind == "call_op_list" {
		set, ok := e.actionsByParent[a.CellID]
		if !ok {
			set = map[string]bool{}
			e.actionsByParent[a.CellID] = set
		}
		// call_op_list is an assignment (`cell = ListQuery()`): it
		// replaces the list, so it needs both the append helper and
		// the clear helper (clear-then-append). On first population
		// (mount) the clear is a no-op against the empty list; on a
		// refetch after a mutation it discards the stale rows so the
		// list reflects server truth instead of duplicating.
		set["append_struct_item"] = true
		set["clear_list"] = true
	}
}

// emit produces the complete JS output.
func (e *spaEmitter) emit() {
	e.w("(() => {\n")

	// Reactive update registry
	e.w("  const __updates = {};\n")
	e.w("  function __reg(cell, fn) { const a = __updates[cell] ||= []; a.push(fn); return () => { const i = a.indexOf(fn); if (i >= 0) a.splice(i, 1); }; }\n")
	e.w("  function __flush(cell) { for (const fn of __updates[cell] || []) fn(); }\n")

	if e.usesRandomLabel {
		e.w(`  const ADJ = ["pretty","large","big","small","tall","short","long","handsome","plain","quaint","clean","elegant","easy","angry","crazy","helpful","mushy","odd","unsightly","adorable","important","inexpensive","cheap","expensive","fancy"];
  const COL = ["red","yellow","blue","green","pink","brown","purple","brown","white","black","orange"];
  const NOUN = ["table","chair","house","bbq","desk","car","pony","cookie","sandwich","burger","pizza","mouse","keyboard"];
  const pick = (a) => a[(Math.random() * a.length) | 0];
  const randomLabel = () => pick(ADJ) + " " + pick(COL) + " " + pick(NOUN);
`)
	}

	// Cell declarations
	cellIDs := make([]string, 0, len(e.doc.Cells))
	for id := range e.doc.Cells {
		cellIDs = append(cellIDs, id)
	}
	sort.Strings(cellIDs)

	for _, id := range cellIDs {
		if e.listChildIDs[id] {
			continue
		}
		if e.listParentIDs[id] {
			ids := listParentIDStrings(e.doc.Cells[id])
			e.wf("  let %s = [", id)
			for i, cid := range ids {
				if i > 0 {
					e.w(", ")
				}
				e.wf("%q", cid)
			}
			e.w("];\n")
			continue
		}
		e.wf("  let %s = %s;\n", id, jsLiteral(e.doc.Cells[id]))
	}

	// Per-list state
	for _, fs := range e.forSites {
		e.wf("  const cells_%s = {", fs.parentCellID)
		for i, cid := range fs.initialIDs {
			if i > 0 {
				e.w(",")
			}
			e.wf(" %s: %s", cid, jsLiteral(fs.initialVals[cid]))
		}
		if len(fs.initialIDs) > 0 {
			e.w(" ")
		}
		e.w("};\n")
		e.wf("  const rows_%s = Object.create(null);\n", fs.parentCellID)
		e.wf("  let counter_%s = 0;\n", fs.parentCellID)
	}

	// Forward-declare for-container element vars
	for _, fs := range e.forSites {
		e.wf("  let forEl_%d;\n", fs.idx)
	}

	// For-site functions (row factory + actions)
	for _, fs := range e.forSites {
		e.emitForFunctions(fs)
	}

	// DOM build. The mounted .s-root element owns the viewport
	// unconditionally (exactly 100dvh, clipped); the page never
	// scrolls in any mode — scrolling is always an interior property.
	//
	// App shells (height=screen) ARE the viewport box, so the
	// author's shell stack is tagged .s-root directly. Every other
	// mode mounts inside a neutral codegen-owned wrapper that carries
	// .s-root + the mode class, for three reasons: (1) a comment-anchor
	// root (root-level if / lone modal / for) has no element to tag;
	// (2) a styled root (a card-rooted page) must not be stretched to
	// fill or scroll the viewport itself; (3) the doc-mode gutter must
	// live on a surface the author can't override with their own
	// padding=. full → the wrapper scrolls its interior; doc → the
	// wrapper scrolls AND carries the page gutter.
	rootVar := e.emitNode(e.doc.Root, "  ", nil)
	if rootMode(e.doc.Root) == "screen" {
		e.ln("  ", "%s.classList.add('s-root');", rootVar)
		e.ln("  ", "document.body.appendChild(%s);", rootVar)
	} else {
		modeClass := "s-root-doc"
		if rootMode(e.doc.Root) == "full" {
			modeClass = "s-root-scroll"
		}
		wrap := e.fresh()
		e.ln("  ", "const %s = document.createElement('div');", wrap)
		e.ln("  ", "%s.classList.add('s-root', '%s');", wrap, modeClass)
		e.ln("  ", "%s.appendChild(%s);", wrap, rootVar)
		e.ln("  ", "document.body.appendChild(%s);", wrap)
	}

	// Client stubs
	emitClientStubs(e.b, e.doc)

	// Mount actions: fire once after DOM is built and stubs are available.
	if len(e.doc.MountActions) > 0 {
		needsAsync := false
		for _, a := range e.doc.MountActions {
			if actionUsesAwait(a) {
				needsAsync = true
				break
			}
		}
		if needsAsync {
			e.w("  (async () => {\n")
		}
		for _, a := range e.doc.MountActions {
			e.emitActionBody(a, "    ")
		}
		if needsAsync {
			e.w("  })();\n")
		}
	}

	// Test hook
	e.w("  window.__sigil_cells = new Proxy({}, { get(_, p) {\n")
	for _, id := range cellIDs {
		if e.listChildIDs[id] {
			continue
		}
		e.wf("    if (p === %q) return %s;\n", id, id)
	}
	for _, fs := range e.forSites {
		e.wf("    if (p in cells_%s) return cells_%s[p];\n",
			fs.parentCellID, fs.parentCellID)
	}
	e.w("    return undefined;\n  }});\n")

	e.w("})();\n")
}

// ifCtx tracks whether the current emit is inside an if-site, so
// binding registrations can be paired with deregistration on unmount.
type ifCtx struct {
	deregVar string // JS variable name holding the deregister array
}

// emitNode recursively creates one IR node's DOM element (and all
// descendants), returning the JS variable name holding the element.
// The caller is responsible for appending it to the parent.
func (e *spaEmitter) emitNode(n ir.Node, ind string, ifc *ifCtx) string {
	switch n.Kind {
	case ir.KindText:
		return e.emitText(n, ind, ifc)
	case ir.KindTitle:
		return e.emitTitle(n, ind, ifc)
	case ir.KindCode:
		return e.emitCode(n, ind)
	case ir.KindCard:
		return e.emitContainerEl(n, "section", ind, ifc)
	case ir.KindStack:
		return e.emitContainerEl(n, "div", ind, ifc)
	case ir.KindContainer:
		return e.emitContainerEl(n, "div", ind, ifc)
	case ir.KindFragment:
		return e.emitContainerEl(n, "div", ind, ifc)
	case ir.KindButton:
		return e.emitButton(n, ind, ifc)
	case ir.KindTextInput:
		return e.emitTextInput(n, ind, ifc)
	case ir.KindBadge:
		return e.emitBadge(n, ind, ifc)
	case ir.KindIcon:
		return e.emitIcon(n, ind)
	case ir.KindDivider:
		return e.emitDivider(n, ind)
	case ir.KindPulse:
		return e.emitPulse(n, ind)
	case ir.KindBar:
		return e.emitBar(n, ind, ifc)
	case ir.KindIFrame:
		return e.emitIFrame(n, ind, ifc)
	case ir.KindFor:
		return e.emitFor(n, ind)
	case ir.KindIf:
		return e.emitIf(n, ind, ifc)
	case ir.KindModal:
		return e.emitModal(n, ind, ifc)
	case ir.KindRouter:
		return e.emitRouter(n, ind, ifc)
	case ir.KindRoute:
		return e.emitContainerEl(n, "div", ind, ifc)
	case ir.KindMatch:
		return e.emitMatch(n, ind, ifc)
	default:
		v := e.fresh()
		e.ln(ind, "const %s = document.createComment('unknown:%s');", v, n.Kind)
		return v
	}
}

func (e *spaEmitter) emitText(n ir.Node, ind string, ifc *ifCtx) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement('span');", v)
	e.setClass(v, n.ID, ind)
	if ref, ok := n.Bindings["text"]; ok {
		expr := jsTextExpr(ref.CellID, ref.Template)
		e.ln(ind, "%s.textContent = %s;", v, expr)
		e.regUpdate(ref.CellID, fmt.Sprintf("%s.textContent = %s;", v, expr), ind, ifc)
	} else {
		e.ln(ind, "%s.textContent = %s;", v, jsQuote(nodePropStr(n, "text")))
	}
	return v
}

func (e *spaEmitter) emitTitle(n ir.Node, ind string, ifc *ifCtx) string {
	tag := spaTitleTag(n)
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement(%s);", v, jsQuote(tag))
	e.setClass(v, n.ID, ind)
	if ref, ok := n.Bindings["text"]; ok {
		expr := jsTextExpr(ref.CellID, ref.Template)
		e.ln(ind, "%s.textContent = %s;", v, expr)
		e.regUpdate(ref.CellID, fmt.Sprintf("%s.textContent = %s;", v, expr), ind, ifc)
	} else {
		e.ln(ind, "%s.textContent = %s;", v, jsQuote(nodePropStr(n, "text")))
	}
	return v
}

// emitCode builds a verbatim monospace code block: `<pre><code>…</code></pre>`.
// Content is static (the lower stage never produces a binding for a code
// node), so there is nothing reactive to register — just set textContent
// once. `<pre>` preserves the source whitespace and line breaks; the themed
// surface/border come from the resolver class, the monospace/scroll behavior
// from the structural `pre` rule.
func (e *spaEmitter) emitCode(n ir.Node, ind string) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement('pre');", v)
	e.setClass(v, n.ID, ind)
	code := e.fresh()
	e.ln(ind, "const %s = document.createElement('code');", code)
	e.ln(ind, "%s.textContent = %s;", code, jsQuote(nodePropStr(n, "text")))
	e.ln(ind, "%s.appendChild(%s);", v, code)
	return v
}

// rootMode classifies the view root's viewport mode: "screen" (app
// shell — exactly the viewport, interior scroll=y regions own all
// scrolling), "full" (root scrolls its own interior), or "doc"
// (document-style: root scrolls and carries the page gutter).
// height=screen/full is read from the root or a direct child, since
// a view with sibling modals gets wrapped in a synthetic root stack
// and the author's shell sits one level down.
func rootMode(root ir.Node) string {
	rootHeight := func(n ir.Node) string {
		h, _ := n.Props["height"].(string)
		return h
	}
	mode := rootHeight(root)
	if mode != "screen" && mode != "full" {
		for _, c := range root.Children {
			if h := rootHeight(c); h == "screen" || h == "full" {
				mode = h
				break
			}
		}
	}
	switch mode {
	case "screen", "full":
		return mode
	default:
		return "doc"
	}
}

func (e *spaEmitter) emitContainerEl(n ir.Node, tag, ind string, ifc *ifCtx) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement(%s);", v, jsQuote(tag))
	e.setClass(v, n.ID, ind)
	for _, c := range n.Children {
		cv := e.emitNode(c, ind, ifc)
		e.ln(ind, "%s.appendChild(%s);", v, cv)
	}
	if a, ok := n.Handlers["click"]; ok {
		e.ln(ind, "%s.style.cursor = 'pointer';", v)
		e.emitHandler(v, "click", a, ind)
	}
	if matchCell, ok := n.Props["match-cell"].(string); ok {
		matchVal, _ := n.Props["match-value"].(string)
		initExpr := fmt.Sprintf("%s === %s", matchCell, jsQuote(matchVal))
		e.ln(ind, "%s.dataset.active = String(%s);", v, initExpr)
		updCode := fmt.Sprintf("%s.dataset.active = String(%s === %s);", v, matchCell, jsQuote(matchVal))
		e.regUpdate(matchCell, updCode, ind, ifc)
	}
	if n.Props["anchor"] == "end" {
		// anchor=end: follow the newest content. A scroll listener
		// tracks whether the user is at the bottom (40px slop absorbs
		// sub-row jitter); a MutationObserver re-pins on any content
		// change while following. Programmatic pins land back at the
		// bottom, so they recompute follow=true — only a real upward
		// scroll releases the pin, and scrolling back down re-arms it.
		// Observer callbacks are microtasks, so the initial mount's
		// mutations pin the transcript to its end on first paint too.
		e.ln(ind, "let %s_follow = true;", v)
		e.ln(ind, "%s.addEventListener('scroll', () => { %s_follow = (%s.scrollHeight - %s.scrollTop - %s.clientHeight) < 40; });",
			v, v, v, v, v)
		e.ln(ind, "new MutationObserver(() => { if (%s_follow) %s.scrollTop = %s.scrollHeight; }).observe(%s, { childList: true, subtree: true, characterData: true });",
			v, v, v, v)
	}
	return v
}

func (e *spaEmitter) emitButton(n ir.Node, ind string, ifc *ifCtx) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement('button');", v)
	e.ln(ind, "%s.type = 'button';", v)
	e.setClass(v, n.ID, ind)

	// Inline icon
	if iconName, ok := n.Props["icon"].(string); ok {
		iconSet, _ := n.Props["icon-set"].(string)
		iv := e.fresh()
		e.ln(ind, "const %s = document.createElement('span');", iv)
		e.ln(ind, "%s.className = 's-icon';", iv)
		e.ln(ind, "%s.innerHTML = '<svg aria-hidden=\"true\"><use href=\"#sigil-icon-%s-%s\"/></svg>';",
			iv, iconSet, iconName)
		e.ln(ind, "%s.appendChild(%s);", v, iv)
	}

	label := nodePropStr(n, "label")
	if label != "" {
		e.ln(ind, "%s.appendChild(document.createTextNode(%s));", v, jsQuote(label))
	} else if iconName, ok := n.Props["icon"].(string); ok {
		// Icon-only button: the icon SVG is aria-hidden, so without a
		// label the button has no accessible name at all. The icon
		// name becomes the aria-label — screen readers announce it,
		// and scenario tests target it (click button "send").
		e.ln(ind, "%s.setAttribute('aria-label', %s);", v, jsQuote(iconName))
	}

	if ref, ok := n.Bindings["disabled"]; ok {
		e.ln(ind, "%s.disabled = !!%s;", v, ref.CellID)
		e.regUpdate(ref.CellID, fmt.Sprintf("%s.disabled = !!%s;", v, ref.CellID), ind, ifc)
	}

	if a, ok := n.Handlers["click"]; ok {
		e.emitHandler(v, "click", a, ind)
	}
	return v
}

func (e *spaEmitter) emitTextInput(n ir.Node, ind string, ifc *ifCtx) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement('input');", v)
	inputType := "text"
	if t := nodePropStr(n, "type"); t != "" {
		inputType = t
	}
	e.ln(ind, "%s.type = %s;", v, jsQuote(inputType))
	e.setClass(v, n.ID, ind)

	if p := nodePropStr(n, "placeholder"); p != "" {
		e.ln(ind, "%s.placeholder = %s;", v, jsQuote(p))
	}

	if ref, ok := n.Bindings["value"]; ok {
		expr := jsTextExpr(ref.CellID, ref.Template)
		e.ln(ind, "%s.value = %s;", v, expr)
		upd := fmt.Sprintf("{ const v = %s; if (%s.value !== v) %s.value = v; }", expr, v, v)
		e.regUpdate(ref.CellID, upd, ind, ifc)
	} else {
		e.ln(ind, "%s.value = %s;", v, jsQuote(nodePropStr(n, "value")))
	}

	if a, ok := n.Handlers["input"]; ok {
		e.emitHandler(v, "input", a, ind)
	}
	return v
}

// emitIFrame creates an <iframe>. Width/height are literal
// per-instance values from source (px, or fill-width by default), not
// theme tokens — inline style carries them, matching the SSR renderer.
// A bound src follows its cell; the raw-attribute comparison avoids
// reloading the frame when a handler re-sets the same value.
func (e *spaEmitter) emitIFrame(n ir.Node, ind string, ifc *ifCtx) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement('iframe');", v)
	e.setClass(v, n.ID, ind)
	if w, ok := n.Props["width"].(int); ok && w > 0 {
		e.ln(ind, "%s.style.width = '%dpx';", v, w)
	} else {
		e.ln(ind, "%s.style.width = '100%%';", v)
	}
	height := 600
	if h, ok := n.Props["height"].(int); ok && h > 0 {
		height = h
	}
	e.ln(ind, "%s.style.height = '%dpx';", v, height)
	if ref, ok := n.Bindings["src"]; ok {
		expr := jsTextExpr(ref.CellID, ref.Template)
		e.ln(ind, "%s.setAttribute('src', %s);", v, expr)
		upd := fmt.Sprintf("{ const v = %s; if (%s.getAttribute('src') !== v) %s.setAttribute('src', v); }", expr, v, v)
		e.regUpdate(ref.CellID, upd, ind, ifc)
	} else if src := nodePropStr(n, "src"); src != "" {
		e.ln(ind, "%s.setAttribute('src', %s);", v, jsQuote(src))
	}
	return v
}

func (e *spaEmitter) emitBadge(n ir.Node, ind string, ifc *ifCtx) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement('span');", v)
	e.setClass(v, n.ID, ind)
	if bref, ok := n.Bindings["text"]; ok {
		expr := jsTextExpr(bref.CellID, bref.Template)
		e.ln(ind, "%s.textContent = %s;", v, expr)
		e.regUpdate(bref.CellID, fmt.Sprintf("%s.textContent = %s;", v, expr), ind, ifc)
	} else {
		e.ln(ind, "%s.textContent = %s;", v, jsQuote(nodePropStr(n, "text")))
	}
	return v
}

func (e *spaEmitter) emitIcon(n ir.Node, ind string) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement('span');", v)
	e.setClass(v, n.ID, ind)

	set := nodePropStr(n, "icon-set")
	name := nodePropStr(n, "name")
	e.ln(ind, "%s.innerHTML = '<svg aria-hidden=\"true\"><use href=\"#sigil-icon-%s-%s\"/></svg>';", v, set, name)
	return v
}

func (e *spaEmitter) emitDivider(n ir.Node, ind string) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement('hr');", v)
	e.setClass(v, n.ID, ind)
	return v
}

// emitPulse creates the working/streaming affordance: a span hosting
// three <i> dots whose stagger lives in structural CSS. Decorative —
// the adjacent caption carries the semantics, so it's aria-hidden.
func (e *spaEmitter) emitPulse(n ir.Node, ind string) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement('span');", v)
	e.setClass(v, n.ID, ind)
	e.ln(ind, "%s.setAttribute('aria-hidden', 'true');", v)
	e.ln(ind, "%s.innerHTML = '<i></i><i></i><i></i>';", v)
	return v
}

func (e *spaEmitter) emitBar(n ir.Node, ind string, ifc *ifCtx) string {
	v := e.fresh()
	e.ln(ind, "const %s = document.createElement('div');", v)
	e.setClass(v, n.ID, ind)

	max, _ := n.Props["max"].(int)
	if max <= 0 {
		max = 100
	}
	initial := 0
	if vi, ok := n.Props["initial"].(int64); ok {
		initial = int(vi)
	} else if vi, ok := n.Props["initial"].(int); ok {
		initial = vi
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

	fv := e.fresh()
	e.ln(ind, "const %s = document.createElement('div');", fv)
	fillCls := e.classMap[n.ID+"/fill"]
	if fillCls != "" {
		e.ln(ind, "%s.className = %s;", fv, jsQuote(fillCls))
	}
	e.ln(ind, "%s.style.width = '%d%%';", fv, pct)

	if ref, ok := n.Bindings["fill"]; ok {
		upd := fmt.Sprintf("{ const p = Math.max(0, Math.min(100, Math.round((%s) * 100 / %d))); %s.style.width = p + '%%'; }",
			ref.CellID, max, fv)
		e.regUpdate(ref.CellID, upd, ind, ifc)
	}
	e.ln(ind, "%s.appendChild(%s);", v, fv)
	return v
}

func (e *spaEmitter) emitFor(n ir.Node, ind string) string {
	// Find the matching for-site from scan
	var fs *spaForSite
	for i := range e.forSites {
		if e.forSites[i].sid == n.ID {
			fs = &e.forSites[i]
			break
		}
	}
	if fs == nil {
		v := e.fresh()
		e.ln(ind, "const %s = document.createComment('for-site-error');", v)
		return v
	}

	containerVar := fmt.Sprintf("forEl_%d", fs.idx)
	e.ln(ind, "%s = document.createElement('div');", containerVar)
	e.setClass(containerVar, n.ID, ind)

	// Mount initial rows
	e.ln(ind, "for (const __id of %s) %s.appendChild(mkrow_%d(__id));",
		fs.parentCellID, containerVar, fs.idx)

	// Register filter update if present
	if fs.filterCellID != "" {
		e.ln(ind, "__reg(%s, () => { filter_%s(); });", jsQuote(fs.filterCellID), fs.parentCellID)
	}

	return containerVar
}

func (e *spaEmitter) emitIf(n ir.Node, ind string, outerIfc *ifCtx) string {
	bref := n.Bindings["visible"]
	initial, _ := n.Props["initial"].(bool)
	idx := e.nextIfIdx()

	anchor := e.fresh()
	elVar := fmt.Sprintf("if%d_el", idx)
	mountedVar := fmt.Sprintf("if%d_mounted", idx)
	deregVar := fmt.Sprintf("if%d_dereg", idx)

	e.ln(ind, "const %s = document.createComment('');", anchor)
	e.ln(ind, "let %s = null;", elVar)
	e.ln(ind, "let %s = %t;", mountedVar, initial)
	e.ln(ind, "let %s = [];", deregVar)

	innerIfc := &ifCtx{deregVar: deregVar}
	inner := ind + "  "

	// Mount function
	e.ln(ind, "function mount_if%d() {", idx)
	wrapVar := e.fresh()
	e.ln(inner, "const %s = document.createElement('div');", wrapVar)
	// The conditional wrapper is layout-transparent (display:contents)
	// so its children participate in the real flex container above —
	// otherwise width=fill / height=fill inside an `if` would resolve
	// against an inert block and silently no-op. The wrapper still
	// exists in the DOM for mount/unmount, it just contributes no box.
	e.ln(inner, "%s.style.display = 'contents';", wrapVar)
	for _, c := range n.Children {
		cv := e.emitNode(c, inner, innerIfc)
		e.ln(inner, "%s.appendChild(%s);", wrapVar, cv)
	}
	e.ln(inner, "%s = %s;", elVar, wrapVar)
	e.ln(inner, "%s.parentNode.insertBefore(%s, %s);", anchor, wrapVar, anchor)
	e.ln(inner, "%s = true;", mountedVar)
	e.ln(ind, "}")

	// Unmount function
	e.ln(ind, "function unmount_if%d() {", idx)
	e.ln(inner, "for (const d of %s) d();", deregVar)
	e.ln(inner, "%s = [];", deregVar)
	e.ln(inner, "if (%s) %s.remove();", elVar, elVar)
	e.ln(inner, "%s = null;", elVar)
	e.ln(inner, "%s = false;", mountedVar)
	e.ln(ind, "}")

	// Initial mount
	e.ln(ind, "if (%s) mount_if%d();", mountedVar, idx)

	// Visibility update registration
	updBody := fmt.Sprintf("const want = !!%s; if (want !== %s) { if (want) mount_if%d(); else unmount_if%d(); }",
		bref.CellID, mountedVar, idx, idx)
	e.regUpdate(bref.CellID, updBody, ind, outerIfc)

	return anchor
}

// emitMatch renders a discriminated-union `match`: an anchor comment
// plus one mount/unmount block per arm (like a fan of `if`s keyed on
// the union cell's tag). On the union cell changing, m<idx>_apply
// mounts the arm whose variant matches the current tag and unmounts
// the rest; if the active arm stays the same but its payload changed,
// the arm's binding cells are refreshed (and flushed) in place. The
// `as` payload binding flows through real cells the arm subtree reads
// with the ordinary binding machinery.
func (e *spaEmitter) emitMatch(n ir.Node, ind string, outerIfc *ifCtx) string {
	cellID, _ := n.Props["cell"].(string)
	tagged, _ := n.Props["tagged"].(bool)
	idx := e.nextIfIdx()
	// A display:contents container (not an anchor) holds the active
	// arm: the initial apply() runs at build time, before the parent
	// has attached this node, so arms must mount with appendChild on a
	// node that works while detached — exactly what `for` does. Only
	// one arm is ever mounted, so child order is moot.
	container := e.fresh()
	e.ln(ind, "const %s = document.createElement('div');", container)
	e.ln(ind, "%s.style.display = 'contents';", container)

	type armInfo struct {
		variant    string
		node       ir.Node
		el, mnt    string
		dereg      string
		bindCell   string
		bindLeaves [][2]string // {dotted path on payload, leaf cell id}
	}
	arms := make([]armInfo, 0, len(n.Children))
	for ai, arm := range n.Children {
		a := armInfo{
			variant: propStr2(arm, "variant"),
			node:    arm,
			el:      fmt.Sprintf("m%d_%d_el", idx, ai),
			mnt:     fmt.Sprintf("m%d_%d_mounted", idx, ai),
			dereg:   fmt.Sprintf("m%d_%d_dereg", idx, ai),
		}
		a.bindCell, _ = arm.Props["bindCell"].(string)
		if leaves, ok := arm.Props["bindLeaves"].([]any); ok {
			for _, raw := range leaves {
				m, _ := raw.(map[string]any)
				p, _ := m["path"].(string)
				c, _ := m["cell"].(string)
				a.bindLeaves = append(a.bindLeaves, [2]string{p, c})
			}
		}
		arms = append(arms, a)
	}

	for _, a := range arms {
		e.ln(ind, "let %s = null;", a.el)
		e.ln(ind, "let %s = false;", a.mnt)
		e.ln(ind, "let %s = [];", a.dereg)
	}
	inner := ind + "  "

	refresh := func(a armInfo, at string) {
		// Copy the active variant's payload into the arm's binding
		// cell(s) and flush so the subtree reflects it. Only called when
		// the tag matches, so cellID.value is the right shape.
		if a.bindCell != "" {
			e.ln(at, "%s = %s ? %s.value : %s;", a.bindCell, cellID, cellID, a.bindCell)
			e.ln(at, "__flush(%s);", jsQuote(a.bindCell))
		}
		for _, lf := range a.bindLeaves {
			e.ln(at, "%s = (%s && %s.value != null) ? %s.value.%s : %s;", lf[1], cellID, cellID, cellID, lf[0], lf[1])
			e.ln(at, "__flush(%s);", jsQuote(lf[1]))
		}
	}

	for ai, a := range arms {
		e.ln(ind, "function m%d_%d_refresh() {", idx, ai)
		refresh(a, inner)
		e.ln(ind, "}")

		e.ln(ind, "function m%d_%d_mount() {", idx, ai)
		e.ln(inner, "m%d_%d_refresh();", idx, ai)
		wrap := e.fresh()
		e.ln(inner, "const %s = document.createElement('div');", wrap)
		e.ln(inner, "%s.style.display = 'contents';", wrap)
		armIfc := &ifCtx{deregVar: a.dereg}
		for _, c := range a.node.Children {
			cv := e.emitNode(c, inner, armIfc)
			e.ln(inner, "%s.appendChild(%s);", wrap, cv)
		}
		e.ln(inner, "%s = %s;", a.el, wrap)
		e.ln(inner, "%s.appendChild(%s);", container, wrap)
		e.ln(inner, "%s = true;", a.mnt)
		e.ln(ind, "}")

		e.ln(ind, "function m%d_%d_unmount() {", idx, ai)
		e.ln(inner, "for (const d of %s) d();", a.dereg)
		e.ln(inner, "%s = [];", a.dereg)
		e.ln(inner, "if (%s) %s.remove();", a.el, a.el)
		e.ln(inner, "%s = null;", a.el)
		e.ln(inner, "%s = false;", a.mnt)
		e.ln(ind, "}")
	}

	e.ln(ind, "function m%d_apply() {", idx)
	// Tagged unions discriminate on `.tag`; a plain enum's cell IS the
	// variant-name string.
	if tagged {
		e.ln(inner, "const __t = %s ? %s.tag : null;", cellID, cellID)
	} else {
		e.ln(inner, "const __t = %s;", cellID)
	}
	for ai, a := range arms {
		e.ln(inner, "if (__t === %s) { if (!%s) m%d_%d_mount(); else m%d_%d_refresh(); } else if (%s) m%d_%d_unmount();",
			jsQuote(a.variant), a.mnt, idx, ai, idx, ai, a.mnt, idx, ai)
	}
	e.ln(ind, "}")
	e.ln(ind, "m%d_apply();", idx)
	e.regUpdate(cellID, fmt.Sprintf("m%d_apply();", idx), ind, outerIfc)
	return container
}

// propStr2 reads a string prop off an ir.Node ("" if absent).
func propStr2(n ir.Node, key string) string {
	s, _ := n.Props[key].(string)
	return s
}

func (e *spaEmitter) emitModal(n ir.Node, ind string, outerIfc *ifCtx) string {
	bref := n.Bindings["visible"]
	initial, _ := n.Props["initial"].(bool)
	idx := e.nextIfIdx()

	anchor := e.fresh()
	e.ln(ind, "const %s = document.createComment('');", anchor)

	backdropVar := fmt.Sprintf("modal%d_backdrop", idx)
	contentVar := fmt.Sprintf("modal%d_content", idx)
	mountedVar := fmt.Sprintf("modal%d_mounted", idx)
	deregVar := fmt.Sprintf("modal%d_dereg", idx)

	e.ln(ind, "let %s = null;", backdropVar)
	e.ln(ind, "let %s = null;", contentVar)
	e.ln(ind, "let %s = %t;", mountedVar, initial)
	e.ln(ind, "let %s = [];", deregVar)

	innerIfc := &ifCtx{deregVar: deregVar}
	inner := ind + "  "

	// Mount: create backdrop + content. side=bottom/left add a
	// placement modifier class; the structural CSS turns the centered
	// dialog into a bottom sheet or left drawer.
	backdropClass := "s-modal-backdrop"
	if side, _ := n.Props["side"].(string); side != "" {
		backdropClass += " s-modal-side-" + side
	}
	e.ln(ind, "function mount_modal%d() {", idx)
	e.ln(inner, "%s = document.createElement('div');", backdropVar)
	e.ln(inner, "%s.className = %s;", backdropVar, jsQuote(backdropClass))
	e.ln(inner, "%s = document.createElement('div');", contentVar)
	e.ln(inner, "%s.className = 's-modal-content';", contentVar)
	for _, c := range n.Children {
		cv := e.emitNode(c, inner, innerIfc)
		e.ln(inner, "%s.appendChild(%s);", contentVar, cv)
	}
	e.ln(inner, "%s.appendChild(%s);", backdropVar, contentVar)
	// Click backdrop to close
	e.ln(inner, "%s.onclick = (ev) => { if (ev.target === %s) { %s = false; __flush(%s); } };",
		backdropVar, backdropVar, bref.CellID, jsQuote(bref.CellID))
	e.ln(inner, "document.body.appendChild(%s);", backdropVar)
	e.ln(inner, "%s = true;", mountedVar)
	// Escape key to close
	e.ln(inner, "const esc = (ev) => { if (ev.key === 'Escape') { %s = false; __flush(%s); } };",
		bref.CellID, jsQuote(bref.CellID))
	e.ln(inner, "document.addEventListener('keydown', esc);")
	e.ln(inner, "%s.push(() => document.removeEventListener('keydown', esc));", deregVar)
	e.ln(ind, "}")

	// Unmount
	e.ln(ind, "function unmount_modal%d() {", idx)
	e.ln(inner, "for (const d of %s) d();", deregVar)
	e.ln(inner, "%s = [];", deregVar)
	e.ln(inner, "if (%s) %s.remove();", backdropVar, backdropVar)
	e.ln(inner, "%s = null;", backdropVar)
	e.ln(inner, "%s = null;", contentVar)
	e.ln(inner, "%s = false;", mountedVar)
	e.ln(ind, "}")

	// Initial mount
	e.ln(ind, "if (%s) mount_modal%d();", mountedVar, idx)

	// Toggle on cell change
	updBody := fmt.Sprintf("const want = !!%s; if (want !== %s) { if (want) mount_modal%d(); else unmount_modal%d(); }",
		bref.CellID, mountedVar, idx, idx)
	e.regUpdate(bref.CellID, updBody, ind, outerIfc)

	return anchor
}

func (e *spaEmitter) emitRouter(n ir.Node, ind string, outerIfc *ifCtx) string {
	// No `active` cell binding ⇒ path-driven router (History API).
	if _, ok := n.Bindings["active"]; !ok {
		return e.emitRouterPath(n, ind, outerIfc)
	}
	bref := n.Bindings["active"]
	initial, _ := n.Props["initial"].(string)
	idx := e.nextIfIdx()

	container := e.fresh()
	e.ln(ind, "const %s = document.createElement('div');", container)
	e.setClass(container, n.ID, ind)

	activeVar := fmt.Sprintf("route%d_active", idx)
	routeElsVar := fmt.Sprintf("route%d_els", idx)
	mountFnsVar := fmt.Sprintf("route%d_mount", idx)

	e.ln(ind, "let %s = %s;", activeVar, jsQuote(initial))
	e.ln(ind, "const %s = {};", routeElsVar)
	e.ln(ind, "const %s = {};", mountFnsVar)

	for _, c := range n.Children {
		if c.Kind != ir.KindRoute {
			continue
		}
		routeName, _ := c.Props["name"].(string)

		e.ln(ind, "%s[%s] = () => {", mountFnsVar, jsQuote(routeName))
		inner := ind + "  "
		wrapVar := e.fresh()
		e.ln(inner, "const %s = document.createElement('div');", wrapVar)
		e.setClass(wrapVar, c.ID, inner)
		for _, gc := range c.Children {
			cv := e.emitNode(gc, inner, outerIfc)
			e.ln(inner, "%s.appendChild(%s);", wrapVar, cv)
		}
		e.ln(inner, "%s[%s] = %s;", routeElsVar, jsQuote(routeName), wrapVar)
		e.ln(inner, "%s.appendChild(%s);", container, wrapVar)
		e.ln(ind, "};")
	}

	// Read initial route from URL hash (e.g. #runs → "runs")
	e.ln(ind, "{ const h = location.hash.slice(1); if (h && %s[h]) { %s = h; %s = h; } }",
		mountFnsVar, bref.CellID, activeVar)

	// Mount initial route
	e.ln(ind, "if (%s[%s]) %s[%s]();", mountFnsVar, activeVar, mountFnsVar, activeVar)

	// Register update: swap routes on cell change + sync hash
	swapCode := fmt.Sprintf(
		"const next = %s; if (next !== %s) { if (%s[%s]) %s[%s].remove(); %s = next; if (%s[next]) %s[next](); location.hash = next; }",
		bref.CellID, activeVar, routeElsVar, activeVar, routeElsVar, activeVar,
		activeVar, mountFnsVar, mountFnsVar)
	e.regUpdate(bref.CellID, swapCode, ind, outerIfc)

	// Listen for browser back/forward navigation
	e.ln(ind, "window.addEventListener('hashchange', () => {")
	e.ln(ind+"  ", "const h = location.hash.slice(1);")
	e.ln(ind+"  ", "if (h && h !== %s && %s[h]) { %s = h; __flush(%s); }",
		activeVar, mountFnsVar, bref.CellID, jsQuote(bref.CellID))
	e.ln(ind, "});")

	return container
}

// emitRouterPath emits a path-driven router: the active route is the one
// whose `path` facet matches location.pathname. Navigation is History-API
// based — `window.__sigilNav(path)` pushes state and re-renders without a
// full page load, and a popstate listener handles browser back/forward.
// The `navigate` action calls __sigilNav when a path router is mounted and
// falls back to a full load otherwise (see navTo).
// collectRouteNodes flattens a path router's children into its route set,
// descending through `group` nodes (a group is a routing container that
// inherits its facets to members; codegen treats its routes as the router's
// own — the inherited guards are already baked into each route's Props).
func collectRouteNodes(children []ir.Node) []ir.Node {
	var out []ir.Node
	for _, c := range children {
		switch c.Kind {
		case ir.KindRoute:
			out = append(out, c)
		case ir.KindGroup:
			out = append(out, collectRouteNodes(c.Children)...)
		}
	}
	return out
}

func (e *spaEmitter) emitRouterPath(n ir.Node, ind string, outerIfc *ifCtx) string {
	idx := e.nextIfIdx()

	container := e.fresh()
	e.ln(ind, "const %s = document.createElement('div');", container)
	e.setClass(container, n.ID, ind)

	tableVar := fmt.Sprintf("route%d_table", idx)
	activeVar := fmt.Sprintf("route%d_el", idx)
	matchFn := fmt.Sprintf("route%d_match", idx)
	e.ln(ind, "let %s = null;", activeVar)
	e.ln(ind, "const %s = [];", tableVar)

	// Per-router path matcher: compares pattern segments to the pathname,
	// extracting `:param` segments into a name→value map. Returns null on a
	// length or literal-segment mismatch.
	e.ln(ind, "const %s = (pat, p) => {", matchFn)
	mi := ind + "  "
	e.ln(mi, "const ps = pat.split('/'), xs = p.split('/');")
	e.ln(mi, "if (ps.length !== xs.length) return null;")
	e.ln(mi, "const out = {};")
	e.ln(mi, "for (let i = 0; i < ps.length; i++) {")
	e.ln(mi+"  ", "if (ps[i][0] === ':') out[ps[i].slice(1)] = decodeURIComponent(xs[i]);")
	e.ln(mi+"  ", "else if (ps[i] !== xs[i]) return null;")
	e.ln(mi, "}")
	e.ln(mi, "return out;")
	e.ln(ind, "};")

	routes := collectRouteNodes(n.Children)
	for _, c := range routes {
		pathPat, _ := c.Props["path"].(string)
		if pathPat == "" {
			// A route with no path can never match in path-mode; skip it
			// rather than mount it on every URL.
			continue
		}
		// setp seeds this route's param cells from the matched segments
		// before mount, so the view's bindings render with the right values.
		e.ln(ind, "%s.push({ path: %s, setp: (m) => {", tableVar, jsQuote(pathPat))
		sp := ind + "  "
		if raw, ok := c.Props["params"].([]any); ok {
			for _, r := range raw {
				m, ok := r.(map[string]any)
				if !ok {
					continue
				}
				name, _ := m["name"].(string)
				cell, _ := m["cell"].(string)
				if name == "" || cell == "" {
					continue
				}
				e.ln(sp, "%s = m[%s]; __flush(%s);", cell, jsQuote(name), jsQuote(cell))
			}
		}
		// guard runs after setp (so it can read `:param` cells) and before
		// mount. It returns true to admit, false to redirect. A falsy op
		// result or a thrown error (e.g. a 401 from an auth-gated query)
		// both deny. A public route has no guards and always admits.
		e.ln(ind, "}, guard: async () => {")
		gi := ind + "  "
		guardsRaw, _ := c.Props["guards"].([]any)
		if len(guardsRaw) > 0 {
			e.ln(gi, "try {")
			for _, gr := range guardsRaw {
				gm, ok := gr.(map[string]any)
				if !ok {
					continue
				}
				op, _ := gm["op"].(string)
				e.ln(gi+"  ", "if (!(await %s)) return false;", jsOpCallExpr(op, gm["args"]))
			}
			e.ln(gi, "} catch (e) { return false; }")
		}
		e.ln(gi, "return true;")
		e.ln(ind, "}, mount: () => {")
		inner := ind + "  "
		wrapVar := e.fresh()
		e.ln(inner, "const %s = document.createElement('div');", wrapVar)
		e.setClass(wrapVar, c.ID, inner)
		for _, gc := range c.Children {
			cv := e.emitNode(gc, inner, outerIfc)
			e.ln(inner, "%s.appendChild(%s);", wrapVar, cv)
		}
		e.ln(inner, "return %s;", wrapVar)
		e.ln(ind, "} });")
	}

	// A guard denial redirects to the first public route's path (the login
	// surface, by convention), or "/" when none is declared. The target is
	// public, so its own guard admits and there is no redirect loop.
	redirect := "/"
	for _, c := range routes {
		if pub, _ := c.Props["public"].(bool); pub {
			if p, _ := c.Props["path"].(string); p != "" {
				redirect = p
				break
			}
		}
	}

	renderFn := fmt.Sprintf("route%d_render", idx)
	e.ln(ind, "const %s = async () => {", renderFn)
	in := ind + "  "
	e.ln(in, "const __p = location.pathname;")
	e.ln(in, "let __m = null, __r = null;")
	e.ln(in, "for (const x of %s) { const mm = %s(x.path, __p); if (mm) { __m = mm; __r = x; break; } }", tableVar, matchFn)
	e.ln(in, "if (%s) { %s.remove(); %s = null; }", activeVar, activeVar, activeVar)
	e.ln(in, "if (!__r) return;")
	e.ln(in, "__r.setp(__m);")
	e.ln(in, "if (!(await __r.guard())) { window.__sigilNav(%s); return; }", jsQuote(redirect))
	e.ln(in, "%s = __r.mount(); %s.appendChild(%s);", activeVar, container, activeVar)
	e.ln(ind, "};")

	// Client-side navigation seam: pushState + re-render. Exposed globally
	// so the `navigate` action routes client-side when a path router is
	// mounted, and a full load otherwise.
	e.ln(ind, "window.__sigilNav = (path) => { if (path !== location.pathname) history.pushState({}, '', path); %s(); };", renderFn)
	e.ln(ind, "window.addEventListener('popstate', %s);", renderFn)
	e.ln(ind, "%s();", renderFn)

	return container
}

// --- handler emission ---

func (e *spaEmitter) emitHandler(elemVar, event string, a ir.Action, ind string) {
	asyncMod := ""
	if actionUsesAwait(a) {
		asyncMod = "async "
	}
	e.ln(ind, "%s.on%s = %s(event) => {", elemVar, event, asyncMod)
	e.emitActionBody(a, ind+"  ")
	e.ln(ind, "};")
}

func (e *spaEmitter) emitActionBody(a ir.Action, ind string) {
	if a.Kind == "sequence" {
		inner, _ := a.Args["actions"].([]any)
		for _, raw := range inner {
			if ia, ok := raw.(ir.Action); ok {
				e.emitActionBody(ia, ind)
			}
		}
		return
	}

	switch a.Kind {
	case "set":
		e.ln(ind, "%s = %s;", a.CellID, jsActionArg(a.Args["value"]))
	case "set_variant":
		// Union/enum construction. A payload-carrying union builds the
		// tagged `{tag, value}` shape (unit variants carry a null value);
		// a plain enum is just the variant name string.
		tag, _ := a.Args["tag"].(string)
		if tagged, _ := a.Args["tagged"].(bool); !tagged {
			e.ln(ind, "%s = %s;", a.CellID, jsQuote(tag))
			break
		}
		payload := "null"
		if p, ok := a.Args["payload"]; ok {
			payload = jsActionArg(p)
		}
		e.ln(ind, "%s = { tag: %s, value: %s };", a.CellID, jsQuote(tag), payload)
	case "add":
		e.ln(ind, "%s = %s + %s;", a.CellID, a.CellID, jsActionArg(a.Args["delta"]))
	case "toggle":
		e.ln(ind, "%s = !%s;", a.CellID, a.CellID)
	case "navigate":
		// Full-page navigation: load a different server-served page.
		path, _ := a.Args["path"].(string)
		// Route client-side through a mounted path router when present;
		// otherwise fall back to a full page load.
		e.ln(ind, "(window.__sigilNav || ((p) => window.location.assign(p)))(%s);", jsQuote(path))
	case "append_item":
		e.ln(ind, "append_%s(%s);", a.CellID, jsActionArg(a.Args["value"]))
	case "remove_item":
		e.ln(ind, "remove_%s(%s);", a.CellID, jsActionArg(a.Args["target"]))
	case "append_struct_item":
		fields, _ := a.Args["fields"].(map[string]any)
		keys := sortedKeys(fields)
		e.wf("%sappend_struct_%s({", ind, a.CellID)
		for i, k := range keys {
			if i > 0 {
				e.w(",")
			}
			e.wf(" %q: %s", k, jsActionArg(fields[k]))
		}
		if len(keys) > 0 {
			e.w(" ")
		}
		e.w("});\n")
	case "clear_list":
		e.ln(ind, "clear_%s();", a.CellID)
	case "swap_items":
		e.ln(ind, "swap_items_%s(%s, %s);", a.CellID,
			jsActionArg(a.Args["i"]), jsActionArg(a.Args["j"]))
	case "select_in_list":
		e.ln(ind, "select_in_list_%s(%s);", a.CellID,
			jsActionArg(a.Args["target"]))
	case "create_batch_random":
		e.ln(ind, "create_batch_random_%s(%s, %s);", a.CellID,
			jsActionArg(a.Args["count"]), jsActionArg(a.Args["replace"]))
	case "update_every":
		e.ln(ind, "update_every_%s(%s, %s);", a.CellID,
			jsActionArg(a.Args["stride"]), jsActionArg(a.Args["suffix"]))
	case "call_op":
		opName, _ := a.Args["op"].(string)
		callExpr := jsOpCallExpr(opName, a.Args["args"])
		emitCore := func(ind string) {
			if a.CellID == "" {
				e.ln(ind, "await %s;", callExpr)
			} else {
				e.ln(ind, "%s = await %s;", a.CellID, callExpr)
				e.ln(ind, "__flush(%s);", jsQuote(a.CellID))
			}
			// `then navigate "<path>"` runs only here, in the success
			// path: for a command this sits inside the lifecycle try, so
			// a throw skips it and trips .failed instead.
			if path, ok := a.Args["then_navigate"].(string); ok && path != "" {
				// Route client-side through a mounted path router when present;
				// otherwise fall back to a full page load.
				e.ln(ind, "(window.__sigilNav || ((p) => window.location.assign(p)))(%s);", jsQuote(path))
			}
		}
		if !e.emitOpLifecycleWrap(a, opName, emitCore, ind) {
			emitCore(ind)
		}
	case "call_op_spread":
		opName, _ := a.Args["op"].(string)
		callExpr := jsOpCallExpr(opName, a.Args["args"])
		spread, _ := a.Args["spread"].([]any)
		e.ln(ind, "{")
		e.ln(ind+"  ", "const __r = await %s;", callExpr)
		for _, raw := range spread {
			leaf, _ := raw.(map[string]any)
			path, _ := leaf["path"].(string)
			cell, _ := leaf["cell"].(string)
			e.ln(ind+"  ", "%s = __r.%s;", cell, path)
		}
		e.ln(ind, "}")
		for _, raw := range spread {
			leaf, _ := raw.(map[string]any)
			cell, _ := leaf["cell"].(string)
			e.ln(ind, "__flush(%s);", jsQuote(cell))
		}
	case "call_op_list":
		opName, _ := a.Args["op"].(string)
		callExpr := jsOpCallExpr(opName, a.Args["args"])
		fields, _ := a.Args["fields"].([]any)
		e.ln(ind, "{")
		// Await the fetch BEFORE clearing so a failed request leaves
		// the existing rows intact; then replace (clear + append) so
		// the assignment reflects server truth rather than appending.
		e.ln(ind+"  ", "const __arr = await %s;", callExpr)
		e.ln(ind+"  ", "clear_%s();", a.CellID)
		e.ln(ind+"  ", "for (const __item of __arr) {")
		e.wf("%s    append_struct_%s({", ind, a.CellID)
		for i, raw := range fields {
			fname, _ := raw.(string)
			if i > 0 {
				e.w(",")
			}
			e.wf(" %q: __item.%s", fname, fname)
		}
		if len(fields) > 0 {
			e.w(" ")
		}
		e.w("});\n")
		e.ln(ind+"  ", "}")
		e.ln(ind, "}")
	case "call_op_stream":
		opName, _ := a.Args["op"].(string)
		// Positional arg exprs, resolved the same way jsOpCallExpr does
		// ($cell.<id> sentinels become the bare cell var).
		rawArgs, _ := a.Args["args"].([]any)
		parts := make([]string, 0, len(rawArgs)+1)
		for _, ar := range rawArgs {
			if s, ok := ar.(string); ok && strings.HasPrefix(s, "$cell.") {
				parts = append(parts, s[len("$cell."):])
				continue
			}
			parts = append(parts, jsActionArg(ar))
		}
		// The three target shapes share one lifecycle wrapper, so the
		// body is a closure invoked at the right indent depth.
		emitBody := func(ind string) {
			// Multi-channel: one request fans into N targets, demuxed by the
			// delta's channel. The callback takes (channel, text) — see
			// sigilStream's NDJSON branch.
			if chRaw, isMulti := a.Args["channels"].([]any); isMulti {
				e.emitMultiChannelStream(a, opName, parts, chRaw, ind)
				return
			}
			if listID, isRow := a.Args["list"].(string); isRow {
				// Stream into the most recently appended row's field. Row
				// text bindings aren't reactively flushed (set once at
				// build), so we mutate the row cell and rebuild just that
				// one row per delta — cheap, since only the streaming row
				// is touched. __rid is captured once before the await; the
				// handler is sequential, so no new rows arrive mid-stream.
				field, _ := a.Args["field"].(string)
				idx := e.forSiteIdx(listID)
				fieldKey := "." + field
				cb := fmt.Sprintf("(__d) => { cells_%s[__rid + %q] = (cells_%s[__rid + %q] || \"\") + __d; const __r = rows_%s[__rid]; if (__r) { const __n = __r.nextSibling; __r.remove(); const __nr = mkrow_%d(__rid); forEl_%d.insertBefore(__nr, __n); } }",
					listID, fieldKey, listID, fieldKey, listID, idx, idx)
				allArgs := append(parts, cb)
				e.ln(ind, "{")
				e.ln(ind+"  ", "const __rid = %s[%s.length - 1];", listID, listID)
				e.ln(ind+"  ", "if (__rid !== undefined) await window.__sigil_ops_stream.%s(%s);",
					opName, strings.Join(allArgs, ", "))
				e.ln(ind, "}")
				return
			}
			// Scalar: reset the target, then append each streamed delta and
			// flush the binding per chunk so the bound text grows live.
			cbParts := append(append([]string{}, parts...),
				fmt.Sprintf("(__d) => { %s = %s + __d; __flush(%s); }",
					a.CellID, a.CellID, jsQuote(a.CellID)))
			e.ln(ind, "%s = \"\";", a.CellID)
			e.ln(ind, "__flush(%s);", jsQuote(a.CellID))
			e.ln(ind, "await window.__sigil_ops_stream.%s(%s);", opName, strings.Join(cbParts, ", "))
		}

		pend, _ := a.Args["pending_cell"].(string)
		failed, _ := a.Args["failed_cell"].(string)
		errCell, _ := a.Args["error_cell"].(string)
		// `<-` starts the stream and moves on: the call sits in an
		// un-awaited async IIFE, so statements after the arrow run
		// immediately (a composer's `prompt = ""` must not wait minutes
		// for a model to finish). Everything up to the first await still
		// runs synchronously at the arrow site — target resets and the
		// row-form's __rid capture see the handler's state as written.
		// `<Op>.pending` is the "still streaming" signal.
		if pend == "" {
			e.ln(ind, "(async () => {")
			emitBody(ind + "  ")
			e.ln(ind, "})();")
			break
		}
		// Lifecycle wrapper: <Op>.pending rises before the request and
		// falls when the LAST overlapping call settles (per-op open
		// counter); <Op>.failed / <Op>.error reset on every new call and
		// capture the message if the stream throws — without the catch,
		// a failed stream would be an unobservable promise rejection.
		e.ln(ind, "(async () => {")
		e.ln(ind+"  ", "%s = true; __flush(%s);", pend, jsQuote(pend))
		e.ln(ind+"  ", "%s = false; __flush(%s);", failed, jsQuote(failed))
		e.ln(ind+"  ", "%s = \"\"; __flush(%s);", errCell, jsQuote(errCell))
		e.ln(ind+"  ", "window.__sigil_op_open[%q] = (window.__sigil_op_open[%q] || 0) + 1;", opName, opName)
		e.ln(ind+"  ", "try {")
		emitBody(ind + "    ")
		e.ln(ind+"  ", "} catch (__err) {")
		e.ln(ind+"    ", "%s = true; __flush(%s);", failed, jsQuote(failed))
		e.ln(ind+"    ", "%s = String(__err && __err.message || __err); __flush(%s);", errCell, jsQuote(errCell))
		e.ln(ind+"  ", "} finally {")
		e.ln(ind+"    ", "if (--window.__sigil_op_open[%q] === 0) { %s = false; __flush(%s); }", opName, pend, jsQuote(pend))
		e.ln(ind+"  ", "}")
		e.ln(ind, "})();")
	default:
		e.ln(ind, "/* unsupported action %q */", a.Kind)
	}

	// Flush bindings for mutating actions (not list-actions or ops,
	// which handle their own flush).
	switch a.Kind {
	case "set", "set_variant", "add", "toggle":
		e.ln(ind, "__flush(%s);", jsQuote(a.CellID))
	}
}

// emitOpLifecycleWrap wraps a command's inline await in its lifecycle
// bookkeeping when the action carries the stamped cells. It returns
// false (emitting nothing) for ops without lifecycle cells, so the
// caller falls back to a bare call.
//
// Unlike the stream `<-` wrapper, this stays INLINE (awaited, not a
// fire-and-forget IIFE): a command's result and the ordering of the
// statements after it matter (the refetch-after-mutation idiom), so
// they must still run after it settles. <Op>.pending rises before and
// falls when the LAST overlapping call settles (the per-op open
// counter handles a double-tapped Submit); <Op>.failed / <Op>.error
// reset on each call and capture a throw (backend down / non-2xx) that
// would otherwise be an unobservable promise rejection. The catch
// swallows — a failed command no longer aborts the rest of the
// handler; the .failed cell is the signal to branch on. The lifecycle
// cells are top-level, so emitCore may run inside a row closure and
// still address them by id.
func (e *spaEmitter) emitOpLifecycleWrap(a ir.Action, opName string, emitCore func(ind string), ind string) bool {
	pend, ok := a.Args["pending_cell"].(string)
	if !ok {
		return false
	}
	failed, _ := a.Args["failed_cell"].(string)
	errCell, _ := a.Args["error_cell"].(string)
	e.ln(ind, "%s = true; __flush(%s);", pend, jsQuote(pend))
	e.ln(ind, "%s = false; __flush(%s);", failed, jsQuote(failed))
	e.ln(ind, "%s = \"\"; __flush(%s);", errCell, jsQuote(errCell))
	e.ln(ind, "window.__sigil_op_open[%q] = (window.__sigil_op_open[%q] || 0) + 1;", opName, opName)
	e.ln(ind, "try {")
	emitCore(ind + "  ")
	e.ln(ind, "} catch (__err) {")
	e.ln(ind+"  ", "%s = true; __flush(%s);", failed, jsQuote(failed))
	e.ln(ind+"  ", "%s = String(__err && __err.message || __err); __flush(%s);", errCell, jsQuote(errCell))
	e.ln(ind, "} finally {")
	e.ln(ind+"  ", "if (--window.__sigil_op_open[%q] === 0) { %s = false; __flush(%s); }", opName, pend, jsQuote(pend))
	e.ln(ind, "}")
	return true
}

// emitMultiChannelStream emits a `(t1, t2, ...) <- StreamOp(...)` call:
// one request whose deltas are demuxed by channel into N targets. The
// callback signature is (channel, text) — sigilStream's NDJSON branch
// frames the body and calls it once per line.
//
// `bindings` is the lowered `channels` slice: each entry maps a channel
// to a scalar cell (`{channel, cell}`) or, in the transcript form, a row
// field (`{channel, field}`) on the list named by a.Args["list"]. Targets
// are reset before the call so each region clears, then appends per delta.
func (e *spaEmitter) emitMultiChannelStream(a ir.Action, opName string, parts []string, bindings []any, ind string) {
	type binding struct{ channel, target string }
	binds := make([]binding, 0, len(bindings))
	for _, raw := range bindings {
		m, _ := raw.(map[string]any)
		ch, _ := m["channel"].(string)
		if cell, ok := m["cell"].(string); ok {
			binds = append(binds, binding{ch, cell})
		} else if fld, ok := m["field"].(string); ok {
			binds = append(binds, binding{ch, fld})
		}
	}

	if listID, isRow := a.Args["list"].(string); isRow {
		// Transcript form: every channel writes a field of the most
		// recently appended row, then the row is rebuilt once per delta so
		// the bound text reflects the new cell values (row text bindings
		// are set at build time, not reactively flushed).
		idx := e.forSiteIdx(listID)
		var cb strings.Builder
		cb.WriteString("(__ch, __t) => { ")
		for i, b := range binds {
			key := "." + b.target
			if i > 0 {
				cb.WriteString("else ")
			}
			fmt.Fprintf(&cb, "if (__ch === %q) cells_%s[__rid + %q] = (cells_%s[__rid + %q] || \"\") + __t; ",
				b.channel, listID, key, listID, key)
		}
		fmt.Fprintf(&cb, "const __r = rows_%s[__rid]; if (__r) { const __n = __r.nextSibling; __r.remove(); const __nr = mkrow_%d(__rid); forEl_%d.insertBefore(__nr, __n); } }",
			listID, idx, idx)
		allArgs := append(append([]string{}, parts...), cb.String())
		e.ln(ind, "{")
		e.ln(ind+"  ", "const __rid = %s[%s.length - 1];", listID, listID)
		for _, b := range binds {
			e.ln(ind+"  ", "if (__rid !== undefined) cells_%s[__rid + %q] = \"\";", listID, "."+b.target)
		}
		e.ln(ind+"  ", "if (__rid !== undefined) await window.__sigil_ops_stream.%s(%s);",
			opName, strings.Join(allArgs, ", "))
		e.ln(ind, "}")
		return
	}

	// Scalar form: each channel fills one String cell, flushing per delta.
	var cb strings.Builder
	cb.WriteString("(__ch, __t) => { ")
	for i, b := range binds {
		if i > 0 {
			cb.WriteString("else ")
		}
		fmt.Fprintf(&cb, "if (__ch === %q) { %s = %s + __t; __flush(%s); } ",
			b.channel, b.target, b.target, jsQuote(b.target))
	}
	cb.WriteString("}")
	for _, b := range binds {
		e.ln(ind, "%s = \"\";", b.target)
		e.ln(ind, "__flush(%s);", jsQuote(b.target))
	}
	allArgs := append(append([]string{}, parts...), cb.String())
	e.ln(ind, "await window.__sigil_ops_stream.%s(%s);", opName, strings.Join(allArgs, ", "))
}

// --- for-site functions ---

func (e *spaEmitter) emitForFunctions(fs spaForSite) {
	p := fs.parentCellID
	uses := e.actionsByParent[p]
	if uses == nil {
		uses = map[string]bool{}
	}
	ind := "  "
	inner := "    "

	// Row factory
	e.ln(ind, "function mkrow_%d(cellId) {", fs.idx)
	rowVar := e.fresh()
	e.ln(inner, "const %s = document.createElement('div');", rowVar)
	e.ln(inner, "%s.setAttribute('data-sigil-for-item', cellId);", rowVar)
	if fs.templateRow != nil {
		for _, c := range fs.templateRow.Children {
			cv := e.emitRowNode(c, p, inner)
			e.ln(inner, "%s.appendChild(%s);", rowVar, cv)
		}
	}
	e.ln(inner, "rows_%s[cellId] = %s;", p, rowVar)
	e.ln(inner, "return %s;", rowVar)
	e.ln(ind, "}")

	// append
	if uses["append_item"] {
		e.ln(ind, "function append_%s(value) {", p)
		e.ln(inner, "const newId = 'r' + (++counter_%s);", p)
		e.ln(inner, "cells_%s[newId] = value;", p)
		e.ln(inner, "%s.push(newId);", p)
		e.ln(inner, "if (forEl_%d) forEl_%d.appendChild(mkrow_%d(newId));", fs.idx, fs.idx, fs.idx)
		e.ln(ind, "}")
	}

	// append_struct
	if uses["append_struct_item"] {
		e.ln(ind, "function append_struct_%s(fields) {", p)
		e.ln(inner, "const newId = 'r' + (++counter_%s);", p)
		e.ln(inner, "for (const k of Object.keys(fields)) cells_%s[newId + '.' + k] = fields[k];", p)
		e.ln(inner, "%s.push(newId);", p)
		e.ln(inner, "if (forEl_%d) forEl_%d.appendChild(mkrow_%d(newId));", fs.idx, fs.idx, fs.idx)
		e.ln(ind, "}")
	}

	// remove
	if uses["remove_item"] {
		e.ln(ind, "function remove_%s(cellId) {", p)
		e.ln(inner, "const i = %s.indexOf(cellId);", p)
		e.ln(inner, "if (i < 0) return;")
		e.ln(inner, "%s.splice(i, 1);", p)
		e.ln(inner, "delete cells_%s[cellId];", p)
		e.ln(inner, "const prefix = cellId + '.';")
		e.ln(inner, "for (const k of Object.keys(cells_%s)) if (k.startsWith(prefix)) delete cells_%s[k];", p, p)
		e.ln(inner, "const r = rows_%s[cellId];", p)
		e.ln(inner, "if (r && r.parentNode) r.parentNode.removeChild(r);")
		e.ln(inner, "delete rows_%s[cellId];", p)
		e.ln(ind, "}")
	}

	// select tracking
	if uses["select_in_list"] || uses["clear_list"] {
		e.ln(ind, "let selected_%s = '';", p)
	}

	// clear
	if uses["clear_list"] {
		e.ln(ind, "function clear_%s() {", p)
		e.ln(inner, "%s.length = 0;", p)
		e.ln(inner, "for (const k of Object.keys(cells_%s)) delete cells_%s[k];", p, p)
		e.ln(inner, "for (const k of Object.keys(rows_%s)) delete rows_%s[k];", p, p)
		// Guard the DOM reset: a list whose route hasn't mounted yet has
		// a null forEl (same guard append_struct uses). The array/cells
		// resets above still run, so when the route later mounts it
		// rebuilds rows from the corrected data. Without this guard, a
		// call_op_list populate on a not-yet-visible route throws and
		// aborts the whole mount sequence.
		e.ln(inner, "if (forEl_%d) forEl_%d.textContent = '';", fs.idx, fs.idx)
		e.ln(inner, "selected_%s = '';", p)
		e.ln(ind, "}")
	}

	if uses["swap_items"] {
		e.ln(ind, "function swap_items_%s(i, j) {", p)
		e.ln(inner, "if (i < 0 || j < 0 || i >= %s.length || j >= %s.length || i === j) return;", p, p)
		e.ln(inner, "const ai = %s[i], aj = %s[j];", p, p)
		e.ln(inner, "%s[i] = aj; %s[j] = ai;", p, p)
		e.ln(inner, "const rowI = rows_%s[ai], rowJ = rows_%s[aj];", p, p)
		e.ln(inner, "if (!rowI || !rowJ) return;")
		e.ln(inner, "const marker = document.createComment('');")
		e.ln(inner, "rowJ.parentNode.insertBefore(marker, rowJ);")
		e.ln(inner, "rowI.parentNode.insertBefore(rowJ, rowI);")
		e.ln(inner, "marker.parentNode.insertBefore(rowI, marker);")
		e.ln(inner, "marker.remove();")
		e.ln(ind, "}")
	}

	if uses["select_in_list"] {
		e.ln(ind, "function select_in_list_%s(target) {", p)
		e.ln(inner, "if (!target) return;")
		e.ln(inner, "if (selected_%s) {", p)
		e.ln(inner, "  const o = rows_%s[selected_%s];", p, p)
		e.ln(inner, "  if (o) o.removeAttribute('data-sigil-tone-runtime');")
		e.ln(inner, "}")
		e.ln(inner, "selected_%s = target;", p)
		e.ln(inner, "const c = rows_%s[target];", p)
		e.ln(inner, "if (c) c.setAttribute('data-sigil-tone-runtime', 'selected');")
		e.ln(ind, "}")
	}

	if uses["create_batch_random"] {
		e.ln(ind, "function create_batch_random_%s(count, replace) {", p)
		e.ln(inner, "if (replace) clear_%s();", p)
		e.ln(inner, "const frag = document.createDocumentFragment();")
		e.ln(inner, "for (let i = 0; i < count; i++) {")
		e.ln(inner, "  const newId = 'r' + (++counter_%s);", p)
		e.ln(inner, "  cells_%s[newId] = randomLabel();", p)
		e.ln(inner, "  %s.push(newId);", p)
		e.ln(inner, "  const row = mkrow_%d(newId);", fs.idx)
		e.ln(inner, "  frag.appendChild(row);")
		e.ln(inner, "}")
		e.ln(inner, "forEl_%d.appendChild(frag);", fs.idx)
		e.ln(ind, "}")
	}

	if uses["update_every"] {
		e.ln(ind, "function update_every_%s(stride, suffix) {", p)
		e.ln(inner, "if (stride <= 0) return;")
		e.ln(inner, "for (let i = 0; i < %s.length; i += stride) {", p)
		e.ln(inner, "  const id = %s[i];", p)
		e.ln(inner, "  cells_%s[id] = String(cells_%s[id]) + suffix;", p, p)
		e.ln(inner, "  const r = rows_%s[id];", p)
		e.ln(inner, "  if (r) { r.remove(); const nr = mkrow_%d(id); forEl_%d.insertBefore(nr, forEl_%d.children[i] || null); }",
			fs.idx, fs.idx, fs.idx)
		e.ln(inner, "}")
		e.ln(ind, "}")
	}

	// Filter function: when a filter cell is set, iterate all rows and
	// show/hide based on whether any field value contains the search text.
	if fs.filterCellID != "" {
		e.ln(ind, "function filter_%s() {", p)
		e.ln(inner, "const q = String(%s).toLowerCase();", fs.filterCellID)
		e.ln(inner, "for (const id of %s) {", p)
		e.ln(inner, "  const r = rows_%s[id];", p)
		e.ln(inner, "  if (!r) continue;")
		e.ln(inner, "  if (!q) { r.style.display = ''; continue; }")
		e.ln(inner, "  let match = false;")
		e.ln(inner, "  const prefix = id + '.';")
		e.ln(inner, "  for (const k of Object.keys(cells_%s)) {", p)
		e.ln(inner, "    if (k === id || k.startsWith(prefix)) {")
		e.ln(inner, "      if (String(cells_%s[k]).toLowerCase().includes(q)) { match = true; break; }", p)
		e.ln(inner, "    }")
		e.ln(inner, "  }")
		e.ln(inner, "  r.style.display = match ? '' : 'none';")
		e.ln(inner, "}")
		e.ln(ind, "}")
	}
}

// emitRowNode creates one element inside a for-row factory function.
// Cell refs resolve through cells_<parent>[cellId + ".field"].
func (e *spaEmitter) emitRowNode(n ir.Node, parentCellID, ind string) string {
	switch n.Kind {
	case ir.KindText:
		v := e.fresh()
		e.ln(ind, "const %s = document.createElement('span');", v)
		e.setClass(v, n.ID, ind)
		if ref, ok := n.Bindings["text"]; ok {
			expr := spaRowCellExpr(ref.CellID, parentCellID)
			e.ln(ind, "%s.textContent = String(%s);", v, expr)
		} else {
			e.ln(ind, "%s.textContent = %s;", v, jsQuote(nodePropStr(n, "text")))
		}
		return v

	case ir.KindCode:
		v := e.fresh()
		e.ln(ind, "const %s = document.createElement('pre');", v)
		e.setClass(v, n.ID, ind)
		code := e.fresh()
		e.ln(ind, "const %s = document.createElement('code');", code)
		e.ln(ind, "%s.textContent = %s;", code, jsQuote(nodePropStr(n, "text")))
		e.ln(ind, "%s.appendChild(%s);", v, code)
		return v

	case ir.KindBadge:
		v := e.fresh()
		e.ln(ind, "const %s = document.createElement('span');", v)
		e.setClass(v, n.ID, ind)
		if ref, ok := n.Bindings["text"]; ok {
			expr := spaRowCellExpr(ref.CellID, parentCellID)
			e.ln(ind, "%s.textContent = String(%s);", v, expr)
		} else {
			e.ln(ind, "%s.textContent = %s;", v, jsQuote(nodePropStr(n, "text")))
		}
		return v

	case ir.KindButton:
		v := e.fresh()
		e.ln(ind, "const %s = document.createElement('button');", v)
		e.ln(ind, "%s.type = 'button';", v)
		e.setClass(v, n.ID, ind)
		if iconName, ok := n.Props["icon"].(string); ok {
			iconSet, _ := n.Props["icon-set"].(string)
			iv := e.fresh()
			e.ln(ind, "const %s = document.createElement('span');", iv)
			e.ln(ind, "%s.className = 's-icon';", iv)
			e.ln(ind, "%s.innerHTML = '<svg aria-hidden=\"true\"><use href=\"#sigil-icon-%s-%s\"/></svg>';",
				iv, iconSet, iconName)
			e.ln(ind, "%s.appendChild(%s);", v, iv)
		}
		label := nodePropStr(n, "label")
		if label != "" {
			e.ln(ind, "%s.appendChild(document.createTextNode(%s));", v, jsQuote(label))
		}
		if a, ok := n.Handlers["click"]; ok {
			e.emitRowHandler(v, "click", a, parentCellID, ind)
		}
		return v

	case ir.KindStack, ir.KindCard, ir.KindContainer, ir.KindFragment:
		tag := "div"
		if n.Kind == ir.KindCard {
			tag = "section"
		}
		v := e.fresh()
		e.ln(ind, "const %s = document.createElement(%s);", v, jsQuote(tag))
		e.setClass(v, n.ID, ind)
		for _, c := range n.Children {
			cv := e.emitRowNode(c, parentCellID, ind)
			e.ln(ind, "%s.appendChild(%s);", v, cv)
		}
		if a, ok := n.Handlers["click"]; ok {
			e.ln(ind, "%s.style.cursor = 'pointer';", v)
			e.emitRowHandler(v, "click", a, parentCellID, ind)
		}
		return v

	case ir.KindIcon:
		v := e.fresh()
		e.ln(ind, "const %s = document.createElement('span');", v)
		e.setClass(v, n.ID, ind)
		set := nodePropStr(n, "icon-set")
		name := nodePropStr(n, "name")
		e.ln(ind, "%s.innerHTML = '<svg aria-hidden=\"true\"><use href=\"#sigil-icon-%s-%s\"/></svg>';", v, set, name)
		return v

	case ir.KindDivider:
		v := e.fresh()
		e.ln(ind, "const %s = document.createElement('hr');", v)
		e.setClass(v, n.ID, ind)
		return v

	case ir.KindPulse:
		return e.emitPulse(n, ind)

	case ir.KindIf:
		// Nested if inside for: the condition is evaluated per row at
		// BUILD time, against the row's current cell values. Rows are
		// rebuilt whenever a handler mutates one of their cells (see
		// emitRowHandler), so the conditional subtree — bindings,
		// handlers and all — tracks the row's data without needing its
		// own mount/unmount machinery. This is what lets a transcript
		// row branch on a speaker field (`if m.user → text m.text`).
		bref := n.Bindings["visible"]
		cond := spaRowCellExpr(bref.CellID, parentCellID)
		v := e.fresh()
		e.ln(ind, "let %s;", v)
		e.ln(ind, "if (%s) {", cond)
		inner := ind + "  "
		wrap := e.fresh()
		e.ln(inner, "const %s = document.createElement('div');", wrap)
		for _, c := range n.Children {
			cv := e.emitRowNode(c, parentCellID, inner)
			e.ln(inner, "%s.appendChild(%s);", wrap, cv)
		}
		e.ln(inner, "%s = %s;", v, wrap)
		e.ln(ind, "} else {")
		e.ln(ind+"  ", "%s = document.createComment('if');", v)
		e.ln(ind, "}")
		return v

	default:
		v := e.fresh()
		e.ln(ind, "const %s = document.createComment('row:%s');", v, n.Kind)
		return v
	}
}

func (e *spaEmitter) emitRowHandler(elemVar, event string, a ir.Action, parentCellID, ind string) {
	asyncMod := ""
	if actionUsesAwait(a) {
		asyncMod = "async "
	}
	e.ln(ind, "%s.on%s = %s(event) => {", elemVar, event, asyncMod)
	e.emitRowActionBody(a, parentCellID, ind+"  ")
	// Row content is written once at build time (no per-binding flush
	// machinery inside rows), so any mutation of the row's own cells
	// rebuilds the row — same idiom streaming and update_every use.
	// One rebuild per handler invocation, after all statements ran.
	// The rows_ guard keeps a handler that removed its own row (e.g.
	// `item.done = true; items.remove(item)`) from resurrecting it.
	if actionMutatesRowCell(a) {
		idx := e.forSiteIdx(parentCellID)
		e.ln(ind+"  ",
			"{ const __r = rows_%s[cellId]; if (__r) { const __n = __r.nextSibling; __r.remove(); forEl_%d.insertBefore(mkrow_%d(cellId), __n); } }",
			parentCellID, idx, idx)
	}
	e.ln(ind, "};")
}

// actionMutatesRowCell reports whether an action (recursing into
// sequence) writes the row's own cell or one of its dotted sub-fields
// — i.e. whether the row's rendered content can be stale after the
// handler runs.
func actionMutatesRowCell(a ir.Action) bool {
	if a.Kind == "sequence" {
		inner, _ := a.Args["actions"].([]any)
		for _, raw := range inner {
			if ia, ok := raw.(ir.Action); ok && actionMutatesRowCell(ia) {
				return true
			}
		}
		return false
	}
	switch a.Kind {
	case "set", "add", "toggle", "call_op":
		return strings.HasPrefix(a.CellID, "$ITEM")
	}
	return false
}

func (e *spaEmitter) emitRowActionBody(a ir.Action, parentCellID, ind string) {
	if a.Kind == "sequence" {
		inner, _ := a.Args["actions"].([]any)
		for _, raw := range inner {
			if ia, ok := raw.(ir.Action); ok {
				e.emitRowActionBody(ia, parentCellID, ind)
			}
		}
		return
	}

	target := spaRowCellExpr(a.CellID, parentCellID)
	isTopLevel := !strings.HasPrefix(a.CellID, "$ITEM")
	switch a.Kind {
	case "set":
		e.ln(ind, "%s = %s;", target, spaRowArg(a.Args["value"], parentCellID))
		if isTopLevel {
			e.ln(ind, "__flush(%s);", jsQuote(a.CellID))
		}
	case "add":
		e.ln(ind, "%s = %s + %s;", target, target, spaRowArg(a.Args["delta"], parentCellID))
		if isTopLevel {
			e.ln(ind, "__flush(%s);", jsQuote(a.CellID))
		}
	case "toggle":
		e.ln(ind, "%s = !%s;", target, target)
		if isTopLevel {
			e.ln(ind, "__flush(%s);", jsQuote(a.CellID))
		}
	case "append_item":
		e.ln(ind, "append_%s(%s);", a.CellID, spaRowArg(a.Args["value"], parentCellID))
	case "remove_item":
		e.ln(ind, "remove_%s(%s);", a.CellID, spaRowArg(a.Args["target"], parentCellID))
	case "call_op":
		opName, _ := a.Args["op"].(string)
		args, _ := a.Args["args"].([]any)
		parts := make([]string, 0, len(args))
		for _, arg := range args {
			parts = append(parts, spaRowArg(arg, parentCellID))
		}
		callExpr := fmt.Sprintf("window.__sigil_ops.%s(%s)", opName, strings.Join(parts, ", "))
		emitCore := func(ind string) {
			if a.CellID == "" {
				e.ln(ind, "await %s;", callExpr)
			} else {
				e.ln(ind, "%s = await %s;", target, callExpr)
			}
			if path, ok := a.Args["then_navigate"].(string); ok && path != "" {
				// Route client-side through a mounted path router when present;
				// otherwise fall back to a full page load.
				e.ln(ind, "(window.__sigilNav || ((p) => window.location.assign(p)))(%s);", jsQuote(path))
			}
		}
		// Command lifecycle cells are top-level (global) cells, so the
		// wrapper references them by id even inside a row closure.
		if !e.emitOpLifecycleWrap(a, opName, emitCore, ind) {
			emitCore(ind)
		}
	default:
		e.ln(ind, "/* unsupported row action %q */", a.Kind)
	}
}

// --- helpers ---

func (e *spaEmitter) setClass(varName, nodeID, ind string) {
	if cls := e.classMap[nodeID]; cls != "" {
		e.ln(ind, "%s.className = %s;", varName, jsQuote(cls))
	}
}

func (e *spaEmitter) regUpdate(cellID, code, ind string, ifc *ifCtx) {
	if ifc != nil {
		e.ln(ind, "%s.push(__reg(%s, () => { %s }));", ifc.deregVar, jsQuote(cellID), code)
	} else {
		e.ln(ind, "__reg(%s, () => { %s });", jsQuote(cellID), code)
	}
}

func nodePropStr(n ir.Node, k string) string {
	if v, ok := n.Props[k].(string); ok {
		return v
	}
	return ""
}

func spaTitleTag(n ir.Node) string {
	switch nodePropStr(n, "size") {
	case "lg":
		return "h1"
	case "sm":
		return "h3"
	default:
		return "h2"
	}
}

func spaRowCellExpr(cellID, parentCellID string) string {
	if cellID == "$ITEM" {
		return fmt.Sprintf("cells_%s[cellId]", parentCellID)
	}
	if strings.HasPrefix(cellID, "$ITEM.") {
		field := cellID[len("$ITEM."):]
		return fmt.Sprintf("cells_%s[cellId + %q]", parentCellID, "."+field)
	}
	return cellID
}

func spaRowArg(v any, parentCellID string) string {
	if s, ok := v.(string); ok {
		if s == "$ITEM" {
			return "cellId"
		}
		if strings.HasPrefix(s, "$event.") {
			return "event.target." + s[len("$event."):]
		}
		if strings.HasPrefix(s, "$cell.") {
			rest := s[len("$cell."):]
			if rest == "$ITEM" {
				return fmt.Sprintf("cells_%s[cellId]", parentCellID)
			}
			if strings.HasPrefix(rest, "$ITEM.") {
				field := rest[len("$ITEM."):]
				return fmt.Sprintf("cells_%s[cellId + %q]", parentCellID, "."+field)
			}
			return rest
		}
	}
	return jsLiteral(v)
}
