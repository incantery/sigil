// Package codegen emits per-app JS directly from an ir.Document. It
// IS the dispatch layer — there is no shared runtime that ships to
// the browser. Each app gets exactly the closures it needs (the
// closures Profile/Emit can express today); IR shapes outside the
// profile are a compile error, not a fallback.
//
// The architectural rule: every byte that ships to a user device must
// justify itself on bundle size or runtime perf — never on compiler
// convenience. So codegen produces direct DOM-node closures, inline
// arithmetic, and a static set of bindings; no dispatch table, no
// kind switch, no cell registry indirection.
//
// Adding a new IR feature requires extending Profile (the gate) and
// Emit (the codegen). Until both land, the feature can't be authored
// in source — by construction. This is the discipline the
// "compiler is the framework" thesis demands; the alternative would
// be a fallback that quietly ships more runtime each time.
package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/incantery/mako/pkg/ir"
)

// Profile reports whether the given document is expressible by the
// current codegen. ok=true means Emit will succeed; ok=false returns
// a human-readable reason that bubbles up as a compile error from
// html.WriteDoc. Adding the missing IR shape to Profile + Emit is
// the only way to unblock a rejected doc.
func Profile(doc ir.Document) (ok bool, reason string) {
	var walk func(n ir.Node, insideFor bool) string
	walk = func(n ir.Node, insideFor bool) string {
		switch n.Kind {
		case ir.KindCard, ir.KindTitle, ir.KindFragment, ir.KindContainer, ir.KindBadge, ir.KindIcon, ir.KindDivider, ir.KindPulse, ir.KindRouter, ir.KindRoute, ir.KindGroup, ir.KindModal:
			// Pure layout/text wrappers — fine, recurse.
		case ir.KindMatch, ir.KindMatchArm:
			// Discriminated-union match + its arms. The SPA emitter
			// renders each arm as a tag-gated mount block with its
			// payload binding flowing through real cells; arm subtrees
			// recurse like any other children.
		case ir.KindStack:
			for ev, a := range n.Handlers {
				if ev != "click" {
					return fmt.Sprintf("stack event %q not yet supported", ev)
				}
				if reason := profileAction(a); reason != "" {
					return reason
				}
			}
		case ir.KindBar:
			for prop := range n.Bindings {
				if prop != "fill" {
					return fmt.Sprintf("bar-binding prop %q not yet supported", prop)
				}
			}
		case ir.KindIf:
			// Reactive mount/unmount of the subtree. Only the `visible`
			// binding shape is recognized; future props (transitions,
			// keep-mounted, …) are out-of-profile until proven needed.
			//
			// Nested `if` inside `for` is fully supported by the SPA
			// emitter (bindings and handlers included): the condition
			// evaluates at row-build time and rows rebuild on mutation,
			// so the conditional subtree needs no mount/unmount state
			// of its own. (The legacy SSR emitter in this file still
			// skips inner bindings under a row-if — it is golden-test
			// only; EmitSPA is the production path.)
			for prop := range n.Bindings {
				if prop != "visible" {
					return fmt.Sprintf("if-binding prop %q not yet supported", prop)
				}
			}
		case ir.KindFor:
			// Each for-site is its own per-list state machine: parent
			// list cell, child-cell map, row map, mount/append/remove
			// closures. Nested for inside for would need scoped child
			// maps — out-of-profile until proven needed.
			if insideFor {
				return "nested `for` not yet supported in codegen"
			}
		case ir.KindForItem:
			// for_item nodes are produced by lowerFor — they have no
			// emit code of their own; the parent for-site's mount
			// routine handles the row.
		case ir.KindText:
			for prop := range n.Bindings {
				if prop != "text" {
					return fmt.Sprintf("binding prop %q not yet supported", prop)
				}
			}
		case ir.KindCode:
			// Verbatim static content — no bindings, no handlers. Lower
			// guarantees this shape; the profile just admits the kind.
			if len(n.Bindings) > 0 {
				return "code blocks are static (no bindings)"
			}
			if len(n.Handlers) > 0 {
				return "code blocks take no event handlers"
			}
		case ir.KindIFrame:
			// Static src, or src bound to a cell (the catalog-viewer
			// pattern: sidebar buttons set the cell, the frame follows).
			for prop := range n.Bindings {
				if prop != "src" {
					return fmt.Sprintf("iframe binding prop %q not yet supported", prop)
				}
			}
			if len(n.Handlers) > 0 {
				return "iframe events not yet supported"
			}
		case ir.KindTextInput:
			for prop := range n.Bindings {
				if prop != "value" {
					return fmt.Sprintf("text_input binding prop %q not yet supported", prop)
				}
			}
			for ev, a := range n.Handlers {
				if ev != "input" {
					return fmt.Sprintf("text_input event %q not yet supported", ev)
				}
				if a.Kind != "set" {
					return fmt.Sprintf("text_input action %q not yet supported", a.Kind)
				}
			}
		case ir.KindButton:
			for ev, a := range n.Handlers {
				if ev != "click" {
					return fmt.Sprintf("event %q not yet supported", ev)
				}
				if reason := profileAction(a); reason != "" {
					return reason
				}
			}
		default:
			return fmt.Sprintf("kind %q not yet supported by codegen", n.Kind)
		}
		childInsideFor := insideFor || n.Kind == ir.KindFor
		for _, c := range n.Children {
			if r := walk(c, childInsideFor); r != "" {
				return r
			}
		}
		return ""
	}
	if r := walk(doc.Root, false); r != "" {
		return false, r
	}
	return true, ""
}

// profileAction reports whether an action shape (including nested
// sequence actions) is supported by the current codegen profile.
// Empty return = supported.
func profileAction(a ir.Action) string {
	switch a.Kind {
	case "set", "set_variant", "add", "toggle", "navigate",
		"append_item", "remove_item", "append_struct_item",
		"clear_list", "swap_items", "select_in_list",
		"create_batch_random", "update_every",
		"call_op", "call_op_spread", "call_op_list", "call_op_stream":
		return ""
	case "sequence":
		inner, _ := a.Args["actions"].([]any)
		for _, raw := range inner {
			ia, ok := raw.(ir.Action)
			if !ok {
				return "sequence with non-action element"
			}
			if r := profileAction(ia); r != "" {
				return r
			}
		}
		return ""
	}
	return fmt.Sprintf("action %q not yet supported", a.Kind)
}

// bindingSite is one DOM prop the codegen wires to a cell. ifSid is
// the data-sid of the enclosing `if` site (empty when the binding sits
// at the top level, always mounted). When ifSid != "", the resolved
// element reference is nulled on unmount and re-resolved on mount, so
// the cell's update writes are conditional on `if_<sid>_n != null`.
type bindingSite struct {
	sid      string
	prop     string
	template string // for "text" bindings; "" = plain String() write
	ifSid    string
	cellID   string
	max      int // for "fill" bindings (bar progress max), 0 = not a fill
}

// handlerSite is one wired event handler: which sid, which event,
// which declarative action.
type handlerSite struct {
	sid    string
	event  string
	action ir.Action
}

// ifSite is one `if <bool-cell>` reactive mount point. cellID is the
// bool cell driving visibility; initial is the SSR-rendered state.
// children carries the bindings nested directly under this if (used
// to emit a mount() routine that resolves them).
type ifSite struct {
	sid      string
	cellID   string
	initial  bool
	children []bindingSite
}

// forSite is one `for <name> in <list>` reactive list. parentCellID
// is the list cell; initialIDs are the child cell ids present at
// SSR time (each gets a pre-rendered row). templateRow is the
// for_item IR node whose Props["template"]==true — its subtree is
// the recipe for new rows (and the canonical structure for path
// walks during per-row mount).
type forSite struct {
	sid          string
	parentCellID string
	initialIDs   []string
	initialVals  map[string]any // initial scalar value per child cell
	templateRow  *ir.Node
}

// rowEmit collects the per-row code we generate from one pass over
// the template subtree. Order matters: element refs first, then
// initial-value writes, then handlers (which capture the refs in
// closures).
type rowEmit struct {
	elementRefs []string // `let e_0_1 = rowEl.children[0].children[1];`
	initWrites  []string // `e_0_1.textContent = String(cells_c4[cellId]);`
	handlers    []string // `e_0_0.onclick = () => { ... };`
}

// rowIfSite tracks one nested `if` inside a for-row template. Each
// instance needs a row-local mounted flag and a swap routine that
// flips the wrapper between <template> and <div> while keeping the
// element-ref var in sync.
type rowIfSite struct {
	key      string // unique within this mount, derived from path
	elemVar  string // the let-bound row element ref
	cellID   string // raw cell id from binding (may be $ITEM.X)
	cellExpr string // resolved JS cell access expression
}

// Emit generates the per-app JS for doc. Caller is responsible for
// having checked Profile first; emitting a doc that fails the profile
// is a programmer error and panics.
func Emit(doc ir.Document) string {
	if ok, reason := Profile(doc); !ok {
		panic("codegen.Emit called on unsupported doc: " + reason)
	}

	bindings := map[string][]bindingSite{} // cellID -> bindings (top-level only)
	ifSites := map[string]*ifSite{}        // ifSid -> site, for mount/unmount gen
	var ifOrder []string                   // stable iteration order
	var handlers []handlerSite
	var forSites []forSite
	listChildIDs := map[string]bool{} // child cells that live in a per-list map
	listParentIDs := map[string]bool{}

	var walk func(n ir.Node, ifSid string)
	walk = func(n ir.Node, ifSid string) {
		if n.Kind == ir.KindFor {
			parentCellID, _ := n.Props["cell"].(string)
			fs := forSite{sid: n.ID, parentCellID: parentCellID, initialVals: map[string]any{}}
			listParentIDs[parentCellID] = true
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
				if cellID, ok := c.Props["cell"].(string); ok {
					fs.initialIDs = append(fs.initialIDs, cellID)
					listChildIDs[cellID] = true
					if v, ok := doc.Cells[cellID]; ok {
						fs.initialVals[cellID] = v
					}
				}
			}
			forSites = append(forSites, fs)
			// Bindings/handlers inside the for-rows belong to the per-row
			// mount routine, not top-level update closures. Skip the
			// recursion.
			return
		}
		if n.Kind == ir.KindIf {
			bref := n.Bindings["visible"]
			initial, _ := n.Props["initial"].(bool)
			site := &ifSite{sid: n.ID, cellID: bref.CellID, initial: initial}
			ifSites[n.ID] = site
			ifOrder = append(ifOrder, n.ID)
			bindings[bref.CellID] = append(bindings[bref.CellID], bindingSite{
				sid: n.ID, prop: "visible", ifSid: ifSid, cellID: bref.CellID,
			})
			for _, c := range n.Children {
				walk(c, n.ID)
			}
			return
		}
		for prop, ref := range n.Bindings {
			bs := bindingSite{
				sid: n.ID, prop: prop, template: ref.Template,
				ifSid: ifSid, cellID: ref.CellID,
			}
			// Bar's "fill" binding carries the max from the node's
			// props so the update function can compute width pct.
			if prop == "fill" {
				if m, ok := n.Props["max"].(int); ok {
					bs.max = m
				}
			}
			bindings[ref.CellID] = append(bindings[ref.CellID], bs)
			if ifSid != "" {
				site := ifSites[ifSid]
				site.children = append(site.children, bs)
			}
		}
		for ev, a := range n.Handlers {
			handlers = append(handlers, handlerSite{sid: n.ID, event: ev, action: a})
		}
		for _, c := range n.Children {
			walk(c, ifSid)
		}
	}
	walk(doc.Root, "")

	// Stable orderings so output diffs cleanly across rebuilds.
	cellIDs := make([]string, 0, len(doc.Cells))
	for id := range doc.Cells {
		cellIDs = append(cellIDs, id)
	}
	sort.Strings(cellIDs)
	for _, id := range cellIDs {
		sites := bindings[id]
		sort.SliceStable(sites, func(i, j int) bool {
			if sites[i].sid != sites[j].sid {
				return sites[i].sid < sites[j].sid
			}
			return sites[i].prop < sites[j].prop
		})
		bindings[id] = sites
	}

	// Detect whether any handler eventually invokes create_batch_random;
	// the random-label vocabulary + helper only ships when something
	// actually uses it.
	usesRandomLabel := false
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
			usesRandomLabel = true
			break
		}
	}

	var b strings.Builder
	b.WriteString("(() => {\n")

	// 0. Conditional shared helpers. randomLabel() picks one word from
	// each of three baked vocabularies — the canonical
	// js-framework-benchmark word lists so leaderboard entries are
	// visually comparable. Only ships when create_batch_random is in
	// the app.
	if usesRandomLabel {
		b.WriteString(`  const ADJ = ["pretty","large","big","small","tall","short","long","handsome","plain","quaint","clean","elegant","easy","angry","crazy","helpful","mushy","odd","unsightly","adorable","important","inexpensive","cheap","expensive","fancy"];
  const COL = ["red","yellow","blue","green","pink","brown","purple","brown","white","black","orange"];
  const NOUN = ["table","chair","house","bbq","desk","car","pony","cookie","sandwich","burger","pizza","mouse","keyboard"];
  const pick = (a) => a[(Math.random() * a.length) | 0];
  const randomLabel = () => pick(ADJ) + " " + pick(COL) + " " + pick(NOUN);
`)
	}

	// 1. Cell declarations as JS locals. No registry; no id→name map.
	// List-children live in per-list cells_<parent> maps emitted later
	// — skip them here. List parents are emitted as JS arrays.
	for _, id := range cellIDs {
		if listChildIDs[id] {
			continue
		}
		if listParentIDs[id] {
			// The parent's value is an ordered slice of child cell ids.
			// Lower emits this as []string; tolerate []any too in case
			// of future tweaks.
			ids := listParentIDStrings(doc.Cells[id])
			fmt.Fprintf(&b, "  let %s = [", id)
			for i, cid := range ids {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%q", cid)
			}
			b.WriteString("];\n")
			continue
		}
		fmt.Fprintf(&b, "  let %s = %s;\n", id, jsLiteral(doc.Cells[id]))
	}

	// 1b. Per-list state: child-cell map, row-element map, mint counter.
	for _, fs := range forSites {
		fmt.Fprintf(&b, "  const cells_%s = {", fs.parentCellID)
		// Stable order over initial children.
		for i, cid := range fs.initialIDs {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, " %s: %s", cid, jsLiteral(fs.initialVals[cid]))
		}
		if len(fs.initialIDs) > 0 {
			b.WriteString(" ")
		}
		b.WriteString("};\n")
		fmt.Fprintf(&b, "  const rows_%s = Object.create(null);\n", fs.parentCellID)
		fmt.Fprintf(&b, "  let counter_%s = 0;\n", fs.parentCellID)
	}

	// 2. Top-level binding resolution. Bindings inside an if-subtree
	// are resolved per-mount; here we only grab the always-mounted ones.
	for _, id := range cellIDs {
		for _, s := range bindings[id] {
			if s.prop == "visible" {
				// Visibility "binding" is really the if-site itself; no
				// element reference variable. mount/unmount handles it.
				continue
			}
			if s.ifSid != "" {
				// Conditional — declare as let, initialize from current
				// DOM if SSR mounted the subtree (i.e. the if was true
				// at compile time), else leave null.
				site := ifSites[s.ifSid]
				if site.initial {
					fmt.Fprintf(&b, "  let %s = document.querySelector('[data-sid=\"%s\"]');\n",
						bindVar(s.sid, s.prop), bindElementSid(s))
				} else {
					fmt.Fprintf(&b, "  let %s = null;\n", bindVar(s.sid, s.prop))
				}
				continue
			}
			fmt.Fprintf(&b, "  const %s = document.querySelector('[data-sid=\"%s\"]');\n",
				bindVar(s.sid, s.prop), bindElementSid(s))
		}
	}

	// 3. Per-if mount / unmount routines. Mount converts the inert
	// <template data-sid="…"> into a live <div> by cloning its
	// content, resolves each binding inside the subtree, and writes
	// current cell values. Unmount reverses the swap and nulls the
	// resolved-element vars so subsequent update_<cell>() calls skip
	// them.
	for _, ifID := range ifOrder {
		site := ifSites[ifID]
		clean := strings.ReplaceAll(ifID, "/", "_")
		fmt.Fprintf(&b, "  let if%s_mounted = %t;\n", clean, site.initial)
		fmt.Fprintf(&b, "  const mount%s = (root) => {\n", clean)
		for _, child := range site.children {
			fmt.Fprintf(&b, "    %s = root.querySelector('[data-sid=\"%s\"]');\n",
				bindVar(child.sid, child.prop), child.sid)
			emitBindingWrite(&b, child, "    ")
		}
		b.WriteString("  };\n")
		fmt.Fprintf(&b, "  const unmount%s = () => {\n", clean)
		for _, child := range site.children {
			fmt.Fprintf(&b, "    %s = null;\n", bindVar(child.sid, child.prop))
		}
		b.WriteString("  };\n")
		// If SSR mounted the subtree (initial=true), resolve inner
		// bindings now so update_<cell> can write through immediately.
		if site.initial {
			fmt.Fprintf(&b, "  mount%s(document.querySelector('[data-sid=\"%s\"]'));\n",
				clean, ifID)
		}
	}

	// 4. Per-cell update closure. Writes every binding for the cell,
	// with null guards for conditional ones. For visibility bindings,
	// dispatch to the if-site swap routine.
	for _, id := range cellIDs {
		sites := bindings[id]
		if len(sites) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  const update_%s = () => {\n", id)
		for _, s := range sites {
			if s.prop == "visible" {
				emitIfSwap(&b, s.sid, id, "    ")
				continue
			}
			if s.ifSid != "" {
				fmt.Fprintf(&b, "    if (%s) ", bindVar(s.sid, s.prop))
				emitBindingWriteInline(&b, s, id)
				continue
			}
			b.WriteString("    ")
			emitBindingWriteInline(&b, s, id)
		}
		b.WriteString("  };\n")
	}

	// 4b. Per-for mount/append/remove + bulk routines. Each routine
	// is emitted only when an action of the matching kind actually
	// targets this parent list cell. Counters that only do
	// append/remove don't ship the swap/clear/select/etc. closures
	// they'd never call.
	actionsByParent := actionsTargetingLists(handlers)
	for _, fs := range forSites {
		emitForRoutines(&b, fs, actionsByParent[fs.parentCellID])
	}

	// 5. Wire each handler. Action is inlined; followed by a call to
	// the target cell's update_ closure so all bindings (including
	// visibility, including templated text) flush.
	sort.Slice(handlers, func(i, j int) bool {
		if handlers[i].sid != handlers[j].sid {
			return handlers[i].sid < handlers[j].sid
		}
		return handlers[i].event < handlers[j].event
	})
	// emitOneTopAction emits one action plus its post-flush. Recurses
	// for sequence so multi-statement handlers expand inline.
	var emitOneTopAction func(a ir.Action)
	emitOneTopAction = func(a ir.Action) {
		if a.Kind == "sequence" {
			inner, _ := a.Args["actions"].([]any)
			for _, raw := range inner {
				if ia, ok := raw.(ir.Action); ok {
					emitOneTopAction(ia)
				}
			}
			return
		}
		emitAction(&b, a)
		// Mutating-without-binding actions flush internally; append/remove
		// flush via their per-list routines; everything else calls
		// update_<cell> on the affected top-level cell.
		switch a.Kind {
		case "append_item", "remove_item", "append_struct_item":
			// list-cell actions; per-list routines own the DOM update
		case "call_op_spread":
			// Record spread writes many cells; flush each one with a
			// binding. `update_<cell>()` is only emitted for cells
			// that have bindings, so the hasBinding gate keeps the
			// JS from referencing missing symbols.
			spread, _ := a.Args["spread"].([]any)
			for _, raw := range spread {
				leaf, _ := raw.(map[string]any)
				cell, _ := leaf["cell"].(string)
				if hasBinding(bindings, cell) {
					fmt.Fprintf(&b, "    update_%s();\n", cell)
				}
			}
		default:
			if hasBinding(bindings, a.CellID) {
				fmt.Fprintf(&b, "    update_%s();\n", a.CellID)
			}
		}
	}

	for _, h := range handlers {
		// `async` is only emitted when the handler tree actually awaits;
		// keeps op-less handlers (the existing UI examples) byte-identical
		// in goldens and avoids the implicit Promise return for the no-op
		// case.
		asyncMod := ""
		if actionUsesAwait(h.action) {
			asyncMod = "async "
		}
		fmt.Fprintf(&b, "  document.querySelector('[data-sid=\"%s\"]').on%s = %s(event) => {\n",
			h.sid, h.event, asyncMod)
		emitOneTopAction(h.action)
		b.WriteString("  };\n")
	}

	// 5b. Mount each SSR-rendered row through the same mount routine
	// the append path uses. Keeps initial-paint + post-append behavior
	// identical (no SSR-specific code path).
	for _, fs := range forSites {
		for _, cellID := range fs.initialIDs {
			fmt.Fprintf(&b,
				"  mount_%s(document.querySelector('[data-sid=\"%s\"] [data-sigil-for-item=\"%s\"]'), %q);\n",
				cleanID(fs.sid), fs.sid, cellID, cellID)
		}
	}

	// 6. Test hook: live cell getters so `sigil test` can read state.
	// Top-level vars get direct getters; list-child cells are exposed
	// via a Proxy that funnels lookups through the per-list cells_<p>
	// maps so dynamically-appended cells (created post-page-load) are
	// visible to tests too.
	b.WriteString("  window.__sigil_cells = new Proxy({}, { get(_, p) {\n")
	for _, id := range cellIDs {
		if listChildIDs[id] {
			continue
		}
		fmt.Fprintf(&b, "    if (p === %q) return %s;\n", id, id)
	}
	for _, fs := range forSites {
		fmt.Fprintf(&b, "    if (p in cells_%s) return cells_%s[p];\n",
			fs.parentCellID, fs.parentCellID)
	}
	b.WriteString("    return undefined;\n  }});\n")

	// 7. Client stubs: one async function per declared query / command.
	// Shape mirrors connect-web — POST a JSON body to a derived path,
	// return the parsed response. The args object is keyed by the
	// declared input names; positional call-site args map to those
	// names in declaration order. No validation here yet (that's a
	// follow-on commit); for now we trust the wire and let JSON parse
	// errors surface as throws.
	emitClientStubs(&b, doc)

	b.WriteString("})();\n")
	return b.String()
}

// emitClientStubs writes the runtime's request pipeline:
//
//   - `window.__sigil_backends` config map (URL + auth method + token
//     cell id per declared backend)
//   - `sigilFetch` shared helper: in-flight dedupe, query-result
//     cache keyed by (op, JSON args), command-driven cache eviction
//     by declared invalidates list
//   - `window.__sigil_ops` thin per-op stubs that route into sigilFetch
//
// The dedupe + cache here is what kills the "repeated fetch on every
// re-render / repeated click" pattern. Queries with the same args
// return from cache until a command that lists them as invalidates
// resolves; concurrent identical requests share one in-flight Promise.
//
// Empty docs (no ops AND no backends) emit nothing — keeps the bundle
// tight for the UI-only examples that don't touch the network.
func emitClientStubs(b *strings.Builder, doc ir.Document) {
	if len(doc.Queries) == 0 && len(doc.Commands) == 0 && len(doc.Streams) == 0 && len(doc.Backends) == 0 {
		return
	}

	// 1. Backend configuration. Read-only at runtime; the fetch
	// helper consults this to build URLs + auth headers.
	b.WriteString("  window.__sigil_backends = {\n")
	for _, bk := range doc.Backends {
		fmt.Fprintf(b, "    %s: { url: %q, auth: %q",
			bk.Name, bk.URL, bk.Auth.Method)
		if bk.Auth.TokenCellID != "" {
			fmt.Fprintf(b, ", tokenCellID: %q", bk.Auth.TokenCellID)
		}
		b.WriteString(" },\n")
	}
	b.WriteString("  };\n")

	// 2. Per-op call tables. Lookup-by-name during sigilFetch so
	// the runtime knows each op's backend + invalidations.
	b.WriteString("  const __sigil_query_meta = {\n")
	for _, q := range doc.Queries {
		fmt.Fprintf(b, "    %s: { backend: %q },\n", q.Name, q.Backend)
	}
	b.WriteString("  };\n")
	b.WriteString("  const __sigil_command_meta = {\n")
	for _, c := range doc.Commands {
		fmt.Fprintf(b, "    %s: { backend: %q, invalidates: [", c.Name, c.Backend)
		for i, inv := range c.Invalidates {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%q", inv)
		}
		b.WriteString("] },\n")
	}
	b.WriteString("  };\n")
	b.WriteString("  const __sigil_stream_meta = {\n")
	for _, s := range doc.Streams {
		if len(s.Channels) > 0 {
			// Multi-channel stream: the runtime frames the body as NDJSON
			// and demuxes each line by its channel. `channels` lists the
			// valid channel names (the record's field names).
			quoted := make([]string, len(s.Channels))
			for i, ch := range s.Channels {
				quoted[i] = fmt.Sprintf("%q", ch)
			}
			fmt.Fprintf(b, "    %s: { backend: %q, channels: [%s] },\n",
				s.Name, s.Backend, strings.Join(quoted, ", "))
			continue
		}
		fmt.Fprintf(b, "    %s: { backend: %q },\n", s.Name, s.Backend)
	}
	b.WriteString("  };\n")

	// 3. Shared request pipeline. `__sigil_inflight` dedupes
	// concurrent identical requests (Promise share); `__sigil_cache`
	// retains resolved query results keyed by (op, JSON-args) until
	// a command's invalidates list evicts them. Commands also
	// dedupe in-flight (two rapid clicks of "save" coalesce) but
	// never cache — same-args commands repeated later DO refire.
	b.WriteString(`  window.__sigil_inflight = Object.create(null);
  window.__sigil_cache    = Object.create(null);
  async function sigilFetch(op, kind, args, meta) {
    const argsKey = JSON.stringify(args || {});
    const cacheKey = op + ":" + argsKey;
    if (kind === "query" && cacheKey in window.__sigil_cache) {
      return window.__sigil_cache[cacheKey];
    }
    if (cacheKey in window.__sigil_inflight) {
      return window.__sigil_inflight[cacheKey];
    }
    const cfg = meta.backend ? window.__sigil_backends[meta.backend] : null;
    const headers = { "Content-Type": "application/json" };
    if (cfg && cfg.auth === "bearer" && cfg.tokenCellID) {
      const token = window.__sigil_cells[cfg.tokenCellID];
      if (token) headers["Authorization"] = "Bearer " + token;
    }
    const credentials = cfg && cfg.auth === "cookie" ? "include" : "same-origin";
    const url = (cfg ? cfg.url : "") + "/" + kind + "/" + op;
    const promise = (async () => {
      const res = await fetch(url, {
        method: "POST", headers, credentials,
        body: argsKey,
      });
      if (!res.ok) throw new Error(op + ": " + res.status);
      return res.json();
    })();
    window.__sigil_inflight[cacheKey] = promise;
    promise.finally(() => { delete window.__sigil_inflight[cacheKey]; });
    if (kind === "query") {
      window.__sigil_cache[cacheKey] = promise;
      promise.catch(() => { delete window.__sigil_cache[cacheKey]; });
    } else if (meta.invalidates && meta.invalidates.length) {
      promise.then(() => {
        for (const q of meta.invalidates) {
          const prefix = q + ":";
          for (const key of Object.keys(window.__sigil_cache)) {
            if (key.startsWith(prefix)) delete window.__sigil_cache[key];
          }
        }
      });
    }
    return promise;
  }
`)

	// Per-op count of in-flight calls. The handler-side lifecycle
	// wrapper increments around each call so `<Op>.pending` only falls
	// when the LAST overlapping call settles — shared by streams (held-
	// open connections) and commands (request/response round-trips), so
	// it's declared whenever either kind exists.
	if len(doc.Streams) > 0 || len(doc.Commands) > 0 {
		b.WriteString("  window.__sigil_op_open = Object.create(null);\n")
	}

	// 3b. Streaming pipeline. Unlike sigilFetch, a stream is never cached
	// or deduped — each call opens a fresh connection and the response
	// body is consumed incrementally. onDelta fires once per decoded
	// chunk; the caller patches the bound cell and flushes per chunk.
	if len(doc.Streams) > 0 {
		b.WriteString(`  async function sigilStream(op, args, meta, onDelta) {
    const cfg = meta.backend ? window.__sigil_backends[meta.backend] : null;
    const headers = { "Content-Type": "application/json" };
    if (cfg && cfg.auth === "bearer" && cfg.tokenCellID) {
      const token = window.__sigil_cells[cfg.tokenCellID];
      if (token) headers["Authorization"] = "Bearer " + token;
    }
    const credentials = cfg && cfg.auth === "cookie" ? "include" : "same-origin";
    const url = (cfg ? cfg.url : "") + "/stream/" + op;
    const res = await fetch(url, { method: "POST", headers, credentials, body: JSON.stringify(args || {}) });
    if (!res.ok || !res.body) throw new Error(op + ": " + res.status);
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    if (meta.channels) {
      // Multi-channel: the body is NDJSON, one {"channel","text"} per
      // line. Buffer across chunks (a read can split a line), parse each
      // complete line, and dispatch (channel, text) to the demux callback.
      let buf = "";
      const emit = (line) => {
        line = line.trim();
        if (!line) return;
        let obj;
        try { obj = JSON.parse(line); } catch (e) { return; }
        if (!obj || obj.channel == null) return;
        // "__error" is the reserved channel a generated server uses to
        // carry a mid-stream failure in-band (the status line is long
        // gone by then). Throwing here rejects the stream call, so the
        // op's .failed/.error cells trip exactly like a pre-delta 502.
        if (obj.channel === "__error") throw new Error(op + ": " + (obj.text != null ? obj.text : "stream failed"));
        onDelta(obj.channel, obj.text != null ? obj.text : "");
      };
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        let nl;
        while ((nl = buf.indexOf("\n")) >= 0) {
          emit(buf.slice(0, nl));
          buf = buf.slice(nl + 1);
        }
      }
      buf += dec.decode();
      emit(buf);
      return;
    }
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      const chunk = dec.decode(value, { stream: true });
      if (chunk) onDelta(chunk);
    }
    const tail = dec.decode();
    if (tail) onDelta(tail);
  }
`)
	}

	// 4. Per-op stubs — thin wrappers that build the args object
	// from positional params and dispatch via sigilFetch. The
	// surface stays unchanged (`__sigil_ops.X(...)` returns a
	// Promise) so existing call sites in handlers don't need to
	// know about the new pipeline.
	b.WriteString("  window.__sigil_ops = {\n")
	for _, q := range doc.Queries {
		emitOneStub(b, "query", q.Name, q.Inputs)
	}
	for _, c := range doc.Commands {
		emitOneStub(b, "command", c.Name, c.Inputs)
	}
	b.WriteString("  };\n")

	// Stream ops get their own table: each stub takes the declared args
	// plus a trailing onDelta callback and dispatches via sigilStream.
	if len(doc.Streams) > 0 {
		b.WriteString("  window.__sigil_ops_stream = {\n")
		for _, s := range doc.Streams {
			emitOneStreamStub(b, s.Name, s.Inputs)
		}
		b.WriteString("  };\n")
	}
}

// emitOneStreamStub writes one stream op's stub. Same arg-building as
// emitOneStub but with a trailing __onDelta param and dispatch through
// sigilStream + the stream meta table.
func emitOneStreamStub(b *strings.Builder, name string, inputs []ir.TypeFieldSpec) {
	params := make([]string, 0, len(inputs)+1)
	for _, in := range inputs {
		params = append(params, in.Name)
	}
	argObj := "{}"
	if len(params) > 0 {
		argObj = "{ " + strings.Join(params, ", ") + " }"
	}
	params = append(params, "__onDelta")
	fmt.Fprintf(b, "    %s: (%s) => sigilStream(%q, %s, __sigil_stream_meta[%q], __onDelta),\n",
		name, strings.Join(params, ", "), name, argObj, name)
}

// emitOneStub writes one operation's stub function. The body now
// just builds an args object and dispatches into sigilFetch; the
// real fetch / dedupe / cache / invalidate logic lives there.
func emitOneStub(b *strings.Builder, kind, name string, inputs []ir.TypeFieldSpec) {
	params := make([]string, 0, len(inputs))
	for _, in := range inputs {
		params = append(params, in.Name)
	}
	fmt.Fprintf(b, "    %s: (%s) => sigilFetch(%q, %q, ",
		name, strings.Join(params, ", "), name, kind)
	if len(params) == 0 {
		b.WriteString("{}")
	} else {
		fmt.Fprintf(b, "{ %s }", strings.Join(params, ", "))
	}
	metaTable := "__sigil_query_meta"
	if kind == "command" {
		metaTable = "__sigil_command_meta"
	}
	fmt.Fprintf(b, ", %s[%q]),\n", metaTable, name)
}

// bindVar returns the JS local-variable name for a (sid, prop) pair.
// Path separators get replaced so the identifier is legal.
func bindVar(sid, prop string) string {
	clean := strings.ReplaceAll(sid, "/", "_")
	return fmt.Sprintf("n%s_%s", clean, prop)
}

// bindElementSid returns the data-sid value to query for a binding's
// target DOM element. Most bindings target the binding-bearing node
// itself (the binding lives ON that element), but `fill` is special:
// the renderer emits the bar as outer<sid> containing inner<sid>/fill,
// and the fill update writes to the inner element's style.width.
// Threading "look up the inner sid for fill" through the codegen
// keeps the per-binding emit code declarative.
func bindElementSid(s bindingSite) string {
	if s.prop == "fill" {
		return s.sid + "/fill"
	}
	return s.sid
}

// emitBindingWrite emits a full statement that writes the binding's
// current value into its DOM target. Used inside mount routines where
// the element ref is freshly assigned and we know it's non-null.
func emitBindingWrite(b *strings.Builder, s bindingSite, indent string) {
	b.WriteString(indent)
	emitBindingWriteInline(b, s, s.cellID)
}

// emitBindingWriteInline emits the write expression for a binding,
// reading from cellID. Both update_<cell> closures and mount routines
// pass the binding's own cell id; no inference at write time.
//
// `value` bindings get a caret-preserving guard: writing .value resets
// the input's selection range, so if the new value matches what's
// already there, skip the write.
func emitBindingWriteInline(b *strings.Builder, s bindingSite, cellID string) {
	switch s.prop {
	case "text":
		fmt.Fprintf(b, "%s.textContent = %s;\n",
			bindVar(s.sid, s.prop), jsTextExpr(cellID, s.template))
	case "value":
		expr := jsTextExpr(cellID, s.template)
		fmt.Fprintf(b, "{ const v = %s; if (%s.value !== v) %s.value = v; }\n",
			expr, bindVar(s.sid, s.prop), bindVar(s.sid, s.prop))
	case "src":
		// Compare against the raw attribute (el.src reflects back the
		// absolute URL) so re-setting the same value doesn't reload the
		// frame's document.
		expr := jsTextExpr(cellID, s.template)
		fmt.Fprintf(b, "{ const v = %s; if (%s.getAttribute('src') !== v) %s.setAttribute('src', v); }\n",
			expr, bindVar(s.sid, s.prop), bindVar(s.sid, s.prop))
	case "fill":
		// Progress-bar width update. `max` is captured at compile time
		// from the bar node's max= kwarg, so the JS does just one
		// number op per change. Clamp at 0..100 to keep the visual
		// sane on out-of-range cell values without surfacing the
		// "your data was bad" diagnostic in the bar itself.
		max := s.max
		if max <= 0 {
			max = 100
		}
		fmt.Fprintf(b, "{ const p = Math.max(0, Math.min(100, Math.round((%s) * 100 / %d))); %s.style.width = p + '%%'; }\n",
			cellID, max, bindVar(s.sid, s.prop))
	}
}

// jsTextExpr returns the JS expression that produces the rendered
// string for a text/value binding: plain `String(cN)` when no
// template, or `"prefix" + String(cN) + "suffix"` when templated.
func jsTextExpr(cellID, template string) string {
	if template == "" {
		return fmt.Sprintf("String(%s)", cellID)
	}
	parts := strings.Split(template, "${0}")
	// Single-segment template (no placeholder) — emit as a static
	// literal. The compiler should only ever produce templates that
	// contain ${0}, but be defensive.
	if len(parts) == 1 {
		return jsQuote(parts[0])
	}
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			if b.Len() > 0 {
				b.WriteString(" + ")
			}
			fmt.Fprintf(&b, "String(%s)", cellID)
		}
		if p != "" {
			if b.Len() > 0 {
				b.WriteString(" + ")
			}
			b.WriteString(jsQuote(p))
		}
	}
	return b.String()
}

// emitIfSwap writes the in-place template↔div swap for an `if` site
// reacting to its visibility cell. The if's data-sid never changes;
// we replace one wrapper element with the other and reattach the
// children.
func emitIfSwap(b *strings.Builder, ifID, cellID, indent string) {
	clean := strings.ReplaceAll(ifID, "/", "_")
	fmt.Fprintf(b, "%sconst want%s = !!%s;\n", indent, clean, cellID)
	fmt.Fprintf(b, "%sif (want%s !== if%s_mounted) {\n", indent, clean, clean)
	fmt.Fprintf(b, "%s  const cur = document.querySelector('[data-sid=\"%s\"]');\n", indent, ifID)
	fmt.Fprintf(b, "%s  if (want%s) {\n", indent, clean)
	fmt.Fprintf(b, "%s    const live = document.createElement('div');\n", indent)
	fmt.Fprintf(b, "%s    for (const a of cur.attributes) live.setAttribute(a.name, a.value);\n", indent)
	fmt.Fprintf(b, "%s    for (const node of cur.content.childNodes) live.appendChild(node.cloneNode(true));\n", indent)
	fmt.Fprintf(b, "%s    cur.replaceWith(live);\n", indent)
	fmt.Fprintf(b, "%s    mount%s(live);\n", indent, clean)
	fmt.Fprintf(b, "%s  } else {\n", indent)
	fmt.Fprintf(b, "%s    const tmpl = document.createElement('template');\n", indent)
	fmt.Fprintf(b, "%s    for (const a of cur.attributes) tmpl.setAttribute(a.name, a.value);\n", indent)
	fmt.Fprintf(b, "%s    while (cur.firstChild) tmpl.content.appendChild(cur.firstChild);\n", indent)
	fmt.Fprintf(b, "%s    cur.replaceWith(tmpl);\n", indent)
	fmt.Fprintf(b, "%s    unmount%s();\n", indent, clean)
	fmt.Fprintf(b, "%s  }\n", indent)
	fmt.Fprintf(b, "%s  if%s_mounted = want%s;\n", indent, clean, clean)
	fmt.Fprintf(b, "%s}\n", indent)
}

func hasBinding(bindings map[string][]bindingSite, cellID string) bool {
	return len(bindings[cellID]) > 0
}

// emitAction writes the inline JS for one declarative action at the
// TOP-LEVEL scope. Row-scope actions (inside a for-row) go through
// jsRowActionBody which knows about $ITEM and cells_<parent>[cellId].
//
// For sequence actions the caller is responsible for emitting the
// post-action binding flushes — see the main emit loop in Emit().
func emitAction(b *strings.Builder, a ir.Action) {
	switch a.Kind {
	case "set":
		fmt.Fprintf(b, "    %s = %s;\n", a.CellID, jsActionArg(a.Args["value"]))
	case "add":
		fmt.Fprintf(b, "    %s = %s + %s;\n", a.CellID, a.CellID, jsActionArg(a.Args["delta"]))
	case "toggle":
		fmt.Fprintf(b, "    %s = !%s;\n", a.CellID, a.CellID)
	case "append_item":
		fmt.Fprintf(b, "    append_%s(%s);\n", a.CellID, jsActionArg(a.Args["value"]))
	case "remove_item":
		fmt.Fprintf(b, "    remove_%s(%s);\n", a.CellID, jsActionArg(a.Args["target"]))
	case "append_struct_item":
		fields, _ := a.Args["fields"].(map[string]any)
		fmt.Fprintf(b, "    append_struct_%s({", a.CellID)
		keys := sortedKeys(fields)
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",")
			}
			// Quote keys so dotted leaf names from `List<Record>` row
			// schemas (`"stats.hp"`) emit as valid JS object literals.
			fmt.Fprintf(b, " %q: %s", k, jsActionArg(fields[k]))
		}
		if len(keys) > 0 {
			b.WriteString(" ")
		}
		b.WriteString("});\n")
	case "clear_list":
		fmt.Fprintf(b, "    clear_%s();\n", a.CellID)
	case "swap_items":
		fmt.Fprintf(b, "    swap_items_%s(%s, %s);\n", a.CellID,
			jsActionArg(a.Args["i"]), jsActionArg(a.Args["j"]))
	case "select_in_list":
		fmt.Fprintf(b, "    select_in_list_%s(%s);\n", a.CellID,
			jsActionArg(a.Args["target"]))
	case "create_batch_random":
		fmt.Fprintf(b, "    create_batch_random_%s(%s, %s);\n", a.CellID,
			jsActionArg(a.Args["count"]), jsActionArg(a.Args["replace"]))
	case "update_every":
		fmt.Fprintf(b, "    update_every_%s(%s, %s);\n", a.CellID,
			jsActionArg(a.Args["stride"]), jsActionArg(a.Args["suffix"]))
	case "call_op":
		opName, _ := a.Args["op"].(string)
		callExpr := jsOpCallExpr(opName, a.Args["args"])
		if a.CellID == "" {
			fmt.Fprintf(b, "    await %s;\n", callExpr)
		} else {
			fmt.Fprintf(b, "    %s = await %s;\n", a.CellID, callExpr)
		}
	case "call_op_spread":
		// `state p : Pokemon` + `p = GetPokemon(id)` spreads the response
		// into the inflated leaf cells. Args["spread"] is a slice of
		// {path, cell} pairs giving each leaf's dotted JSON path on the
		// response and the JS variable name to write to. We wrap the
		// whole thing in a block so the intermediate `__r` doesn't
		// leak into the surrounding handler.
		opName, _ := a.Args["op"].(string)
		callExpr := jsOpCallExpr(opName, a.Args["args"])
		spread, _ := a.Args["spread"].([]any)
		b.WriteString("    {\n")
		fmt.Fprintf(b, "      const __r = await %s;\n", callExpr)
		for _, raw := range spread {
			leaf, _ := raw.(map[string]any)
			path, _ := leaf["path"].(string)
			cell, _ := leaf["cell"].(string)
			fmt.Fprintf(b, "      %s = __r.%s;\n", cell, path)
		}
		b.WriteString("    }\n")
	default:
		fmt.Fprintf(b, "    /* unsupported action %q */\n", a.Kind)
	}
}

// jsOpCallExpr renders one `window.__sigil_ops.<Name>(args…)` call
// expression. argsAny is the lowering-time []any of resolved args:
// literals embed via jsActionArg; cell-ref sentinels ("$cell.<id>")
// unwrap to the bare id so the runtime reads the live variable.
func jsOpCallExpr(opName string, argsAny any) string {
	args, _ := argsAny.([]any)
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if s, ok := a.(string); ok && strings.HasPrefix(s, "$cell.") {
			parts = append(parts, s[len("$cell."):])
			continue
		}
		parts = append(parts, jsActionArg(a))
	}
	return fmt.Sprintf("window.__sigil_ops.%s(%s)", opName, strings.Join(parts, ", "))
}

// actionUsesAwait reports whether an action (recursing into sequence)
// emits `await` — i.e. contains at least one call_op or call_op_spread.
// Used to decide whether the enclosing handler closure needs the
// `async` keyword.
func actionUsesAwait(a ir.Action) bool {
	if a.Kind == "call_op" || a.Kind == "call_op_spread" || a.Kind == "call_op_list" || a.Kind == "call_op_stream" {
		return true
	}
	if a.Kind == "sequence" {
		inner, _ := a.Args["actions"].([]any)
		for _, raw := range inner {
			if ia, ok := raw.(ir.Action); ok {
				if actionUsesAwait(ia) {
					return true
				}
			}
		}
	}
	return false
}

// sortedKeys returns the keys of m in alphabetical order so emitted
// code is deterministic across rebuilds.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cleanID makes a sid safe to embed in a JS identifier. Path-style
// data-sids contain `/`; we replace with `_`.
func cleanID(sid string) string {
	return strings.ReplaceAll(sid, "/", "_")
}

// listParentIDStrings normalizes a list parent's child-id slice
// across the two shapes the lowerer might emit ([]string today,
// []any if a future refactor widens the IR encoding).
func listParentIDStrings(v any) []string {
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// actionsTargetingLists scans every handler (including nested
// sequence inner actions) and returns a per-parent-cell set of action
// kinds used. emitForRoutines uses this to elide closures the app
// never invokes.
func actionsTargetingLists(handlers []handlerSite) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	var visit func(a ir.Action)
	visit = func(a ir.Action) {
		if a.Kind == "sequence" {
			inner, _ := a.Args["actions"].([]any)
			for _, raw := range inner {
				if ia, ok := raw.(ir.Action); ok {
					visit(ia)
				}
			}
			return
		}
		switch a.Kind {
		case "append_item", "remove_item", "append_struct_item",
			"clear_list", "swap_items", "select_in_list",
			"create_batch_random", "update_every":
			set, ok := out[a.CellID]
			if !ok {
				set = map[string]bool{}
				out[a.CellID] = set
			}
			set[a.Kind] = true
		case "call_op_list":
			// A query reassigning a list inside a handler (e.g. a
			// refetch after a mutation) replaces the list: it needs
			// the append helper to add rows and the clear helper to
			// drop the stale ones first.
			set, ok := out[a.CellID]
			if !ok {
				set = map[string]bool{}
				out[a.CellID] = set
			}
			set["append_struct_item"] = true
			set["clear_list"] = true
		}
	}
	for _, h := range handlers {
		visit(h.action)
	}
	return out
}

// emitForRoutines writes the per-list state machine: the mount
// routine (always emitted — every for-site needs to mount its rows)
// plus the action closures (each gated on actual usage so an app
// that only does append/remove doesn't ship swap/clear/select code).
func emitForRoutines(b *strings.Builder, fs forSite, uses map[string]bool) {
	p := fs.parentCellID
	cleanSid := cleanID(fs.sid)
	if uses == nil {
		uses = map[string]bool{}
	}

	// Pre-walk the template subtree to compute the per-row element
	// refs, initial writes, and handlers. The template's IR root is a
	// for_item; its children become rowEl's direct children, which
	// means descendant paths start at rowEl.children[i].
	emit := analyzeRow(fs.templateRow, p)

	// mount_<sid>: takes the row's element and its cell id (a JS
	// string); resolves descendants, writes initial values, wires
	// handlers. All references to `cellId` close over the closure
	// param, and all reads of $ITEM resolve to cells_<parent>[cellId].
	fmt.Fprintf(b, "  const mount_%s = (rowEl, cellId) => {\n", cleanSid)
	fmt.Fprintf(b, "    rows_%s[cellId] = rowEl;\n", p)
	for _, line := range emit.elementRefs {
		fmt.Fprintf(b, "    %s\n", line)
	}
	for _, line := range emit.initWrites {
		fmt.Fprintf(b, "    %s\n", line)
	}
	for _, line := range emit.handlers {
		fmt.Fprintf(b, "    %s\n", line)
	}
	b.WriteString("  };\n")

	// The template element ref is needed by ANY routine that produces
	// new rows (append, append_struct, create_batch). Emit once and
	// only when at least one user is present.
	needsTmplRef := uses["append_item"] || uses["append_struct_item"] || uses["create_batch_random"]
	if needsTmplRef {
		fmt.Fprintf(b, "  const tmpl_%s = document.querySelector('[data-sid=\"%s\"] template[data-sigil-for-template]');\n",
			cleanSid, fs.sid)
	}

	// The wrapper element ref is needed by clear/swap/select/create_batch
	// (anything that performs container-level DOM operations on the list).
	needsWrapperRef := uses["clear_list"] || uses["swap_items"] || uses["select_in_list"] || uses["create_batch_random"]
	if needsWrapperRef {
		fmt.Fprintf(b, "  const wrapper_%s = document.querySelector('[data-sid=\"%s\"]');\n", cleanSid, fs.sid)
	}

	// append_<parent>: mint a new child cell id, clone template, insert.
	if uses["append_item"] {
		fmt.Fprintf(b, "  const append_%s = (value) => {\n", p)
		fmt.Fprintf(b, "    const newId = 'r' + (++counter_%s);\n", p)
		fmt.Fprintf(b, "    cells_%s[newId] = value;\n", p)
		fmt.Fprintf(b, "    %s.push(newId);\n", p)
		fmt.Fprintf(b, "    const frag = tmpl_%s.content.cloneNode(true);\n", cleanSid)
		fmt.Fprintf(b, "    const newRow = frag.firstElementChild;\n")
		fmt.Fprintf(b, "    newRow.setAttribute('data-sigil-for-item', newId);\n")
		fmt.Fprintf(b, "    tmpl_%s.parentNode.insertBefore(newRow, tmpl_%s);\n", cleanSid, cleanSid)
		fmt.Fprintf(b, "    mount_%s(newRow, newId);\n", cleanSid)
		b.WriteString("  };\n")
	}

	// append_struct_<parent>: same DOM dance, with N flat dotted sub-cells.
	if uses["append_struct_item"] {
		fmt.Fprintf(b, "  const append_struct_%s = (fields) => {\n", p)
		fmt.Fprintf(b, "    const newId = 'r' + (++counter_%s);\n", p)
		fmt.Fprintf(b, "    for (const k of Object.keys(fields)) cells_%s[newId + '.' + k] = fields[k];\n", p)
		fmt.Fprintf(b, "    %s.push(newId);\n", p)
		fmt.Fprintf(b, "    const frag = tmpl_%s.content.cloneNode(true);\n", cleanSid)
		fmt.Fprintf(b, "    const newRow = frag.firstElementChild;\n")
		fmt.Fprintf(b, "    newRow.setAttribute('data-sigil-for-item', newId);\n")
		fmt.Fprintf(b, "    tmpl_%s.parentNode.insertBefore(newRow, tmpl_%s);\n", cleanSid, cleanSid)
		fmt.Fprintf(b, "    mount_%s(newRow, newId);\n", cleanSid)
		b.WriteString("  };\n")
	}

	// remove_<parent>: drop the row + sweep its dotted sub-cells.
	if uses["remove_item"] {
		fmt.Fprintf(b, "  const remove_%s = (cellId) => {\n", p)
		fmt.Fprintf(b, "    const i = %s.indexOf(cellId);\n", p)
		fmt.Fprintf(b, "    if (i < 0) return;\n")
		fmt.Fprintf(b, "    %s.splice(i, 1);\n", p)
		fmt.Fprintf(b, "    delete cells_%s[cellId];\n", p)
		fmt.Fprintf(b, "    const prefix = cellId + '.';\n")
		fmt.Fprintf(b, "    for (const k of Object.keys(cells_%s)) if (k.startsWith(prefix)) delete cells_%s[k];\n", p, p)
		fmt.Fprintf(b, "    const r = rows_%s[cellId];\n", p)
		fmt.Fprintf(b, "    if (r && r.parentNode) r.parentNode.removeChild(r);\n")
		fmt.Fprintf(b, "    delete rows_%s[cellId];\n", p)
		b.WriteString("  };\n")
	}

	// select tracking — only allocated when select_in_list is used. And
	// the variable also needs to exist if clear_list is in play (clear
	// resets selection), so emit it when either is present.
	if uses["select_in_list"] || uses["clear_list"] {
		fmt.Fprintf(b, "  let selected_%s = '';\n", p)
	}

	// clear_<parent>: empty + reset selection. textContent=""
	// trick is the runtime's perf-tested path.
	if uses["clear_list"] {
		fmt.Fprintf(b, "  const clear_%s = () => {\n", p)
		fmt.Fprintf(b, "    %s.length = 0;\n", p)
		fmt.Fprintf(b, "    for (const k of Object.keys(cells_%s)) delete cells_%s[k];\n", p, p)
		fmt.Fprintf(b, "    for (const k of Object.keys(rows_%s)) delete rows_%s[k];\n", p, p)
		fmt.Fprintf(b, "    wrapper_%s.textContent = '';\n", cleanSid)
		fmt.Fprintf(b, "    wrapper_%s.appendChild(tmpl_%s);\n", cleanSid, cleanSid)
		fmt.Fprintf(b, "    selected_%s = '';\n", p)
		b.WriteString("  };\n")
	}

	if uses["swap_items"] {
		fmt.Fprintf(b, "  const swap_items_%s = (i, j) => {\n", p)
		fmt.Fprintf(b, "    if (i < 0 || j < 0 || i >= %s.length || j >= %s.length || i === j) return;\n", p, p)
		fmt.Fprintf(b, "    const ai = %s[i], aj = %s[j];\n", p, p)
		fmt.Fprintf(b, "    %s[i] = aj; %s[j] = ai;\n", p, p)
		fmt.Fprintf(b, "    const rowI = wrapper_%s.querySelector('[data-sigil-for-item=\"' + ai + '\"]');\n", cleanSid)
		fmt.Fprintf(b, "    const rowJ = wrapper_%s.querySelector('[data-sigil-for-item=\"' + aj + '\"]');\n", cleanSid)
		fmt.Fprintf(b, "    if (!rowI || !rowJ) return;\n")
		fmt.Fprintf(b, "    const marker = document.createComment('');\n")
		fmt.Fprintf(b, "    rowJ.parentNode.insertBefore(marker, rowJ);\n")
		fmt.Fprintf(b, "    rowI.parentNode.insertBefore(rowJ, rowI);\n")
		fmt.Fprintf(b, "    marker.parentNode.insertBefore(rowI, marker);\n")
		fmt.Fprintf(b, "    marker.remove();\n")
		b.WriteString("  };\n")
	}

	if uses["select_in_list"] {
		fmt.Fprintf(b, "  const select_in_list_%s = (target) => {\n", p)
		fmt.Fprintf(b, "    if (!target) return;\n")
		fmt.Fprintf(b, "    if (selected_%s) {\n", p)
		fmt.Fprintf(b, "      const o = wrapper_%s.querySelector('[data-sigil-for-item=\"' + selected_%s + '\"]');\n", cleanSid, p)
		fmt.Fprintf(b, "      if (o) o.removeAttribute('data-sigil-tone-runtime');\n")
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "    selected_%s = target;\n", p)
		fmt.Fprintf(b, "    const c = wrapper_%s.querySelector('[data-sigil-for-item=\"' + target + '\"]');\n", cleanSid)
		fmt.Fprintf(b, "    if (c) c.setAttribute('data-sigil-tone-runtime', 'selected');\n")
		b.WriteString("  };\n")
	}

	if uses["create_batch_random"] {
		fmt.Fprintf(b, "  const create_batch_random_%s = (count, replace) => {\n", p)
		fmt.Fprintf(b, "    if (replace) clear_%s();\n", p)
		fmt.Fprintf(b, "    const frag = document.createDocumentFragment();\n")
		fmt.Fprintf(b, "    for (let i = 0; i < count; i++) {\n")
		fmt.Fprintf(b, "      const newId = 'r' + (++counter_%s);\n", p)
		fmt.Fprintf(b, "      const label = randomLabel();\n")
		fmt.Fprintf(b, "      cells_%s[newId] = label;\n", p)
		fmt.Fprintf(b, "      %s.push(newId);\n", p)
		fmt.Fprintf(b, "      const row = tmpl_%s.content.cloneNode(true).firstElementChild;\n", cleanSid)
		fmt.Fprintf(b, "      row.setAttribute('data-sigil-for-item', newId);\n")
		fmt.Fprintf(b, "      frag.appendChild(row);\n")
		fmt.Fprintf(b, "      mount_%s(row, newId);\n", cleanSid)
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "    tmpl_%s.parentNode.insertBefore(frag, tmpl_%s);\n", cleanSid, cleanSid)
		b.WriteString("  };\n")
	}

	if uses["update_every"] {
		// Re-call mount on each affected row — re-resolves refs and
		// writes current values. Wasteful (re-wires handlers) but
		// correct. Specialize later if a profile shows it matters.
		fmt.Fprintf(b, "  const update_every_%s = (stride, suffix) => {\n", p)
		fmt.Fprintf(b, "    if (stride <= 0) return;\n")
		fmt.Fprintf(b, "    for (let i = 0; i < %s.length; i += stride) {\n", p)
		fmt.Fprintf(b, "      const id = %s[i];\n", p)
		fmt.Fprintf(b, "      cells_%s[id] = String(cells_%s[id]) + suffix;\n", p, p)
		fmt.Fprintf(b, "      const r = rows_%s[id];\n", p)
		fmt.Fprintf(b, "      if (r) mount_%s(r, id);\n", cleanSid)
		fmt.Fprintf(b, "    }\n")
		b.WriteString("  };\n")
	}
}

// analyzeRow walks the for_item template subtree and produces the
// per-row emit fragments: descendant element refs, initial-value
// writes (so the row paints correctly on first mount), and handler
// wirings (each closes over the row's `cellId` param via JS scope).
//
// The template's IR root is the for_item itself; its children
// correspond directly to rowEl.children[i]. We assign an ascending
// element-ref var per encountered bind/handler site and remember
// each site's relative descendant path.
//
// Handlers also emit a flush of every binding on the cell they
// mutate — without that step, the DOM would drift after the first
// click (cell value changes, but textContent stays stale).
func analyzeRow(tmpl *ir.Node, parentCellID string) rowEmit {
	var out rowEmit
	if tmpl == nil {
		return out
	}
	refVars := map[string]string{}
	refVar := func(path []int) string {
		key := pathKey(path)
		if v, ok := refVars[key]; ok {
			return v
		}
		v := "e" + key
		refVars[key] = v
		expr := "rowEl"
		for _, idx := range path {
			expr = fmt.Sprintf("%s.children[%d]", expr, idx)
		}
		// `let` (not `const`) because if-site wrappers get reassigned
		// on template↔div swap. Cost vs const is zero in V8.
		out.elementRefs = append(out.elementRefs, fmt.Sprintf("let %s = %s;", v, expr))
		return v
	}

	// Two-pass walk: pass 1 collects bindings (so handler emits in
	// pass 2 know what to flush regardless of source order). Without
	// this, a handler on an earlier sibling than the binding it
	// observes would generate no DOM update and the row would drift.
	type rowBinding struct {
		elemVar  string
		prop     string
		template string
		cellExpr string
	}
	bindsByCell := map[string][]rowBinding{}

	type pendingHandler struct {
		elemVar string
		event   string
		action  ir.Action
	}
	var pending []pendingHandler

	var rowIfs []rowIfSite
	// cellID -> indices into rowIfs, so handler emit can call the
	// right swap_ routines after mutating that cell.
	ifsByCell := map[string][]int{}

	var collect func(n *ir.Node, path []int)
	collect = func(n *ir.Node, path []int) {
		if n == nil {
			return
		}
		if n.Kind == ir.KindIf {
			// Nested if inside the row. Reserve a let-bound element ref
			// (so the swap routine can reassign it) and a per-row
			// mount-state flag.
			ref, _ := n.Bindings["visible"]
			site := rowIfSite{
				key:      pathKey(path),
				elemVar:  refVar(path),
				cellID:   ref.CellID,
				cellExpr: rowCellExpr(ref.CellID, parentCellID),
			}
			idx := len(rowIfs)
			rowIfs = append(rowIfs, site)
			ifsByCell[ref.CellID] = append(ifsByCell[ref.CellID], idx)
			// This legacy emitter never wires bindings/handlers under a
			// row-if (the SPA emitter — the production path — does, via
			// build-time evaluation + row rebuild). Skip recursion.
			return
		}
		for prop, ref := range n.Bindings {
			elem := refVar(path)
			cellExpr := rowCellExpr(ref.CellID, parentCellID)
			expr := jsRowTextExpr(cellExpr, ref.Template)
			switch prop {
			case "text":
				out.initWrites = append(out.initWrites,
					fmt.Sprintf("%s.textContent = %s;", elem, expr))
			case "value":
				out.initWrites = append(out.initWrites,
					fmt.Sprintf("{ const v = %s; if (%s.value !== v) %s.value = v; }",
						expr, elem, elem))
			}
			bindsByCell[ref.CellID] = append(bindsByCell[ref.CellID], rowBinding{
				elemVar: elem, prop: prop, template: ref.Template, cellExpr: cellExpr,
			})
		}
		for ev, a := range n.Handlers {
			elem := refVar(path)
			pending = append(pending, pendingHandler{elemVar: elem, event: ev, action: a})
		}
		for i := range n.Children {
			c := n.Children[i]
			collect(&c, append(path, i))
		}
	}
	for i := range tmpl.Children {
		c := tmpl.Children[i]
		collect(&c, []int{i})
	}

	// Per-row if-site setup lines. Emitted after element refs, before
	// handlers, so the swap routines exist when handlers reference
	// them. Each site emits:
	//   let if_<key>_mounted = !!<cellExpr>;
	//   const swap_<key> = () => { ... };
	for _, site := range rowIfs {
		out.initWrites = append(out.initWrites, rowIfState(site)...)
	}

	// Pass 2: emit handlers with full visibility of every binding +
	// every nested if-site.
	for _, h := range pending {
		body := jsRowActionBody(h.action, parentCellID)
		flushes := ""
		if h.action.Kind != "append_item" && h.action.Kind != "remove_item" {
			for _, rb := range bindsByCell[h.action.CellID] {
				expr := jsRowTextExpr(rb.cellExpr, rb.template)
				switch rb.prop {
				case "text":
					flushes += fmt.Sprintf(" %s.textContent = %s;", rb.elemVar, expr)
				case "value":
					flushes += fmt.Sprintf(" { const v = %s; if (%s.value !== v) %s.value = v; }",
						expr, rb.elemVar, rb.elemVar)
				}
			}
			// Call swap_<key> for every nested if-site whose visibility
			// cell was just mutated.
			for _, idx := range ifsByCell[h.action.CellID] {
				flushes += fmt.Sprintf(" swap_%s();", rowIfs[idx].key)
			}
		}
		asyncMod := ""
		if actionUsesAwait(h.action) {
			asyncMod = "async "
		}
		out.handlers = append(out.handlers,
			fmt.Sprintf("%s.on%s = %s(event) => { %s%s };", h.elemVar, h.event, asyncMod, body, flushes))
	}
	return out
}

// rowIfState emits the per-row if-site setup: a `let if_<key>_mounted`
// flag initialized from the controlling cell, plus a `swap_<key>`
// closure that flips the inert-template / live-div wrapper and
// updates the row's element-ref var. The wrapper var is assumed to be
// declared upstream by refVar (now always a `let`).
func rowIfState(site rowIfSite) []string {
	out := []string{
		fmt.Sprintf("let if_%s_mounted = !!%s;", site.key, site.cellExpr),
		fmt.Sprintf("const swap_%s = () => {", site.key),
		fmt.Sprintf("  const want = !!%s;", site.cellExpr),
		fmt.Sprintf("  if (want === if_%s_mounted) return;", site.key),
		fmt.Sprintf("  if (want) {"),
		fmt.Sprintf("    const live = document.createElement('div');"),
		fmt.Sprintf("    for (const a of %s.attributes) live.setAttribute(a.name, a.value);", site.elemVar),
		fmt.Sprintf("    for (const node of %s.content.childNodes) live.appendChild(node.cloneNode(true));", site.elemVar),
		fmt.Sprintf("    %s.replaceWith(live);", site.elemVar),
		fmt.Sprintf("    %s = live;", site.elemVar),
		fmt.Sprintf("  } else {"),
		fmt.Sprintf("    const t = document.createElement('template');"),
		fmt.Sprintf("    for (const a of %s.attributes) t.setAttribute(a.name, a.value);", site.elemVar),
		fmt.Sprintf("    while (%s.firstChild) t.content.appendChild(%s.firstChild);", site.elemVar, site.elemVar),
		fmt.Sprintf("    %s.replaceWith(t);", site.elemVar),
		fmt.Sprintf("    %s = t;", site.elemVar),
		fmt.Sprintf("  }"),
		fmt.Sprintf("  if_%s_mounted = want;", site.key),
		fmt.Sprintf("};"),
	}
	return out
}

func pathKey(path []int) string {
	var b strings.Builder
	for _, p := range path {
		fmt.Fprintf(&b, "_%d", p)
	}
	return b.String()
}

// rowCellExpr renders a cell reference inside a for-row. The loop
// variable $ITEM (or its dotted sub-fields $ITEM.X) routes through
// the per-list cells_<parent> map keyed by `cellId` (or `cellId + ".X"`).
// Other cell ids resolve to themselves (e.g. the parent list cell in
// remove_item's target=…).
func rowCellExpr(cellID, parentCellID string) string {
	if cellID == "$ITEM" {
		return fmt.Sprintf("cells_%s[cellId]", parentCellID)
	}
	if strings.HasPrefix(cellID, "$ITEM.") {
		field := cellID[len("$ITEM."):]
		return fmt.Sprintf("cells_%s[cellId + %q]", parentCellID, "."+field)
	}
	return cellID
}

// jsRowTextExpr is like jsTextExpr but takes a pre-rendered cell
// access expression (so it can be either `cN` or `cells_p[cellId]`).
func jsRowTextExpr(cellExpr, template string) string {
	if template == "" {
		return "String(" + cellExpr + ")"
	}
	parts := strings.Split(template, "${0}")
	if len(parts) == 1 {
		return jsQuote(parts[0])
	}
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			if b.Len() > 0 {
				b.WriteString(" + ")
			}
			fmt.Fprintf(&b, "String(%s)", cellExpr)
		}
		if p != "" {
			if b.Len() > 0 {
				b.WriteString(" + ")
			}
			b.WriteString(jsQuote(p))
		}
	}
	return b.String()
}

// jsRowActionBody returns the JS statements (no trailing newline) for
// one action inside a for-row handler. The caller is responsible for
// appending the post-action binding flushes (see analyzeRow).
func jsRowActionBody(a ir.Action, parentCellID string) string {
	target := rowCellExpr(a.CellID, parentCellID)
	switch a.Kind {
	case "set":
		return fmt.Sprintf("%s = %s;", target, jsRowArg(a.Args["value"], parentCellID))
	case "add":
		return fmt.Sprintf("%s = %s + %s;", target, target, jsRowArg(a.Args["delta"], parentCellID))
	case "toggle":
		return fmt.Sprintf("%s = !%s;", target, target)
	case "append_item":
		return fmt.Sprintf("append_%s(%s);", a.CellID, jsRowArg(a.Args["value"], parentCellID))
	case "remove_item":
		return fmt.Sprintf("remove_%s(%s);", a.CellID, jsRowArg(a.Args["target"], parentCellID))
	case "call_op":
		opName, _ := a.Args["op"].(string)
		args, _ := a.Args["args"].([]any)
		parts := make([]string, 0, len(args))
		for _, arg := range args {
			parts = append(parts, jsRowArg(arg, parentCellID))
		}
		callExpr := fmt.Sprintf("window.__sigil_ops.%s(%s)", opName, strings.Join(parts, ", "))
		if a.CellID == "" {
			return fmt.Sprintf("await %s;", callExpr)
		}
		return fmt.Sprintf("%s = await %s;", target, callExpr)
	}
	return fmt.Sprintf("/* unsupported row action %q */", a.Kind)
}

// jsRowArg is jsActionArg but inside a row context: cell sentinels
// resolving to $ITEM (the row's primary cell) or one of its dotted
// sub-fields are read through the per-list cells_<parent> map. The
// closure's `cellId` param is the row's id; sub-field reads append
// the dotted leaf path to it.
//
// Top-level cells (`$cell.cN` for some cN that isn't $ITEM-prefixed)
// resolve to their bare variable name — the same as the
// non-row context — since those vars are still in scope.
func jsRowArg(v any, parentCellID string) string {
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

// jsActionArg renders an action argument, resolving the `$event.X` and
// `$cell.X` sentinels that the lowerer embeds in handler payloads.
// `$event.value` becomes `event.target.value`; `$cell.cN` becomes a
// read of the cell variable. Plain literals fall through to jsLiteral.
func jsActionArg(v any) string {
	if s, ok := v.(string); ok && strings.HasPrefix(s, "$") {
		switch {
		case strings.HasPrefix(s, "$event."):
			return "event.target." + s[len("$event."):]
		case strings.HasPrefix(s, "$cell."):
			return s[len("$cell."):]
		}
	}
	return jsLiteral(v)
}

// jsLiteral renders an `any` value as a JS literal. Numbers are
// printed without scientific notation for small ints; strings get
// JSON-style escaping; bools as bare true/false.
func jsLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case float64:
		// Integer-valued floats render as ints (avoids "1.0" cluttering
		// the output for the overwhelmingly common case).
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case string:
		return jsQuote(x)
	case ir.UnionValue:
		// Discriminated-union value: `{ tag, value }`. Unit variants
		// carry a null value.
		return fmt.Sprintf("{ tag: %s, value: %s }", jsQuote(x.Tag), jsLiteral(x.Value))
	default:
		return "null"
	}
}

// jsQuote does the minimum string-literal escaping needed: backslash,
// double-quote, and the control characters that break JS lexing.
func jsQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		case '<':
			// The bundle is emitted inside an inline <script>; a source
			// string containing "</script>" would close the element no
			// matter how the JS string is quoted. Escaping every '<' as
			// \x3C (=== "<" in JS) neutralizes that, and "<!--" too.
			b.WriteString("\\x3C")
		case '\u2028':
			b.WriteString("\\u2028") // line separator: legal in JS strings, breaks the parser
		case '\u2029':
			b.WriteString("\\u2029") // paragraph separator: same
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
