// Package lower turns a parsed AST into an ir.Document the existing
// renderers can consume.
package lower

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/incantery/mako/pkg/ir"
	"github.com/incantery/mako/pkg/lang/ast"
	"github.com/incantery/mako/pkg/lang/diag"
	"github.com/incantery/mako/pkg/lang/icons"
	"github.com/incantery/mako/pkg/theme"
)

// applyTone validates a `tone=` kwarg value against Sigil's closed
// intent-tone vocabulary and stores it on the IR node. The tone is
// realized at render time per active theme — primitives never see colors.
func applyTone(n *ir.Node, v ast.Value) error {
	if v.Kind != ast.ValueIdent {
		return diag.New("lower", v.Pos.Line, v.Pos.Col,
			"tone= must be an ident (e.g. primary, danger, success)")
	}
	for _, valid := range theme.IntentTones {
		if v.String == valid {
			n.Props["tone"] = v.String
			return nil
		}
	}
	d := diag.New("lower", v.Pos.Line, v.Pos.Col,
		fmt.Sprintf("unknown tone %q", v.String))
	if hint := suggestFromSet(v.String, theme.IntentTones); hint != "" {
		d.Suggestion = "did you mean " + hint + "?"
	}
	return d
}

// acceptToneKwarg is the small kwarg loop for primitives whose ONLY
// supported kwarg is `tone=`. Callers that take more kwargs (button,
// stack, input) handle the loop inline. Centralizes the "unknown kwarg
// with suggestion" diagnostic so simple primitives stay terse.
func acceptToneKwarg(n *ir.Node, kwargs map[string]ast.Value, kindName string) error {
	return acceptToneAndSizeKwarg(n, kwargs, kindName, nil)
}

// titleSizes / textSizes are the closed size enums the renderer
// knows about. These map 1:1 to entries in the theme's TextScale —
// `lg` → `heading-lg`, `caption` → `caption`, etc. Renderers
// resolve the named scale; the Sigil source never mentions pixels
// or weights directly.
var (
	titleSizes      = []string{"lg", "md", "sm"}
	textSizes       = []string{"body", "body-strong", "caption"}
	cardElevations  = []string{"flat", "sm", "md", "lg"}
	containerWidths = []string{"narrow", "medium", "wide", "full"}
	radiusTokens    = []string{"none", "sm", "md", "lg", "xl", "xxl", "full"}
	alignSelfValues = []string{"start", "center", "end", "stretch"}
	spaceTokens     = []string{"xs", "sm", "md", "lg", "xl"}
)

// applySpaceKwarg validates padding= / padx= / pady= — a space-scale
// token, or an integer for pixel values between scale steps (the Aura
// pill's 12/18 per-axis padding isn't on the scale). Stored as the
// token name or "<n>px"; resolvers branch on the suffix.
func applySpaceKwarg(n *ir.Node, prim, key string, v ast.Value) error {
	switch v.Kind {
	case ast.ValueIdent:
		for _, s := range spaceTokens {
			if v.String == s {
				n.Props[key] = v.String
				return nil
			}
		}
		d := diag.New("lower", v.Pos.Line, v.Pos.Col,
			fmt.Sprintf("%s: unknown %s token %q (want one of %s, or an integer pixel value)",
				prim, key, v.String, strings.Join(spaceTokens, ", ")))
		if hint := suggestFromSet(v.String, spaceTokens); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return d
	case ast.ValueInt:
		if v.Int < 0 {
			return diag.New("lower", v.Pos.Line, v.Pos.Col,
				fmt.Sprintf("%s: %s= must not be negative", prim, key))
		}
		n.Props[key] = fmt.Sprintf("%dpx", v.Int)
		return nil
	}
	return diag.New("lower", v.Pos.Line, v.Pos.Col,
		fmt.Sprintf("%s: %s= takes a space token (%s) or an integer pixel value",
			prim, key, strings.Join(spaceTokens, ", ")))
}

// textSizeVocab returns the `size=` vocabulary for text/badge: the
// built-in scale plus any tokens declared via theme-block `text`
// bindings (sorted, so diagnostics stay deterministic).
func (l *lowerer) textSizeVocab() []string {
	if len(l.themeTextTokens) == 0 {
		return textSizes
	}
	out := append([]string{}, textSizes...)
	extra := make([]string, 0, len(l.themeTextTokens))
	for k := range l.themeTextTokens {
		known := false
		for _, s := range out {
			if s == k {
				known = true
				break
			}
		}
		if !known {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// applyRadius validates a `radius=` kwarg against the closed radii
// scale and stores it. The pixel values live in the theme (full = the
// stadium/pill extreme); primitives never see numbers.
func applyRadius(n *ir.Node, prim string, v ast.Value) error {
	if v.Kind != ast.ValueIdent {
		return diag.New("lower", v.Pos.Line, v.Pos.Col,
			fmt.Sprintf("%s: radius= takes an identifier (one of %s)", prim, strings.Join(radiusTokens, ", ")))
	}
	for _, r := range radiusTokens {
		if v.String == r {
			n.Props["radius"] = v.String
			return nil
		}
	}
	d := diag.New("lower", v.Pos.Line, v.Pos.Col,
		fmt.Sprintf("%s: unknown radius %q (want one of %s)", prim, v.String, strings.Join(radiusTokens, ", ")))
	if hint := suggestFromSet(v.String, radiusTokens); hint != "" {
		d.Suggestion = "did you mean " + hint + "?"
	}
	return d
}

// applyAlignSelf validates an `align=` kwarg — the element's OWN
// alignment within its parent stack (align-self), not the alignment
// of its children. This is the chat-bubble spelling:
// `card tone=primary align=end maxwidth=480`.
func applyAlignSelf(n *ir.Node, prim string, v ast.Value) error {
	if v.Kind != ast.ValueIdent {
		return diag.New("lower", v.Pos.Line, v.Pos.Col,
			fmt.Sprintf("%s: align= takes an identifier (one of %s)", prim, strings.Join(alignSelfValues, ", ")))
	}
	for _, a := range alignSelfValues {
		if v.String == a {
			n.Props["align-self"] = v.String
			return nil
		}
	}
	d := diag.New("lower", v.Pos.Line, v.Pos.Col,
		fmt.Sprintf("%s: unknown align %q (want one of %s)", prim, v.String, strings.Join(alignSelfValues, ", ")))
	if hint := suggestFromSet(v.String, alignSelfValues); hint != "" {
		d.Suggestion = "did you mean " + hint + "?"
	}
	return d
}

// pairedTones is the tone vocabulary for surface treatments that mix
// from a tone's background color (aura=, shadow=) — the intent tones
// minus `default` (no tone) and `muted` (a single color, no bg pair).
var pairedTones = []string{"surface", "page", "primary", "accent", "danger", "success", "warning"}

// applySurfaceToneKwarg validates aura=/shadow= — a paired tone whose
// background the treatment mixes from.
func applySurfaceToneKwarg(n *ir.Node, prim, key string, v ast.Value) error {
	if v.Kind != ast.ValueIdent {
		return diag.New("lower", v.Pos.Line, v.Pos.Col,
			fmt.Sprintf("%s: %s= takes a tone identifier (one of %s)", prim, key, strings.Join(pairedTones, ", ")))
	}
	for _, t := range pairedTones {
		if v.String == t {
			n.Props[key] = v.String
			return nil
		}
	}
	d := diag.New("lower", v.Pos.Line, v.Pos.Col,
		fmt.Sprintf("%s: unknown %s tone %q (want one of %s)", prim, key, v.String, strings.Join(pairedTones, ", ")))
	if hint := suggestFromSet(v.String, pairedTones); hint != "" {
		d.Suggestion = "did you mean " + hint + "?"
	}
	return d
}

// applyMaxWidth validates a `maxwidth=` kwarg — an integer pixel cap
// on the element's width (same unit convention as width=N). The
// element still shrinks to fit below the cap.
func applyMaxWidth(n *ir.Node, prim string, v ast.Value) error {
	if v.Kind != ast.ValueInt || v.Int <= 0 {
		return diag.New("lower", v.Pos.Line, v.Pos.Col,
			fmt.Sprintf("%s: maxwidth= must be a positive integer (pixels)", prim))
	}
	n.Props["maxwidth"] = fmt.Sprintf("%dpx", v.Int)
	return nil
}

// resolveIconRef parses a qualified icon name (`Set.name`) and
// validates it against the project's declared icon sets. The
// compiler ships zero curated icons — every reference must point
// into an `icons <Name> = ...` declaration that the lowerer has
// already processed. Bare names (no `.`) are rejected with a hint
// that points at the new shape.
//
// kind is the caller-facing label ("icon", "button: icon=") used to
// prefix the error message so authors see which site reported the
// problem.
func (l *lowerer) resolveIconRef(raw string, pos ast.Pos, kind string) (string, string, error) {
	dot := -1
	for i, r := range raw {
		if r == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		d := diag.New("lower", pos.Line, pos.Col,
			fmt.Sprintf("%s: bare icon name %q — use the qualified form (e.g. Lucide.%s)",
				kind, raw, raw))
		d.Suggestion = "declare an `icons <Name> = web \"./path\"` set and reference it as `Name." + raw + "`"
		return "", "", d
	}
	setName, iconName := raw[:dot], raw[dot+1:]
	set, ok := l.knownIcons[setName]
	if !ok {
		d := diag.New("lower", pos.Line, pos.Col,
			fmt.Sprintf("%s: no icons set named %q in this project", kind, setName))
		if hint := suggestFromSet(setName, l.iconSetNames()); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return "", "", d
	}
	if !set[iconName] {
		d := diag.New("lower", pos.Line, pos.Col,
			fmt.Sprintf("%s: icon %q is not declared in set %q", kind, iconName, setName))
		if hint := suggestFromSet(iconName, mapKeys(set)); hint != "" {
			d.Suggestion = "did you mean " + setName + "." + hint + "?"
		}
		return "", "", d
	}
	return setName, iconName, nil
}

// iconSetNames lists every declared icon set; used in suggestion
// hints for the "no icons set named X" error.
func (l *lowerer) iconSetNames() []string {
	out := make([]string, 0, len(l.knownIcons))
	for k := range l.knownIcons {
		out = append(out, k)
	}
	return out
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// acceptToneAndSizeKwarg generalizes acceptToneKwarg: it accepts
// `tone=` plus an optional `size=` kwarg whose value must be in the
// closed `sizes` set. When sizes is nil, size= isn't allowed (same
// behavior as the tone-only form). Validated size lands in
// n.Props["size"]; renderers consult the theme to resolve the named
// scale (e.g. heading-lg → font-size + weight).
func acceptToneAndSizeKwarg(n *ir.Node, kwargs map[string]ast.Value, kindName string, sizes []string) error {
	known := []string{"tone"}
	if len(sizes) > 0 {
		known = append(known, "size")
	}
	for k, v := range kwargs {
		switch k {
		case "tone":
			if err := applyTone(n, v); err != nil {
				return err
			}
			continue
		case "size":
			if len(sizes) == 0 {
				// fall through to "unknown kwarg" below
				break
			}
			if v.Kind != ast.ValueIdent {
				return diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("%s: size= takes an identifier (one of %s)", kindName, strings.Join(sizes, ", ")))
			}
			ok := false
			for _, s := range sizes {
				if v.String == s {
					ok = true
					break
				}
			}
			if !ok {
				d := diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("%s: unknown size %q (want one of %s)", kindName, v.String, strings.Join(sizes, ", ")))
				if hint := suggestFromSet(v.String, sizes); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return d
			}
			n.Props["size"] = v.String
			continue
		}
		d := diag.New("lower", v.Pos.Line, v.Pos.Col,
			fmt.Sprintf("%s: unknown keyword arg %q", kindName, k))
		if hint := suggestFromSet(k, known); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return d
	}
	return nil
}

// Lower converts an AST root into an IR document. The root must be a `view`,
// or a `module` containing component decls and exactly one `view`.
//
// State decls (children of the view with Kind == "state") must appear
// before any non-state children. They populate a symbol table that the rest
// of the lowering consults to resolve cell references (e.g. `text count`)
// and the LHS of handler statements.
func Lower(a *ast.Node) (ir.Document, error) {
	l := &lowerer{
		cellsByName:     map[string]string{},
		cellInit:        map[string]any{},
		listCells:       map[string][]string{},
		listFields:      map[string][]fieldSpec{},
		idToName:        map[string]string{},
		components:      map[string]*componentDef{},
		compOrder:       nil,
		inlining:        map[string]bool{},
		flows:           map[string]*flowDef{},
		flowInlining:    map[string]bool{},
		knownTypes:      map[string]ir.TypeDecl{},
		knownApps:       map[string]ir.App{},
		knownOps:        map[string]opSig{},
		knownIcons:      map[string]map[string]bool{},
		knownBackends:   map[string]bool{},
		sessionCells:    map[string]map[string]string{},
		recordStates:    map[string]string{},
		unionStates:     map[string]string{},
		opCells:         map[string]opLifecycle{},
		themeTextTokens: map[string]bool{},
		diags:           &diag.Diagnostics{},
	}

	// Accept either a bare `view`, a `module` of decls (component | view |
	// app | test | ...), or a single top-level `app` / `test` decl. The
	// last case normalizes into a synthetic module so classifyModule can
	// route it uniformly. A file with no `view` is allowed when it carries
	// other declarations (e.g. a tests-only file declaring an external app
	// target); classifyModule diagnoses the empty case.
	switch a.Kind {
	case "app", "test", "story":
		a = &ast.Node{Kind: "module", Pos: a.Pos, Children: []*ast.Node{a}}
	}
	var view *ast.Node
	switch a.Kind {
	case "module":
		view = l.classifyModule(a)
	case "view":
		view = a
	default:
		l.diags.Add(diag.New("lower", a.Pos.Line, a.Pos.Col,
			fmt.Sprintf("top-level must be `view`, `app`, or `test`, got %q", a.Kind)))
		return ir.Document{}, l.diags.Err()
	}

	var name string
	if view != nil {
		if len(view.Args) >= 1 && view.Args[0].Kind == ast.ValueIdent {
			name = view.Args[0].String
		} else {
			l.diags.Add(diag.New("lower", view.Pos.Line, view.Pos.Col,
				"view needs a name identifier"))
		}
	}

	// Lower sessions first so backends can resolve `token from
	// Sess.cell` paths to concrete cell IDs. Session cells share
	// the global cell-id namespace so the runtime treats them
	// identically to view state.
	if len(l.sessionNodes) > 0 {
		l.loweredSessions = l.lowerSessions()
	}

	// Lower backends after sessions; backends may reference session
	// cells via their auth bindings, but they don't reference each
	// other.
	if len(l.backendNodes) > 0 {
		l.loweredBackends = l.lowerBackends()
		for _, b := range l.loweredBackends {
			l.knownBackends[b.Name] = true
		}
	}

	// Lower declared types up front so state decls below can resolve
	// `state x : Pokemon` against the known type set. Errors land in
	// l.diags; the lowered list is also kept for doc.Types below.
	if len(l.typeNodes) > 0 {
		l.loweredTypes = l.lowerTypes()
		for _, td := range l.loweredTypes {
			l.knownTypes[td.Name] = td
		}
	}

	// Lower queries / commands next — handler bodies below need to
	// validate `cell = OpName(args)` against the known op set, and
	// op signatures depend on types being known first.
	if len(l.opNodes) > 0 {
		l.loweredQueries, l.loweredCommands, l.loweredStreams = l.lowerOps()
		for _, q := range l.loweredQueries {
			l.knownOps[q.Name] = opSig{name: q.Name, inputs: q.Inputs, ret: q.Return, kind: "query"}
		}
		for _, c := range l.loweredCommands {
			l.knownOps[c.Name] = opSig{name: c.Name, inputs: c.Inputs, ret: c.Return, kind: "command"}
			// Commands are awaited inline, but a round-trip can still be
			// in flight (disable Submit) or fail (backend down) — same
			// three implicit cells as streams, wired by an inline wrapper
			// at the call site so post-call statements still sequence.
			l.declareOpLifecycleCells(c.Name)
		}
		for _, s := range l.loweredStreams {
			l.knownOps[s.Name] = opSig{name: s.Name, inputs: s.Inputs, ret: s.Return, kind: "stream", channels: s.Channels}
			l.declareOpLifecycleCells(s.Name)
		}
	}

	// Lower icon sets before the view body so `icon=Set.name` refs
	// can be validated against the declared registry. Each set's
	// folder is walked + every .svg is parsed and validated.
	if len(l.iconNodes) > 0 {
		l.loweredIconSets = l.lowerIconSets()
		for _, set := range l.loweredIconSets {
			names := make(map[string]bool, len(set.Icons))
			for name := range set.Icons {
				names[name] = true
			}
			l.knownIcons[set.Name] = names
		}
	}

	// Lower font sources. The parser guarantees shape (provider ident +
	// 1..n family strings); lowering closes the provider vocabulary.
	// Families union per provider across all decls — packages in a
	// monorepo may each declare `fonts google = …` and a shared family
	// must load once, not once per declaring package.
	fontSeen := map[string]map[string]bool{}
	fontFams := map[string][]string{}
	for _, fn := range l.fontNodes {
		provider := fn.Args[0].String
		if provider != "google" {
			d := diag.New("lower", fn.Args[0].Pos.Line, fn.Args[0].Pos.Col,
				fmt.Sprintf("unknown font provider %q", provider))
			d.Suggestion = "today only `google` is supported (fonts google = \"Family\" …)"
			l.diags.Add(d)
			continue
		}
		if fontSeen[provider] == nil {
			fontSeen[provider] = map[string]bool{}
		}
		for _, v := range fn.Args[1:] {
			if fontSeen[provider][v.String] {
				continue
			}
			fontSeen[provider][v.String] = true
			fontFams[provider] = append(fontFams[provider], v.String)
		}
	}
	if fams := fontFams["google"]; len(fams) > 0 {
		l.loweredFonts = append(l.loweredFonts, ir.FontSource{Provider: "google", Families: fams})
	}

	// Lower apps before tests so `scenario in <App>` references resolve.
	loweredApps := make([]ir.App, 0, len(l.appNodes))
	for _, an := range l.appNodes {
		app, err := l.lowerApp(an)
		if err != nil {
			l.diags.AddErr(err)
			continue
		}
		if _, dup := l.knownApps[app.Name]; dup {
			l.diags.Add(diag.New("lower", an.Pos.Line, an.Pos.Col,
				fmt.Sprintf("app %q already declared", app.Name)))
			continue
		}
		l.knownApps[app.Name] = app
		loweredApps = append(loweredApps, app)
	}

	var root ir.Node
	var mountNodes []*ast.Node
	if view != nil {
		// Sweep state decls off the top of the view body. Each error is
		// recorded but doesn't abort the sweep — the rest of the file still
		// gets checked, which is the whole point of the multi-error refactor.
		bodyStart := 0
		for i, child := range view.Children {
			if child.Kind == "__error__" {
				bodyStart = i + 1
				continue
			}
			if child.Kind != "state" {
				bodyStart = i
				break
			}
			if err := l.declareState(child); err != nil {
				l.diags.AddErr(err)
			}
			bodyStart = i + 1
		}
		for i := bodyStart; i < len(view.Children); i++ {
			if view.Children[i].Kind == "state" {
				l.diags.Add(diag.New("lower",
					view.Children[i].Pos.Line, view.Children[i].Pos.Col,
					"state declarations must come before other lines in a view"))
			}
		}

		// Filter out __error__ children (they've already been diagnosed during
		// parse) so the body-shape switch below sees only real nodes.
		// Also extract `on mount` handlers for the document's MountActions.
		body := make([]*ast.Node, 0, len(view.Children)-bodyStart)
		for _, c := range view.Children[bodyStart:] {
			if c.Kind == "__error__" || c.Kind == "state" {
				continue
			}
			if c.Kind == "on_mount" {
				mountNodes = append(mountNodes, c)
				continue
			}
			body = append(body, c)
		}

		switch len(body) {
		case 0:
			if l.diags.Empty() {
				l.diags.Add(diag.New("lower", view.Pos.Line, view.Pos.Col,
					fmt.Sprintf("view %q has no body", name)))
			}
		case 1:
			root = l.lowerNodeCollect(body[0], "/")
		default:
			root = ir.Node{
				Kind:  ir.KindStack,
				ID:    "/",
				Props: map[string]any{"axis": "vertical", "gap": 0},
			}
			for i, child := range body {
				root.Children = append(root.Children,
					l.lowerNodeCollect(child, joinPath("/", i)))
			}
		}
	}

	// Lower mount actions inside the view scope (mountNodes was collected
	// from `on mount { ... }` decls in the view body).
	var loweredMountActions []ir.Action
	if view != nil && len(mountNodes) > 0 {
		for _, mn := range mountNodes {
			for _, stmt := range mn.Children {
				a, err := l.lowerHandler(stmt)
				if err != nil {
					l.diags.AddErr(err)
					continue
				}
				loweredMountActions = append(loweredMountActions, a)
			}
		}
	}

	// Resolve the uniform sizing vocabulary against each container's
	// axis, now that the whole tree (including any synthetic root) is
	// assembled. A width=/height= sizing marker on the root itself is
	// meaningless — the root owns the viewport unconditionally — and
	// silently dropping it would hide a real authoring mistake, so
	// flag it before resolveSizingMarkers deletes it.
	if _, ok := root.Props["sizing-w"]; ok {
		l.diags.Add(diag.New("lower", view.Pos.Line, view.Pos.Col,
			"width= on the view root has no effect — the root always fills the viewport (set width on an interior element instead)"))
	}
	if _, ok := root.Props["sizing-h"]; ok {
		l.diags.Add(diag.New("lower", view.Pos.Line, view.Pos.Col,
			"height= on the view root has no effect — the root always fills the viewport (use height=screen/full for the viewport mode, or set height on an interior element)"))
	}
	resolveSizingMarkers(&root)

	doc := ir.Document{
		Name:         name,
		Root:         root,
		Cells:        l.cellInit,
		CellNames:    l.idToName,
		MountActions: loweredMountActions,
	}
	if len(loweredApps) > 0 {
		doc.Apps = loweredApps
	}
	if len(l.compOrder) > 0 {
		doc.Components = make([]ir.ComponentSig, 0, len(l.compOrder))
		for _, cname := range l.compOrder {
			def := l.components[cname]
			params := make([]string, 0, len(def.params))
			for _, p := range def.params {
				if p.variadic {
					params = append(params, "*"+p.name)
				} else {
					params = append(params, p.name)
				}
			}
			doc.Components = append(doc.Components, ir.ComponentSig{
				Name:   def.name,
				Params: params,
			})
		}
	}
	if len(l.themes) > 0 {
		doc.Themes = make([]any, 0, len(l.themes))
		for _, t := range l.themes {
			doc.Themes = append(doc.Themes, t)
		}
	}
	if len(l.testNodes) > 0 {
		doc.Tests = make([]ir.Test, 0, len(l.testNodes))
		for _, tn := range l.testNodes {
			t, err := l.lowerTest(tn, name)
			if err != nil {
				l.diags.AddErr(err)
				continue
			}
			doc.Tests = append(doc.Tests, t)
		}
	}
	if len(l.storyNodes) > 0 && l.diags.Empty() {
		// Stories re-lower the module's shared decls per story; running
		// them only when the module itself is clean keeps any error in a
		// shared decl from being reported once per story on top of the
		// main report.
		doc.Stories = l.lowerStories(a)
	}
	if len(l.loweredTypes) > 0 {
		doc.Types = l.loweredTypes
	}
	if len(l.loweredQueries) > 0 {
		doc.Queries = l.loweredQueries
	}
	if len(l.loweredCommands) > 0 {
		doc.Commands = l.loweredCommands
	}
	if len(l.loweredStreams) > 0 {
		doc.Streams = l.loweredStreams
	}
	if len(l.loweredIconSets) > 0 {
		doc.IconSets = l.loweredIconSets
	}
	if len(l.loweredBackends) > 0 {
		doc.Backends = l.loweredBackends
	}
	if len(l.loweredSessions) > 0 {
		doc.Sessions = l.loweredSessions
	}
	if len(l.loweredFonts) > 0 {
		doc.Fonts = l.loweredFonts
	}
	return doc, l.diags.Err()
}

// lowerSessions walks every `session` decl and produces ir.Session
// values. Each session-scoped state cell gets a global cell id
// (shared namespace with view state, indistinguishable to the
// runtime) plus a record in l.sessionCells so backends can look up
// `Sess.cell` paths during lowerBackends.
//
// Initial values come from the type's default (no `=` literal v1).
func (l *lowerer) lowerSessions() []ir.Session {
	out := make([]ir.Session, 0, len(l.sessionNodes))
	seen := map[string]bool{}
	for _, n := range l.sessionNodes {
		if len(n.Args) == 0 || n.Args[0].Kind != ast.ValueIdent {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				"session declaration missing name"))
			continue
		}
		name := n.Args[0].String
		if seen[name] {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				fmt.Sprintf("session %q already declared", name)))
			continue
		}
		seen[name] = true
		l.sessionCells[name] = map[string]string{}

		session := ir.Session{Name: name}
		for _, child := range n.Children {
			if child.Kind != "state" {
				l.diags.Add(diag.New("lower", child.Pos.Line, child.Pos.Col,
					"session body only allows `state` declarations"))
				continue
			}
			if len(child.Args) == 0 || child.Args[0].Kind != ast.ValueIdent {
				l.diags.Add(diag.New("lower", child.Pos.Line, child.Pos.Col,
					"session state missing name"))
				continue
			}
			cellName := child.Args[0].String
			if l.sessionCells[name][cellName] != "" {
				l.diags.Add(diag.New("lower", child.Pos.Line, child.Pos.Col,
					fmt.Sprintf("session %q has duplicate state %q", name, cellName)))
				continue
			}
			var cellType *ir.TypeRef
			if len(child.Args) >= 2 {
				known := l.knownTypeNames()
				tr, err := lowerTypeRef(child.Args[1], known)
				if err != nil {
					l.diags.AddErr(err)
					continue
				}
				cellType = &tr
			}
			l.cellCounter++
			id := fmt.Sprintf("c%d", l.cellCounter)
			qualified := name + "." + cellName
			l.cellsByName[qualified] = id
			l.idToName[id] = qualified
			l.sessionCells[name][cellName] = id

			var initial any
			if cellType != nil {
				initial = scalarDefault(*cellType)
			}
			l.cellInit[id] = initial

			sc := ir.SessionCell{ID: id, Name: cellName, Initial: initial}
			if cellType != nil {
				sc.Type = *cellType
			}
			session.Cells = append(session.Cells, sc)
		}
		out = append(out, session)
	}
	return out
}

// lowerBackends walks every `backend` decl and produces ir.Backend
// values. Validates: url present + non-empty; auth method is one of
// the closed v1 set {none, bearer, cookie}; bearer requires a
// `token from <Session>.<cell>` binding that names an existing
// session cell.
func (l *lowerer) lowerBackends() []ir.Backend {
	out := make([]ir.Backend, 0, len(l.backendNodes))
	seen := map[string]bool{}
	for _, n := range l.backendNodes {
		if len(n.Args) == 0 || n.Args[0].Kind != ast.ValueIdent {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				"backend declaration missing name"))
			continue
		}
		name := n.Args[0].String
		if seen[name] {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				fmt.Sprintf("backend %q already declared", name)))
			continue
		}
		seen[name] = true

		var url, authMethod, tokenRef string
		var urlSet bool
		for _, child := range n.Children {
			if child.Kind != "backend-binding" || len(child.Args) < 2 {
				continue
			}
			key := child.Args[0].String
			val := child.Args[1].String
			switch key {
			case "url":
				// `url same-origin` (ident, not string) lowers to an empty
				// URL prefix: the client builds `/query/<op>` etc., which the
				// browser resolves against the page's own origin. The empty
				// prefix is only reachable through the keyword — a literal
				// `url ""` still fails the missing-url check below.
				if child.Args[1].Kind == ast.ValueIdent && val == "same-origin" {
					url, urlSet = "", true
					continue
				}
				// Trim a trailing slash: the client builds `url + "/query/op"`,
				// so a trailing slash yields `//query/op`, which Go's ServeMux
				// 301-redirects to the cleaned path — and the browser turns the
				// redirected POST into a GET, so every op silently 404/405s.
				url = strings.TrimRight(val, "/")
				urlSet = url != ""
			case "auth":
				authMethod = val
			case "token":
				tokenRef = val
			}
		}
		if !urlSet {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				fmt.Sprintf("backend %q is missing `url` — give a base URL string, or `url same-origin` to call the page's own origin", name)))
			continue
		}
		if authMethod == "" {
			authMethod = "none"
		}
		switch authMethod {
		case "none", "cookie":
			if tokenRef != "" {
				l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
					fmt.Sprintf("backend %q: auth=%s does not take a token binding", name, authMethod)))
			}
			out = append(out, ir.Backend{Name: name, URL: url,
				Auth: ir.Auth{Method: authMethod}})
		case "bearer":
			if tokenRef == "" {
				l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
					fmt.Sprintf("backend %q: auth=bearer requires `token from <Session>.<cell>`", name)))
				continue
			}
			parts := strings.SplitN(tokenRef, ".", 2)
			if len(parts) != 2 {
				l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
					fmt.Sprintf("backend %q: token must be a `<Session>.<cell>` path, got %q", name, tokenRef)))
				continue
			}
			sessName, cellName := parts[0], parts[1]
			cellID, ok := l.sessionCells[sessName][cellName]
			if !ok {
				l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
					fmt.Sprintf("backend %q: token source %q does not resolve to a session cell", name, tokenRef)))
				continue
			}
			out = append(out, ir.Backend{Name: name, URL: url,
				Auth: ir.Auth{Method: "bearer", TokenCellID: cellID}})
		default:
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				fmt.Sprintf("backend %q: unknown auth method %q (v1: none / bearer / cookie)",
					name, authMethod)))
		}
	}
	return out
}

// lowerIconSets walks every collected `icons` decl, calls the
// icons.Load discovery + validator on each declared target folder,
// and builds a list of ir.IconSet. Diagnostics from validation (bad
// SVG, hardcoded fill, missing viewBox, …) accumulate in l.diags so
// every problem in every set surfaces in one compile pass.
//
// v1 supports only the `web` target. An unknown target keyword is
// rejected with a clear "target X not yet supported" message —
// adding `ios` or `terminal` is a renderer change, not a parser
// change. Duplicate set names or duplicate target keywords within
// one set are also rejected.
func (l *lowerer) lowerIconSets() []ir.IconSet {
	out := make([]ir.IconSet, 0, len(l.iconNodes))
	seenSets := map[string]bool{}
	for _, n := range l.iconNodes {
		if len(n.Args) == 0 || n.Args[0].Kind != ast.ValueIdent {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				"icons declaration missing name"))
			continue
		}
		name := n.Args[0].String
		if seenSets[name] {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				fmt.Sprintf("icons set %q already declared", name)))
			continue
		}
		seenSets[name] = true

		set := ir.IconSet{Name: name, Icons: map[string]ir.IconAsset{}}
		seenTargets := map[string]bool{}
		for _, target := range n.Children {
			if target.Kind != "icon-target" {
				continue
			}
			if len(target.Args) < 2 {
				l.diags.Add(diag.New("lower", target.Pos.Line, target.Pos.Col,
					"icons body line missing target or path"))
				continue
			}
			tname := target.Args[0].String
			tpath := target.Args[1].String
			if seenTargets[tname] {
				l.diags.Add(diag.New("lower", target.Pos.Line, target.Pos.Col,
					fmt.Sprintf("target %q declared twice in icons set %q", tname, name)))
				continue
			}
			seenTargets[tname] = true

			switch tname {
			case "web":
				assets, err := icons.Load(tpath)
				if err != nil {
					l.diags.Add(diag.New("lower", target.Pos.Line, target.Pos.Col, err.Error()))
					continue
				}
				for iconName, a := range assets {
					prev := set.Icons[iconName]
					prev.ViewBox = a.ViewBox
					prev.Web = a.Inner
					set.Icons[iconName] = prev
				}
			default:
				l.diags.Add(diag.New("lower", target.Pos.Line, target.Pos.Col,
					fmt.Sprintf("icons target %q not yet supported (v1: web)", tname)))
			}
		}
		out = append(out, set)
	}
	return out
}

// lowerer holds the cell symbol table + diagnostic collector threaded
// through the recursion.
type lowerer struct {
	cellsByName map[string]string // user-visible name -> cell id
	cellInit    map[string]any    // cell id -> initial value
	idToName    map[string]string // cell id -> user-visible name

	// groupGuardStack carries the unresolved guards of the enclosing
	// `group |> guard ...` blocks. Member routes resolve them against their
	// own minted `:param` cells (a group guard like `memberOf orgID` names a
	// param each route declares), so resolution is deferred to the route.
	groupGuardStack []rawGuard
	// groupPublicDepth counts enclosing `group |> public` blocks, so member
	// routes inherit public access for the default-deny check.
	groupPublicDepth int

	listCells       map[string][]string    // parent cell id -> ordered child cell ids
	listFields      map[string][]fieldSpec // parent cell id -> structured field schema (nil for scalar lists)
	cellCounter     int
	components      map[string]*componentDef     // user-defined components by name
	compOrder       []string                     // declaration order for IR Components
	inlining        map[string]bool              // recursion guard during component inlining
	flows           map[string]*flowDef          // user-defined scenario flows by name
	flowInlining    map[string]bool              // recursion guard during flow inlining
	themes          []*theme.Theme               // source-declared themes, in decl order
	testNodes       []*ast.Node                  // source-declared `test` decls, deferred until after the view is lowered
	storyNodes      []*ast.Node                  // source-declared `story` decls, lowered last as standalone sub-documents
	typeNodes       []*ast.Node                  // source-declared `type` decls, lowered up front so state decls can resolve them
	appNodes        []*ast.Node                  // source-declared `app` decls, lowered before tests so scenario refs resolve
	knownApps       map[string]ir.App            // name -> lowered App, populated before tests so `scenario in X` validates
	opNodes         []*ast.Node                  // source-declared `query` / `command` decls, lowered after types
	iconNodes       []*ast.Node                  // source-declared `icons` decls, lowered before view body so icon refs validate
	backendNodes    []*ast.Node                  // source-declared `backend` decls, lowered after sessions
	sessionNodes    []*ast.Node                  // source-declared `session` decls, lowered before backends + ops
	knownTypes      map[string]ir.TypeDecl       // name -> lowered TypeDecl, populated before state decls
	knownOps        map[string]opSig             // name -> op signature, populated before view body so handlers can validate calls
	knownIcons      map[string]map[string]bool   // iconSet name -> set of declared icon names, populated by lowerIconSets
	knownBackends   map[string]bool              // backend name -> declared (used by op→backend binding)
	sessionCells    map[string]map[string]string // session name -> cell name -> cell id; backends consult this for auth bindings
	recordStates    map[string]string            // state name -> declared record type name (for `state p : Pokemon` → spread targets)
	unionStates     map[string]string            // cell id -> declared union type name (for `state s : Result` → match + variant construction)
	opCells         map[string]opLifecycle       // stream op name -> implicit lifecycle cell ids (<Op>.pending / .failed / .error)
	loweredTypes    []ir.TypeDecl                // ordered, populated by the early lowerTypes call; doc.Types reads from here
	loweredQueries  []ir.Query                   // populated by the early lowerOps call; doc.Queries reads from here
	loweredCommands []ir.Command                 // populated by the early lowerOps call; doc.Commands reads from here
	loweredStreams  []ir.Stream                  // populated by the early lowerOps call; doc.Streams reads from here
	loweredIconSets []ir.IconSet                 // populated by lowerIconSets; doc.IconSets reads from here
	loweredBackends []ir.Backend                 // populated by lowerBackends; doc.Backends reads from here
	loweredSessions []ir.Session                 // populated by lowerSessions; doc.Sessions reads from here
	fontNodes       []*ast.Node                  // source-declared `fonts` decls, lowered before the view body
	loweredFonts    []ir.FontSource              // populated from fontNodes; doc.Fonts reads from here
	themeTextTokens map[string]bool              // text-scale tokens declared in theme blocks; extend the closed size= vocabulary
	diags           *diag.Diagnostics
}

// opLifecycle names the three implicit cells synthesized per
// declared stream op. They are ordinary reactive cells (same table,
// same flush machinery as view state) addressed by the dotted names
// `<Op>.pending` / `<Op>.failed` / `<Op>.error`, but read-only from
// source — only the runtime's stream wrapper writes them.
type opLifecycle struct {
	pendingID string // Bool: true while at least one call of this op holds a connection open
	failedID  string // Bool: true after the most recent call failed; reset when a new call starts
	errorID   string // String: the most recent failure's message; "" while pending / after success
}

// opSig is the lowering-time digest of one query / command. Kept
// here (rather than re-deriving from ir.Document) so handler-body
// validation has fast lookup by name.
type opSig struct {
	name     string
	inputs   []ir.TypeFieldSpec
	ret      ir.TypeRef
	kind     string   // "query" | "command" | "stream"
	channels []string // stream only: live channel names (empty = scalar String stream)
}

// fieldSpec describes one column of a structured list: name, semantic
// type (String/Bool/Int), and the default value used when .append skips
// this position. Lower-stage only — the runtime sees the resolved
// per-row field map, not the schema.
type fieldSpec struct {
	name   string
	ftype  string
	defVal any
}

// componentDef holds a parsed `component` declaration ready for inlining.
type componentDef struct {
	name   string
	params []param
	body   []*ast.Node
	pos    ast.Pos
}

type param struct {
	name     string
	variadic bool
}

// flowDef holds a parsed `flow` declaration ready for inlining into a
// scenario (the exogenous-mood analog of componentDef). A flow is a
// named, parameterized sequence of steps; invoking it by name inlines its
// body with positional args bound to params, reusing the same
// substitution machinery as components.
type flowDef struct {
	name   string
	params []param
	body   []*ast.Node
	pos    ast.Pos
}

// registerFlow validates a `flow` decl and records it for inlining. A
// flow name may not shadow a built-in step verb (it would silently
// intercept that verb at every call site). Variadic params are not
// supported in v0.
func (l *lowerer) registerFlow(c *ast.Node) {
	if len(c.Args) == 0 || c.Args[0].Kind != ast.ValueIdent {
		l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col, "flow declaration missing name"))
		return
	}
	name := c.Args[0].String
	for _, v := range testStepVerbs {
		if name == v {
			l.diags.Add(diag.New("lower", c.Args[0].Pos.Line, c.Args[0].Pos.Col,
				fmt.Sprintf("flow name %q shadows a built-in step verb", name)))
			return
		}
	}
	if _, dup := l.flows[name]; dup {
		l.diags.Add(diag.New("lower", c.Args[0].Pos.Line, c.Args[0].Pos.Col,
			fmt.Sprintf("flow %q already declared", name)))
		return
	}
	params := make([]param, 0, len(c.Args)-1)
	seen := map[string]bool{}
	for i := 1; i < len(c.Args); i++ {
		pv := c.Args[i]
		if pv.Variadic {
			l.diags.Add(diag.New("lower", pv.Pos.Line, pv.Pos.Col,
				fmt.Sprintf("flow %q: variadic params are not supported (v0)", name)))
			return
		}
		if seen[pv.String] {
			l.diags.Add(diag.New("lower", pv.Pos.Line, pv.Pos.Col,
				fmt.Sprintf("duplicate parameter %q in flow %q", pv.String, name)))
			return
		}
		seen[pv.String] = true
		params = append(params, param{name: pv.String})
	}
	body := make([]*ast.Node, 0, len(c.Children))
	for _, child := range c.Children {
		if child.Kind == "__error__" {
			continue
		}
		body = append(body, child)
	}
	if len(body) == 0 {
		l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
			fmt.Sprintf("flow %q has no body", name)))
		return
	}
	l.flows[name] = &flowDef{name: name, params: params, body: body, pos: c.Pos}
}

// expandFlowSteps walks a scenario/flow body and inlines any step whose
// head is a known flow, recursively (a flow may call flows). Non-flow
// steps pass through unchanged. The result is a flat list of concrete
// step nodes for lowerStep. Flow args are substituted into the body
// before recursion, so a nested flow call sees concrete literals.
func (l *lowerer) expandFlowSteps(nodes []*ast.Node) ([]*ast.Node, error) {
	out := make([]*ast.Node, 0, len(nodes))
	for _, n := range nodes {
		if n == nil || n.Kind == "__error__" {
			continue
		}
		def, ok := l.flows[n.Kind]
		if !ok {
			out = append(out, n)
			continue
		}
		if l.flowInlining[n.Kind] {
			return nil, diag.New("lower", n.Pos.Line, n.Pos.Col,
				fmt.Sprintf("recursive flow invocation: %q", def.name))
		}
		subs, err := l.flowSubs(n, def)
		if err != nil {
			return nil, err
		}
		l.flowInlining[n.Kind] = true
		substituted := substChildren(def.body, subs)
		expanded, err := l.expandFlowSteps(substituted)
		delete(l.flowInlining, n.Kind)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// flowSubs binds a flow call's positional args to its params. Flow args
// are string/number literals (scenarios have no cells); an ident or any
// other shape is an error.
func (l *lowerer) flowSubs(call *ast.Node, def *flowDef) (map[string]substValue, error) {
	if len(call.Kwargs) > 0 {
		return nil, diag.New("lower", call.Pos.Line, call.Pos.Col,
			fmt.Sprintf("flow %q does not accept keyword args", def.name))
	}
	if len(call.Handlers) > 0 {
		return nil, diag.New("lower", call.Pos.Line, call.Pos.Col,
			fmt.Sprintf("flow %q does not accept event handlers", def.name))
	}
	if len(call.Children) > 0 {
		return nil, diag.New("lower", call.Pos.Line, call.Pos.Col,
			fmt.Sprintf("flow %q does not take a body at the call site", def.name))
	}
	if len(call.Args) != len(def.params) {
		return nil, diag.New("lower", call.Pos.Line, call.Pos.Col,
			fmt.Sprintf("flow %q expects %d arg(s), got %d", def.name, len(def.params), len(call.Args)))
	}
	subs := map[string]substValue{}
	for i, p := range def.params {
		v := call.Args[i]
		switch v.Kind {
		case ast.ValueString, ast.ValueInt:
			lit := v
			subs[p.name] = substValue{literal: &lit}
		default:
			return nil, diag.New("lower", v.Pos.Line, v.Pos.Col,
				fmt.Sprintf("flow %q arg %q must be a string or number literal", def.name, p.name))
		}
	}
	return subs, nil
}

// lowerNodeCollect calls lowerNode and records any error into the
// collector, returning a best-effort IR node (empty on hard failure).
// Used wherever a sibling-walk would otherwise be aborted by a single
// child's failure.
func (l *lowerer) lowerNodeCollect(a *ast.Node, path string) ir.Node {
	if a.Kind == "__error__" {
		return ir.Node{Kind: ir.KindFragment, ID: path}
	}
	n, err := l.lowerNode(a, path)
	if err != nil {
		l.diags.AddErr(err)
		return ir.Node{Kind: ir.KindFragment, ID: path}
	}
	return n
}

// declareState processes a `state name (: Type)? (= expr)?` AST node,
// allocates one or more cell ids, evaluates the initial expression (or
// derives defaults from the type), and registers everything in the
// symbol table.
//
// Shape variants:
//
//	state count = 0                 // untyped scalar, init from literal
//	state count : Int = 0           // typed scalar, init from literal
//	state name : String             // typed scalar, default from type
//	state p : Pokemon               // typed record → inflates leaf cells
//	state items = []                // untyped list (scalar items)
//	state items = []                // structured list (with field_decl children)
//	   label : String
func (l *lowerer) declareState(s *ast.Node) error {
	if len(s.Args) < 1 || s.Args[0].Kind != ast.ValueIdent {
		return diag.New("lower", s.Pos.Line, s.Pos.Col, "state declaration missing name")
	}
	name := s.Args[0].String
	if _, dup := l.cellsByName[name]; dup {
		return diag.New("lower", s.Pos.Line, s.Pos.Col,
			fmt.Sprintf("state %q already declared", name))
	}
	// Ops own the dotted namespace under their name (stream lifecycle
	// cells live at `<Op>.pending` etc.), so a state sharing an op's
	// name would make references ambiguous. Reject the shadowing here,
	// where the author can rename one side.
	if sig, clash := l.knownOps[name]; clash {
		return diag.New("lower", s.Pos.Line, s.Pos.Col,
			fmt.Sprintf("state %q collides with %s %q — pick a different name", name, sig.kind, name))
	}

	// Optional type annotation (Args[1]).
	var stateType *ir.TypeRef
	if len(s.Args) >= 2 {
		known := l.knownTypeNames()
		tr, err := lowerTypeRef(s.Args[1], known)
		if err != nil {
			return err
		}
		stateType = &tr
	}

	// Typed declarations whose type is a declared record or sum:
	// inflate leaf cells (records) or a scalar tag cell (sums). The
	// initializer is rejected — there's no literal syntax for either
	// shape yet, and defaults come straight from the type.
	if stateType != nil {
		if td, ok := l.knownTypes[stateType.Name]; ok {
			switch td.Kind {
			case "record":
				if len(s.Children) > 0 {
					return diag.New("lower", s.Pos.Line, s.Pos.Col,
						fmt.Sprintf("state %q has record type %q — omit `=` to default-initialize",
							name, stateType.Name))
				}
				if err := l.inflateRecord(name, td, s.Pos); err != nil {
					return err
				}
				// Remember the record-type binding so a future
				// `cell = OpReturningRecord(args)` can spread the
				// response into the leaf cells we just created.
				l.recordStates[name] = stateType.Name
				return nil
			case "sum":
				if len(s.Children) > 0 {
					return diag.New("lower", s.Pos.Line, s.Pos.Col,
						fmt.Sprintf("state %q has sum type %q — omit `=` to default-initialize",
							name, stateType.Name))
				}
				l.cellCounter++
				id := fmt.Sprintf("c%d", l.cellCounter)
				l.cellsByName[name] = id
				l.idToName[id] = name
				// Every sum-typed cell is matchable, so record its type.
				// A payload-carrying union uses the tagged `{tag, value}`
				// runtime shape; a plain (all-unit) enum stays a string
				// holding the current variant name.
				l.unionStates[id] = stateType.Name
				if td.HasPayloads() {
					// Default-initialize to the first variant, which must
					// be a unit variant (there is no payload-literal syntax
					// to synthesize one for it).
					first := td.VariantSpecs[0]
					if first.Payload != nil {
						return diag.New("lower", s.Pos.Line, s.Pos.Col,
							fmt.Sprintf("state %q: union %q default-initializes to its first variant %q, which carries a payload — reorder so a unit variant comes first, or set it explicitly",
								name, stateType.Name, first.Name))
					}
					l.cellInit[id] = ir.UnionValue{Tag: first.Name}
					return nil
				}
				def := ""
				if len(td.Variants) > 0 {
					def = td.Variants[0]
				}
				l.cellInit[id] = def
				return nil
			}
		}
	}

	// Typed list state: `state items : List<T>` or `state items : List<T> = []`.
	// When T is a declared record, the row shape is derived from the record's
	// leaves (recursive) instead of inline field decls. Inline field decls
	// alongside a typed list are rejected — pick one shape. Only an empty
	// list literal is allowed as init (no record-literal syntax yet).
	if stateType != nil && stateType.Name == "List" {
		if len(s.Children) > 0 {
			expr := s.Children[0]
			if expr.Kind != "list_lit" {
				return diag.New("lower", s.Pos.Line, s.Pos.Col,
					fmt.Sprintf("state %q : List<…> initializer must be `[]`", name))
			}
			if len(expr.Children) > 0 {
				return diag.New("lower", s.Pos.Line, s.Pos.Col,
					fmt.Sprintf("state %q : List<…> must start empty and grow via .append()", name))
			}
			// Inline field decls forbidden when a type is supplied.
			for _, sib := range s.Children[1:] {
				if sib.Kind == "field_decl" {
					return diag.New("lower", sib.Pos.Line, sib.Pos.Col,
						fmt.Sprintf("state %q has type %q — drop the inline field decls",
							name, "List<"+stateType.GenericArgs[0].Name+">"))
				}
			}
		}
		var fields []fieldSpec
		inner := stateType.GenericArgs[0]
		switch {
		case primitiveTypes[inner.Name]:
			// Scalar list of a primitive — no field schema (same as
			// untyped `state items = []`).
		default:
			td, ok := l.knownTypes[inner.Name]
			if !ok {
				return diag.New("lower", s.Args[1].Pos.Line, s.Args[1].Pos.Col,
					fmt.Sprintf("unknown list element type %q", inner.Name))
			}
			if td.Kind != "record" {
				return diag.New("lower", s.Args[1].Pos.Line, s.Args[1].Pos.Col,
					fmt.Sprintf("list element type %q must be a record (got %s)", inner.Name, td.Kind))
			}
			var err error
			fields, err = l.flattenRecordFields("", td)
			if err != nil {
				return err
			}
		}
		l.cellCounter++
		parentID := fmt.Sprintf("c%d", l.cellCounter)
		l.cellInit[parentID] = []string{}
		l.cellsByName[name] = parentID
		l.idToName[parentID] = name
		l.listCells[parentID] = nil
		if len(fields) > 0 {
			l.listFields[parentID] = fields
		}
		return nil
	}

	// Typed primitive state with no initializer: default from the type.
	if stateType != nil && len(s.Children) == 0 {
		if primitiveTypes[stateType.Name] {
			l.cellCounter++
			id := fmt.Sprintf("c%d", l.cellCounter)
			l.cellsByName[name] = id
			l.idToName[id] = name
			l.cellInit[id] = scalarDefault(*stateType)
			return nil
		}
		return diag.New("lower", s.Args[1].Pos.Line, s.Args[1].Pos.Col,
			fmt.Sprintf("don't know how to default-initialize state of type %q", stateType.Name))
	}

	// All remaining shapes require an initializer expression.
	if len(s.Children) < 1 {
		return diag.New("lower", s.Pos.Line, s.Pos.Col,
			"state declaration needs an initial expression")
	}
	expr := s.Children[0]

	// List literal → list cell (parent cell id, each element gets its own
	// child cell). Same wire format the Go For uses. If the state has
	// additional children, they're field decls turning this into a
	// structured list — items are objects with named subfields rather
	// than scalars.
	if expr.Kind == "list_lit" {
		// Extract structured-row field shape from the state's siblings,
		// if any. A list state with field decls is structured.
		var fields []fieldSpec
		for _, sib := range s.Children[1:] {
			if sib.Kind == "__error__" {
				continue
			}
			if sib.Kind != "field_decl" {
				return diag.New("lower", sib.Pos.Line, sib.Pos.Col,
					fmt.Sprintf("unexpected %q under state — expected field declarations", sib.Kind))
			}
			f, err := parseFieldSpec(sib)
			if err != nil {
				return err
			}
			fields = append(fields, f)
		}
		// Structured lists must start empty in v0 — combining an initial
		// list literal with a field schema is ambiguous (what fields do
		// the literal items have?). Cleaner to require empty + grow via
		// append.
		if len(fields) > 0 && len(expr.Children) > 0 {
			return diag.New("lower", s.Pos.Line, s.Pos.Col,
				"structured list states must start `[]` and grow via .append()")
		}

		var childIDs []string
		for _, elem := range expr.Children {
			v, err := evalInit(elem)
			if err != nil {
				return err
			}
			l.cellCounter++
			childID := fmt.Sprintf("c%d", l.cellCounter)
			l.cellInit[childID] = v
			childIDs = append(childIDs, childID)
		}
		l.cellCounter++
		parentID := fmt.Sprintf("c%d", l.cellCounter)
		l.cellInit[parentID] = childIDs
		l.cellsByName[name] = parentID
		l.idToName[parentID] = name
		l.listCells[parentID] = childIDs
		if len(fields) > 0 {
			l.listFields[parentID] = fields
		}
		return nil
	}

	value, err := evalInit(expr)
	if err != nil {
		return err
	}
	l.cellCounter++
	id := fmt.Sprintf("c%d", l.cellCounter)
	l.idToName[id] = name
	l.cellsByName[name] = id
	l.cellInit[id] = value
	return nil
}

// knownTypeNames returns the user-declared type names as a bool set,
// the shape lowerTypeRef expects. Built lazily — small file sizes
// make caching not worth the staleness risk.
func (l *lowerer) knownTypeNames() map[string]bool {
	out := make(map[string]bool, len(l.knownTypes))
	for k := range l.knownTypes {
		out[k] = true
	}
	return out
}

// scalarDefault returns the zero value for a primitive type ref.
// Optional and generic markers are caller-handled; this function only
// looks at the head name.
func scalarDefault(t ir.TypeRef) any {
	switch t.Name {
	case "Int":
		return int64(0)
	case "String":
		return ""
	case "Bool":
		return false
	}
	return nil
}

// inflateRecord registers per-leaf scalar cells for a record-typed
// state. `prefix` is the dotted name path the leaves hang off of
// (e.g. "p" for `state p : Pokemon`, then "p.stats" when recursing).
// Sub-records recurse, sums become one scalar string cell holding the
// first declared variant, primitives become one scalar cell with the
// type's zero value. Nested lists are deferred — they need
// per-row inflation that the structured-list path handles separately.
func (l *lowerer) inflateRecord(prefix string, td ir.TypeDecl, pos ast.Pos) error {
	for _, f := range td.Fields {
		fname := prefix + "." + f.Name
		switch {
		case primitiveTypes[f.Type.Name]:
			l.cellCounter++
			id := fmt.Sprintf("c%d", l.cellCounter)
			l.cellsByName[fname] = id
			l.idToName[id] = fname
			l.cellInit[id] = scalarDefault(f.Type)
		case f.Type.Name == "List":
			return diag.New("lower", pos.Line, pos.Col,
				fmt.Sprintf("list field %q in record-typed state is not yet supported", fname))
		default:
			inner, ok := l.knownTypes[f.Type.Name]
			if !ok {
				return diag.New("lower", pos.Line, pos.Col,
					fmt.Sprintf("unknown type %q for field %q", f.Type.Name, fname))
			}
			switch inner.Kind {
			case "record":
				if err := l.inflateRecord(fname, inner, pos); err != nil {
					return err
				}
			case "sum":
				l.cellCounter++
				id := fmt.Sprintf("c%d", l.cellCounter)
				l.cellsByName[fname] = id
				l.idToName[id] = fname
				def := ""
				if len(inner.Variants) > 0 {
					def = inner.Variants[0]
				}
				l.cellInit[id] = def
			default:
				return diag.New("lower", pos.Line, pos.Col,
					fmt.Sprintf("can't inflate field %q with type %q (kind %q)", fname, f.Type.Name, inner.Kind))
			}
		}
	}
	return nil
}

// flattenRecordFields walks a record TypeDecl's fields recursively and
// produces a flat []fieldSpec keyed by the dotted leaf path. Used by
// the typed-list-state path to derive the row schema from a declared
// record T in `state items : List<T>`. Sums collapse to a single
// scalar string field defaulted to the first variant. Nested lists
// are rejected (no inflation strategy yet for List inside a row).
//
// `prefix` is the dotted path accumulated so far ("" at the top
// call, "stats" while recursing into a sub-record).
func (l *lowerer) flattenRecordFields(prefix string, td ir.TypeDecl) ([]fieldSpec, error) {
	var out []fieldSpec
	join := func(name string) string {
		if prefix == "" {
			return name
		}
		return prefix + "." + name
	}
	for _, f := range td.Fields {
		fname := join(f.Name)
		switch {
		case primitiveTypes[f.Type.Name]:
			out = append(out, fieldSpec{
				name:   fname,
				ftype:  f.Type.Name,
				defVal: scalarDefault(f.Type),
			})
		case f.Type.Name == "List":
			return nil, fmt.Errorf("list field %q nested inside a list element is not yet supported", fname)
		default:
			inner, ok := l.knownTypes[f.Type.Name]
			if !ok {
				return nil, fmt.Errorf("unknown type %q for field %q", f.Type.Name, fname)
			}
			switch inner.Kind {
			case "record":
				nested, err := l.flattenRecordFields(fname, inner)
				if err != nil {
					return nil, err
				}
				out = append(out, nested...)
			case "sum":
				def := ""
				if len(inner.Variants) > 0 {
					def = inner.Variants[0]
				}
				out = append(out, fieldSpec{
					name:   fname,
					ftype:  "String",
					defVal: def,
				})
			default:
				return nil, fmt.Errorf("can't flatten field %q with type %q (kind %q)", fname, f.Type.Name, inner.Kind)
			}
		}
	}
	return out, nil
}

// resolveAppendArg encodes one .append() argument into its wire form:
// literals embed directly, cell-refs become "$cell.<id>" sentinels that
// the runtime substitutes at dispatch. Shared between scalar and
// structured-list append paths so the resolution rules stay identical.
func resolveAppendArg(arg *ast.Node, l *lowerer) (any, error) {
	if arg.Kind == "ref" && len(arg.Args) == 1 {
		refName := arg.Args[0].String
		// Bool literals parse as bare-ident refs; they are values, not
		// cell names, so they must win before the cell lookup or
		// `.append(true, …)` fails with `unknown cell "true"`.
		if refName == "true" || refName == "false" {
			return evalInit(arg)
		}
		refID, _, ok := l.lookupCell(refName)
		if !ok {
			d := diag.New("lower", arg.Pos.Line, arg.Pos.Col,
				fmt.Sprintf("unknown cell %q in .append()", refName))
			if hint := l.suggestCellName(refName); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			return nil, d
		}
		return "$cell." + refID, nil
	}
	return evalInit(arg)
}

// parseFieldSpec converts a "field_decl" AST node (name + type + optional
// default expr) into a lowering-time fieldSpec with a defaulted value if
// the source didn't provide one. Type names are the small closed set the
// language ships today: String / Bool / Int.
func parseFieldSpec(n *ast.Node) (fieldSpec, error) {
	if len(n.Args) < 2 {
		return fieldSpec{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
			"field decl missing name or type")
	}
	name := n.Args[0].String
	ftype := n.Args[1].String
	var def any
	switch ftype {
	case "String":
		def = ""
	case "Bool":
		def = false
	case "Int":
		def = int64(0)
	default:
		d := diag.New("lower", n.Args[1].Pos.Line, n.Args[1].Pos.Col,
			fmt.Sprintf("unknown field type %q", ftype))
		if hint := suggestFromSet(ftype, []string{"String", "Bool", "Int"}); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return fieldSpec{}, d
	}
	if len(n.Children) > 0 {
		v, err := evalInit(n.Children[0])
		if err != nil {
			return fieldSpec{}, err
		}
		def = v
	}
	return fieldSpec{name: name, ftype: ftype, defVal: def}, nil
}

// evalInit constant-folds the initial expression of a state cell.
//
// Supported (S0+L3.1/L4): int/string literals, bool literals via the bare
// idents `true` / `false`. List literals are handled one level up in
// declareState because they spawn child cells in the registry, not a single
// value. Richer initializers (refs to other cells, computed expressions)
// would need ordering rules and are deferred.
func evalInit(e *ast.Node) (any, error) {
	if e.Kind == "lit" && len(e.Args) > 0 {
		v := e.Args[0]
		switch v.Kind {
		case ast.ValueInt:
			return v.Int, nil
		case ast.ValueString:
			return v.String, nil
		}
	}
	if e.Kind == "ref" && len(e.Args) > 0 {
		switch e.Args[0].String {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	return nil, diag.New("lower", e.Pos.Line, e.Pos.Col,
		"state initializer must be a literal (int, string, bool, or list of literals)")
}

// validKinds is the set of component names lowering recognizes. New kinds
// land here AND in the HTML renderer's writeNode switch.
var validKinds = map[string]ir.Kind{
	"card":      ir.KindCard,
	"stack":     ir.KindStack,
	"title":     ir.KindTitle,
	"text":      ir.KindText,
	"code":      ir.KindCode,
	"button":    ir.KindButton,
	"input":     ir.KindTextInput,
	"if":        ir.KindIf,
	"iframe":    ir.KindIFrame,
	"container": ir.KindContainer,
	"badge":     ir.KindBadge,
	"bar":       ir.KindBar,
	"icon":      ir.KindIcon,
	"divider":   ir.KindDivider,
	"pulse":     ir.KindPulse,
	"router":    ir.KindRouter,
	"route":     ir.KindRoute,
	"modal":     ir.KindModal,
	// "for" is handled by lowerFor (special: generates its own KindFor /
	// KindForItem tree from the symbol table).
}

// BuiltinKinds returns the component kind names lowering recognizes,
// sorted. Editor tooling (LSP semantic tokens, tree-sitter highlight
// sync tests) treats this list as the single source of truth so the
// builtin vocabulary can't drift between the compiler and editors.
func BuiltinKinds() []string {
	out := make([]string, 0, len(validKinds)+1)
	for k := range validKinds {
		out = append(out, k)
	}
	out = append(out, "for")
	sort.Strings(out)
	return out
}

var leafKinds = map[string]bool{
	"title":   true,
	"code":    true,
	"iframe":  true,
	"text":    true,
	"button":  true,
	"input":   true,
	"badge":   true,
	"bar":     true,
	"icon":    true,
	"divider": true,
	"pulse":   true,
}

func (l *lowerer) lowerNode(a *ast.Node, path string) (ir.Node, error) {
	// `for` is a special form: its body is re-lowered N+1 times (once per
	// existing list element + once with the $ITEM sentinel for the template
	// row that the runtime clones on append). lowerFor handles all of that.
	if a.Kind == "for" {
		return l.lowerFor(a, path)
	}
	// `match` is a special form like `for`: it dispatches over a union
	// cell's tag, lowering one arm subtree per variant with the arm's
	// `as` payload binding scoped to that subtree, and the exhaustiveness
	// check lives in lowerMatch.
	if a.Kind == "match" {
		return l.lowerMatch(a, path)
	}
	// `group` is a routing special form: a `group |> guard ... |> layout ...`
	// block whose pipe facets are inherited by every member route. It is not
	// a component kind (stays out of validKinds), so handle it here.
	if a.Kind == "group" {
		return l.lowerGroup(a, path), nil
	}
	// A `splice` at lower time has escaped a component body — it should
	// have been expanded during inlining. Surfacing it here means the
	// author wrote `*name` outside a `component` body.
	if a.Kind == "splice" {
		name := ""
		if len(a.Args) > 0 {
			name = a.Args[0].String
		}
		return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
			fmt.Sprintf("`*%s` splice is only valid inside a component body", name))
	}
	// User-defined component? Inline its body with args bound to params.
	if def, ok := l.components[a.Kind]; ok {
		return l.lowerUserComponent(a, def, path)
	}
	kind, ok := validKinds[a.Kind]
	if !ok {
		d := diag.New("lower", a.Pos.Line, a.Pos.Col,
			fmt.Sprintf("unknown component %q", a.Kind))
		if hint := l.suggestKindOrComponent(a.Kind); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.Node{}, d
	}
	n := ir.Node{
		Kind:  kind,
		ID:    path,
		Props: map[string]any{},
	}

	switch a.Kind {
	case "title":
		if len(a.Args) != 1 {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"title takes one argument (a string literal or cell reference)")
		}
		if err := acceptToneAndSizeKwarg(&n, a.Kwargs, "title", titleSizes); err != nil {
			return ir.Node{}, err
		}
		switch a.Args[0].Kind {
		case ast.ValueString:
			n.Props["text"] = a.Args[0].String
		case ast.ValueIdent:
			id, init, ok := l.lookupCell(a.Args[0].String)
			if !ok {
				d := diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
					fmt.Sprintf("unknown name %q", a.Args[0].String))
				if hint := l.suggestCellName(a.Args[0].String); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
			n.Props["text"] = fmt.Sprintf("%v", init)
			if n.Bindings == nil {
				n.Bindings = map[string]ir.BindingRef{}
			}
			n.Bindings["text"] = ir.BindingRef{CellID: id}
		default:
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"title takes a string literal or cell reference")
		}

	case "text":
		if len(a.Args) != 1 {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"text takes one argument (a string literal or a cell reference)")
		}
		if err := acceptToneAndSizeKwarg(&n, a.Kwargs, "text", l.textSizeVocab()); err != nil {
			return ir.Node{}, err
		}
		switch a.Args[0].Kind {
		case ast.ValueString:
			// Scan for `${ident}` interpolations. If none, static text.
			// If exactly one, produce a bound text node (with template if
			// there are surrounding literal characters). Multiple
			// interpolations defer to a future revision.
			if err := l.applyTextString(&n, a.Args[0].String, a.Args[0].Pos); err != nil {
				return ir.Node{}, err
			}
		case ast.ValueIdent:
			id, init, ok := l.lookupCell(a.Args[0].String)
			if !ok {
				d := diag.New("lower",
					a.Args[0].Pos.Line, a.Args[0].Pos.Col,
					fmt.Sprintf("unknown name %q", a.Args[0].String))
				if hint := l.suggestCellName(a.Args[0].String); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
			n.Props["text"] = fmt.Sprintf("%v", init)
			if n.Bindings == nil {
				n.Bindings = map[string]ir.BindingRef{}
			}
			n.Bindings["text"] = ir.BindingRef{CellID: id}
		default:
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"text takes a string or cell reference")
		}

	case "code":
		// A verbatim monospace code block. Its content is the raw indented
		// body the parser captured (or a single inline string) — it is NEVER
		// interpolated, so `${…}`, `{`, `}` survive untouched. Static only:
		// no cell binding, since a code listing has no reactive content.
		if len(a.Args) != 1 || a.Args[0].Kind != ast.ValueString {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"code takes a verbatim indented body (or a single string)")
		}
		if len(a.Kwargs) > 0 {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"code takes no keyword arguments")
		}
		n.Props["text"] = a.Args[0].String

	case "input":
		// `input <string-cell> placeholder="…"` — two-way binds a text input
		// to a string cell. Same shape ui.TextInput produces.
		if len(a.Args) != 1 || a.Args[0].Kind != ast.ValueIdent {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"input takes one cell reference (a string cell)")
		}
		cellName := a.Args[0].String
		if op, ro := l.implicitOpCellOwner(cellName); ro {
			return ir.Node{}, diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
				fmt.Sprintf("%q is read-only (%s %q lifecycle state) — an input would write it; bind it with `text` / `if` instead", cellName, l.knownOps[op].kind, op))
		}
		cellID, init, ok := l.lookupCell(cellName)
		if !ok {
			d := diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
				fmt.Sprintf("unknown name %q in input", cellName))
			if hint := l.suggestCellName(cellName); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			return ir.Node{}, d
		}
		initStr, isStr := init.(string)
		if !isStr {
			return ir.Node{}, diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
				fmt.Sprintf("input requires a string cell; %q is %T", cellName, init))
		}
		n.Props["value"] = initStr
		if n.Bindings == nil {
			n.Bindings = map[string]ir.BindingRef{}
		}
		n.Bindings["value"] = ir.BindingRef{CellID: cellID}
		n.Handlers = map[string]ir.Action{
			"input": {
				Kind:   "set",
				CellID: cellID,
				Args:   map[string]any{"value": "$event.value"},
			},
		}
		for k, v := range a.Kwargs {
			switch k {
			case "placeholder":
				if v.Kind != ast.ValueString {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"input placeholder= must be a string")
				}
				n.Props["placeholder"] = v.String
			case "tone":
				if err := applyTone(&n, v); err != nil {
					return ir.Node{}, err
				}
			case "radius":
				if err := applyRadius(&n, "input", v); err != nil {
					return ir.Node{}, err
				}
			case "type":
				// Closed set: `text` (default), `password` (masked), and
				// `email` (mobile keyboard + native shape hint). The HTML
				// `type` is otherwise unreachable, so a login form can't
				// mask its password field without it.
				if v.Kind != ast.ValueIdent && v.Kind != ast.ValueString {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"input type= must be one of text, password, email")
				}
				switch v.String {
				case "text", "password", "email":
					n.Props["type"] = v.String
				default:
					d := diag.New("lower", v.Pos.Line, v.Pos.Col,
						fmt.Sprintf("input: unknown type %q", v.String))
					if hint := suggestFromSet(v.String, []string{"text", "password", "email"}); hint != "" {
						d.Suggestion = "did you mean " + hint + "?"
					}
					return ir.Node{}, d
				}
			default:
				d := diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("input: unknown keyword arg %q", k))
				if hint := suggestFromSet(k, []string{"placeholder", "tone", "radius", "type"}); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
		}

	case "button":
		if len(a.Args) < 1 || a.Args[0].Kind != ast.ValueString {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"button takes a string label as its first argument")
		}
		if len(a.Args) > 1 {
			return ir.Node{}, diag.New("lower", a.Args[1].Pos.Line, a.Args[1].Pos.Col,
				"button takes one positional arg (the label)")
		}
		for k, v := range a.Kwargs {
			switch k {
			case "tone":
				if err := applyTone(&n, v); err != nil {
					return ir.Node{}, err
				}
			case "icon":
				if v.Kind != ast.ValueIdent {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"button: icon= takes a qualified icon name (e.g. Lucide.search)")
				}
				set, name, err := l.resolveIconRef(v.String, v.Pos, "button: icon=")
				if err != nil {
					return ir.Node{}, err
				}
				n.Props["icon-set"] = set
				n.Props["icon"] = name
			case "disabled":
				// `disabled=<boolCell>` — reactive: the button disables
				// whenever the cell is true. Pairs with the implicit
				// stream-lifecycle cells (`disabled=Chat.pending`) but any
				// bool cell works.
				if v.Kind != ast.ValueIdent {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"button: disabled= takes a bool cell reference")
				}
				cellID, init, ok := l.lookupCell(v.String)
				if !ok {
					d := diag.New("lower", v.Pos.Line, v.Pos.Col,
						fmt.Sprintf("unknown name %q in disabled=", v.String))
					if hint := l.suggestCellName(v.String); hint != "" {
						d.Suggestion = "did you mean " + hint + "?"
					}
					return ir.Node{}, d
				}
				initBool, isBool := init.(bool)
				if !isBool {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						fmt.Sprintf("disabled= requires a bool cell; %q is %T", v.String, init))
				}
				if n.Bindings == nil {
					n.Bindings = map[string]ir.BindingRef{}
				}
				n.Bindings["disabled"] = ir.BindingRef{CellID: cellID}
				n.Props["disabled-initial"] = initBool
			case "radius":
				if err := applyRadius(&n, "button", v); err != nil {
					return ir.Node{}, err
				}
			case "padding", "padx", "pady":
				if err := applySpaceKwarg(&n, "button", k, v); err != nil {
					return ir.Node{}, err
				}
			default:
				d := diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("button: unknown keyword arg %q", k))
				if hint := suggestFromSet(k, []string{"tone", "icon", "disabled", "radius", "padding", "padx", "pady"}); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
		}
		n.Props["label"] = a.Args[0].String
		if len(a.Handlers) > 0 {
			n.Handlers = map[string]ir.Action{}
			for event, stmt := range a.Handlers {
				action, err := l.lowerHandler(stmt)
				if err != nil {
					return ir.Node{}, err
				}
				n.Handlers[event] = action
			}
		}

	case "stack":
		n.Props["axis"] = "vertical"
		n.Props["gap"] = 0
		for _, arg := range a.Args {
			if arg.Kind != ast.ValueIdent {
				return ir.Node{}, diag.New("lower", arg.Pos.Line, arg.Pos.Col,
					fmt.Sprintf("stack: unexpected positional %v", arg.String))
			}
			switch arg.String {
			case "horizontal":
				n.Props["axis"] = "horizontal"
			case "vertical":
				n.Props["axis"] = "vertical"
			case "glass":
				n.Props["glass"] = true
			default:
				d := diag.New("lower", arg.Pos.Line, arg.Pos.Col,
					fmt.Sprintf("stack: unknown flag %q", arg.String))
				if hint := suggestFromSet(arg.String, []string{"horizontal", "vertical", "glass"}); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
		}
		stackKnown := []string{"gap", "tone", "height", "flex", "width", "scroll", "anchor", "padding", "padx", "pady", "columns", "border", "match", "when", "radius", "align", "maxwidth", "aura", "shadow"}
		for k, v := range a.Kwargs {
			switch k {
			case "gap":
				if v.Kind != ast.ValueInt {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"stack: gap= must be an integer")
				}
				n.Props["gap"] = int(v.Int)
			case "tone":
				if err := applyTone(&n, v); err != nil {
					return ir.Node{}, err
				}
			case "height":
				// Root viewport modes: `full` (root scrolls its interior)
				// and `screen` (exact viewport, clipped — interior scroll=y
				// regions own all scrolling). Plus the uniform sizing
				// vocabulary: `fill` grows along this dimension, `fit` hugs
				// content, an integer is exact pixels. fill/fit resolve
				// against the parent's axis in resolveSizingMarkers.
				switch {
				case v.Kind == ast.ValueIdent && (v.String == "full" || v.String == "screen"):
					n.Props["height"] = v.String
				case v.Kind == ast.ValueIdent && (v.String == "fit" || v.String == "fill"):
					n.Props["sizing-h"] = v.String
				case v.Kind == ast.ValueInt:
					n.Props["sizing-h"] = fmt.Sprintf("%dpx", v.Int)
				default:
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"stack: height= must be `full`, `screen`, `fit`, `fill`, or a pixel integer")
				}
			case "flex":
				// flex=N is the main-axis fill(N) spelling: the element
				// takes N shares of the parent's leftover main-axis space.
				if v.Kind != ast.ValueInt {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"stack: flex= must be an integer")
				}
				n.Props["flex"] = int(v.Int)
			case "width":
				switch {
				case v.Kind == ast.ValueIdent && (v.String == "fit" || v.String == "fill"):
					n.Props["sizing-w"] = v.String
				case v.Kind == ast.ValueInt:
					n.Props["width"] = fmt.Sprintf("%dpx", v.Int)
				default:
					d := diag.New("lower", v.Pos.Line, v.Pos.Col,
						"stack: width= must be `fit`, `fill`, or a pixel integer")
					if v.Kind == ast.ValueIdent {
						if hint := suggestFromSet(v.String, []string{"fit", "fill"}); hint != "" {
							d.Suggestion = "did you mean " + hint + "?"
						}
					}
					return ir.Node{}, d
				}
			case "scroll":
				if v.Kind != ast.ValueIdent || v.String != "y" {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"stack: scroll= must be `y`")
				}
				n.Props["scroll"] = "y"
			case "anchor":
				// `anchor=end` — follow-the-end scrolling for transcripts:
				// the container pins to its newest content as it grows and
				// releases the pin while the user is scrolled away from the
				// bottom. Requires scroll=y (checked after the kwarg loop,
				// since map order is arbitrary).
				if v.Kind != ast.ValueIdent || v.String != "end" {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"stack: anchor= must be `end`")
				}
				n.Props["anchor"] = "end"
			case "padding", "padx", "pady":
				if err := applySpaceKwarg(&n, "stack", k, v); err != nil {
					return ir.Node{}, err
				}
			case "columns":
				if v.Kind != ast.ValueString {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"stack: columns= must be a quoted string (e.g. \"2fr 1fr 80px\")")
				}
				n.Props["columns"] = v.String
			case "border":
				if v.Kind != ast.ValueIdent {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"stack: border= must be a tone identifier")
				}
				n.Props["border"] = v.String
			case "radius":
				if err := applyRadius(&n, "stack", v); err != nil {
					return ir.Node{}, err
				}
			case "align":
				if err := applyAlignSelf(&n, "stack", v); err != nil {
					return ir.Node{}, err
				}
			case "maxwidth":
				if err := applyMaxWidth(&n, "stack", v); err != nil {
					return ir.Node{}, err
				}
			case "aura", "shadow":
				if err := applySurfaceToneKwarg(&n, "stack", k, v); err != nil {
					return ir.Node{}, err
				}
			case "match":
				if v.Kind != ast.ValueIdent {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"stack: match= must be a cell name")
				}
				matchCellID, _, ok := l.lookupCell(v.String)
				if !ok {
					d := diag.New("lower", v.Pos.Line, v.Pos.Col,
						fmt.Sprintf("stack: unknown cell %q in match=", v.String))
					if hint := l.suggestCellName(v.String); hint != "" {
						d.Suggestion = "did you mean " + hint + "?"
					}
					return ir.Node{}, d
				}
				n.Props["match-cell"] = matchCellID
			case "when":
				if v.Kind != ast.ValueString {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"stack: when= must be a quoted string")
				}
				n.Props["match-value"] = v.String
			default:
				d := diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("stack: unknown keyword arg %q", k))
				if hint := suggestFromSet(k, stackKnown); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
		}
		if n.Props["anchor"] == "end" && n.Props["scroll"] != "y" {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"stack: anchor=end requires scroll=y (only a scrolling container has an end to follow)")
		}
		if len(a.Handlers) > 0 {
			n.Handlers = map[string]ir.Action{}
			for event, stmt := range a.Handlers {
				action, err := l.lowerHandler(stmt)
				if err != nil {
					return ir.Node{}, err
				}
				n.Handlers[event] = action
			}
		}

	case "card":
		for _, arg := range a.Args {
			if arg.Kind == ast.ValueIdent && arg.String == "glass" {
				n.Props["glass"] = true
				continue
			}
			d := diag.New("lower", arg.Pos.Line, arg.Pos.Col,
				fmt.Sprintf("card: unknown flag %v (card takes no positional args except `glass`)", arg.String))
			if arg.Kind == ast.ValueIdent {
				if hint := suggestFromSet(arg.String, []string{"glass"}); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
			}
			return ir.Node{}, d
		}
		for k, v := range a.Kwargs {
			switch k {
			case "tone":
				if err := applyTone(&n, v); err != nil {
					return ir.Node{}, err
				}
			case "elevation":
				if v.Kind != ast.ValueIdent {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						fmt.Sprintf("card: elevation= takes an identifier (one of %s)", strings.Join(cardElevations, ", ")))
				}
				ok := false
				for _, e := range cardElevations {
					if v.String == e {
						ok = true
						break
					}
				}
				if !ok {
					d := diag.New("lower", v.Pos.Line, v.Pos.Col,
						fmt.Sprintf("card: unknown elevation %q (want one of %s)", v.String, strings.Join(cardElevations, ", ")))
					if hint := suggestFromSet(v.String, cardElevations); hint != "" {
						d.Suggestion = "did you mean " + hint + "?"
					}
					return ir.Node{}, d
				}
				n.Props["elevation"] = v.String
			case "radius":
				if err := applyRadius(&n, "card", v); err != nil {
					return ir.Node{}, err
				}
			case "align":
				if err := applyAlignSelf(&n, "card", v); err != nil {
					return ir.Node{}, err
				}
			case "maxwidth":
				if err := applyMaxWidth(&n, "card", v); err != nil {
					return ir.Node{}, err
				}
			case "aura", "shadow":
				if err := applySurfaceToneKwarg(&n, "card", k, v); err != nil {
					return ir.Node{}, err
				}
			case "padding", "padx", "pady":
				if err := applySpaceKwarg(&n, "card", k, v); err != nil {
					return ir.Node{}, err
				}
			case "width":
				// Same uniform sizing vocabulary as stack: fit / fill /
				// pixel integer. fill/fit resolve against the parent's
				// axis in resolveSizingMarkers.
				switch {
				case v.Kind == ast.ValueIdent && (v.String == "fit" || v.String == "fill"):
					n.Props["sizing-w"] = v.String
				case v.Kind == ast.ValueInt:
					n.Props["width"] = fmt.Sprintf("%dpx", v.Int)
				default:
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"card: width= must be `fit`, `fill`, or a pixel integer")
				}
			case "height":
				switch {
				case v.Kind == ast.ValueIdent && (v.String == "fit" || v.String == "fill"):
					n.Props["sizing-h"] = v.String
				case v.Kind == ast.ValueInt:
					n.Props["sizing-h"] = fmt.Sprintf("%dpx", v.Int)
				default:
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"card: height= must be `fit`, `fill`, or a pixel integer")
				}
			case "flex":
				if v.Kind != ast.ValueInt {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"card: flex= must be an integer")
				}
				n.Props["flex"] = int(v.Int)
			default:
				d := diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("card: unknown keyword arg %q", k))
				if hint := suggestFromSet(k, []string{"tone", "elevation", "radius", "align", "maxwidth", "aura", "shadow", "padding", "padx", "pady", "width", "height", "flex"}); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
		}

	case "container":
		// `container width=<size>` constrains its max-width to a
		// theme-defined token, centered horizontally. No positional
		// args. Default is `full` (no constraint) so an unkwarg'd
		// container is just a transparent passthrough — handy when
		// the user wants a tone/elevation wrapper without changing
		// width.
		if len(a.Args) > 0 {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"container takes no positional args; use width=...")
		}
		for k, v := range a.Kwargs {
			if k == "width" {
				if v.Kind != ast.ValueIdent {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						fmt.Sprintf("container: width= takes an identifier (one of %s)", strings.Join(containerWidths, ", ")))
				}
				ok := false
				for _, w := range containerWidths {
					if v.String == w {
						ok = true
						break
					}
				}
				if !ok {
					d := diag.New("lower", v.Pos.Line, v.Pos.Col,
						fmt.Sprintf("container: unknown width %q (want one of %s)", v.String, strings.Join(containerWidths, ", ")))
					if hint := suggestFromSet(v.String, containerWidths); hint != "" {
						d.Suggestion = "did you mean " + hint + "?"
					}
					return ir.Node{}, d
				}
				n.Props["width"] = v.String
				continue
			}
			d := diag.New("lower", v.Pos.Line, v.Pos.Col,
				fmt.Sprintf("container: unknown keyword arg %q", k))
			if hint := suggestFromSet(k, []string{"width"}); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			return ir.Node{}, d
		}

	case "divider":
		// `divider` is a layout-only horizontal rule. Optional
		// tone= recolors the line — useful for a danger-toned
		// section break or a muted hairline between rows. Takes
		// no positional args; no content.
		if len(a.Args) > 0 {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"divider takes no positional args")
		}
		if err := acceptToneAndSizeKwarg(&n, a.Kwargs, "divider", nil); err != nil {
			return ir.Node{}, err
		}

	case "pulse":
		// `pulse` is the working/streaming affordance: three staggered
		// fading dots (static at reduced motion). Optional tone=
		// recolors them — `if Chat.pending → pulse tone=accent` is the
		// chat "Iris is thinking" moment. No args, no content.
		if len(a.Args) > 0 {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"pulse takes no positional args")
		}
		if err := acceptToneKwarg(&n, a.Kwargs, "pulse"); err != nil {
			return ir.Node{}, err
		}

	case "icon":
		// `icon Set.name tone=accent` — qualified icon reference. The
		// set name comes from an `icons <Name> = ...` decl elsewhere
		// in the project; the compiler ships zero curated icons.
		// Tones inherit from parent by default; explicit tone=
		// recolors the icon via the currentColor cascade.
		if len(a.Args) != 1 || a.Args[0].Kind != ast.ValueIdent {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"icon takes one argument: a qualified icon name (e.g. Lucide.search)")
		}
		set, name, err := l.resolveIconRef(a.Args[0].String, a.Args[0].Pos, "icon")
		if err != nil {
			return ir.Node{}, err
		}
		if err := acceptToneAndSizeKwarg(&n, a.Kwargs, "icon", nil); err != nil {
			return ir.Node{}, err
		}
		n.Props["icon-set"] = set
		n.Props["name"] = name

	case "badge":
		// `badge "label" tone=success size=caption` — small inline
		// tone-pill. Accepts a string literal or a cell reference
		// (same shape as `text`); for the cell case, a "text"
		// binding lands so the runtime keeps the label in sync.
		if len(a.Args) != 1 {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"badge takes one argument (a string literal or cell reference)")
		}
		if err := acceptToneAndSizeKwarg(&n, a.Kwargs, "badge", l.textSizeVocab()); err != nil {
			return ir.Node{}, err
		}
		switch a.Args[0].Kind {
		case ast.ValueString:
			n.Props["text"] = a.Args[0].String
		case ast.ValueIdent:
			id, init, ok := l.lookupCell(a.Args[0].String)
			if !ok {
				d := diag.New("lower",
					a.Args[0].Pos.Line, a.Args[0].Pos.Col,
					fmt.Sprintf("unknown name %q", a.Args[0].String))
				if hint := l.suggestCellName(a.Args[0].String); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
			n.Props["text"] = fmt.Sprintf("%v", init)
			if n.Bindings == nil {
				n.Bindings = map[string]ir.BindingRef{}
			}
			n.Bindings["text"] = ir.BindingRef{CellID: id}
		default:
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"badge takes a string or cell reference")
		}

	case "bar":
		// `bar value=<cell> max=<int> tone=<tone>` — horizontal
		// progress bar. The value cell drives a reactive "fill"
		// binding that codegen turns into a width update on the
		// inner element. Max defaults to 100 if omitted (common
		// case: percentage). Tone defaults to "accent".
		if len(a.Args) > 0 {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"bar takes no positional args; use value= and max=")
		}
		var valueCellID string
		maxVal := 100
		for k, v := range a.Kwargs {
			switch k {
			case "value":
				if v.Kind != ast.ValueIdent {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"bar: value= takes a cell reference")
				}
				id, _, ok := l.lookupCell(v.String)
				if !ok {
					d := diag.New("lower", v.Pos.Line, v.Pos.Col,
						fmt.Sprintf("unknown cell %q in bar value=", v.String))
					if hint := l.suggestCellName(v.String); hint != "" {
						d.Suggestion = "did you mean " + hint + "?"
					}
					return ir.Node{}, d
				}
				valueCellID = id
			case "max":
				if v.Kind != ast.ValueInt {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"bar: max= must be an integer literal")
				}
				if v.Int <= 0 {
					return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
						"bar: max= must be positive")
				}
				maxVal = int(v.Int)
			case "tone":
				if err := applyTone(&n, v); err != nil {
					return ir.Node{}, err
				}
			default:
				d := diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("bar: unknown keyword arg %q", k))
				if hint := suggestFromSet(k, []string{"value", "max", "tone"}); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
		}
		if valueCellID == "" {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"bar requires `value=<cell>`")
		}
		if _, hasTone := n.Props["tone"]; !hasTone {
			n.Props["tone"] = "accent"
		}
		n.Props["max"] = maxVal
		// Stash initial value for SSR — renderer uses it to compute
		// the bar's starting width without needing the cells map.
		// Runtime updates supersede this on subsequent cell writes.
		switch init := l.cellInit[valueCellID].(type) {
		case int:
			n.Props["initial"] = int64(init)
		case int64:
			n.Props["initial"] = init
		case float64:
			n.Props["initial"] = int64(init)
		}
		if n.Bindings == nil {
			n.Bindings = map[string]ir.BindingRef{}
		}
		n.Bindings["fill"] = ir.BindingRef{CellID: valueCellID}

	case "iframe":
		// `iframe src="https://..."` static, or `iframe src=current_url`
		// reactive (binds the iframe src to the cell — runtime updates
		// the DOM attribute when the cell changes). HTML-only primitive
		// in v0; SwiftUI/PDF targets would have to error or substitute.
		if len(a.Args) > 0 {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"iframe takes no positional args; use src=...")
		}
		srcKwarg, hasSrc := a.Kwargs["src"]
		if !hasSrc {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"iframe requires `src=` (string literal or cell reference)")
		}
		switch srcKwarg.Kind {
		case ast.ValueString:
			n.Props["src"] = srcKwarg.String
		case ast.ValueIdent:
			cellID, init, ok := l.lookupCell(srcKwarg.String)
			if !ok {
				d := diag.New("lower", srcKwarg.Pos.Line, srcKwarg.Pos.Col,
					fmt.Sprintf("unknown name %q in iframe src", srcKwarg.String))
				if hint := l.suggestCellName(srcKwarg.String); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
			n.Props["src"] = fmt.Sprintf("%v", init)
			if n.Bindings == nil {
				n.Bindings = map[string]ir.BindingRef{}
			}
			n.Bindings["src"] = ir.BindingRef{CellID: cellID}
		default:
			return ir.Node{}, diag.New("lower", srcKwarg.Pos.Line, srcKwarg.Pos.Col,
				"iframe src= must be a string or cell reference")
		}
		// Optional height/width in pixels. Default height chosen to fill
		// a typical inner pane; width defaults to fill the parent (no
		// explicit width — the renderer emits 100% when unset).
		if heightKwarg, ok := a.Kwargs["height"]; ok {
			if heightKwarg.Kind != ast.ValueInt {
				return ir.Node{}, diag.New("lower", heightKwarg.Pos.Line, heightKwarg.Pos.Col,
					"iframe height= must be an integer (pixels)")
			}
			n.Props["height"] = int(heightKwarg.Int)
		}
		if widthKwarg, ok := a.Kwargs["width"]; ok {
			if widthKwarg.Kind != ast.ValueInt {
				return ir.Node{}, diag.New("lower", widthKwarg.Pos.Line, widthKwarg.Pos.Col,
					"iframe width= must be an integer (pixels)")
			}
			n.Props["width"] = int(widthKwarg.Int)
		}
		// Reject unknown kwargs with a suggestion.
		for k, v := range a.Kwargs {
			if k != "src" && k != "height" && k != "width" {
				d := diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("iframe: unknown keyword arg %q", k))
				if hint := suggestFromSet(k, []string{"src", "height", "width"}); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
		}

	case "if":
		if len(a.Args) != 1 || a.Args[0].Kind != ast.ValueIdent {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"`if` takes one bool cell reference")
		}
		if len(a.Kwargs) > 0 {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"`if` does not accept keyword args")
		}
		cellName := a.Args[0].String
		cellID, init, ok := l.lookupCell(cellName)
		if !ok {
			d := diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
				fmt.Sprintf("unknown name %q in `if`", cellName))
			if hint := l.suggestCellName(cellName); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			return ir.Node{}, d
		}
		initBool, isBool := init.(bool)
		if !isBool {
			return ir.Node{}, diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
				fmt.Sprintf("`if` requires a bool cell; %q is %T", cellName, init))
		}
		if n.Bindings == nil {
			n.Bindings = map[string]ir.BindingRef{}
		}
		n.Bindings["visible"] = ir.BindingRef{CellID: cellID}
		n.Props["initial"] = initBool

	case "modal":
		if len(a.Args) != 1 || a.Args[0].Kind != ast.ValueIdent {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"`modal` takes one bool cell reference")
		}
		for k, v := range a.Kwargs {
			if k != "side" {
				d := diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("modal: unknown keyword arg %q", k))
				if hint := suggestFromSet(k, []string{"side"}); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
			// `side=bottom` is the mobile sheet (slides over the bottom
			// edge, full width); `side=left` is the drawer. Omitted =
			// the centered dialog.
			if v.Kind != ast.ValueIdent || (v.String != "bottom" && v.String != "left") {
				return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
					"modal: side= must be `bottom` (sheet) or `left` (drawer)")
			}
			n.Props["side"] = v.String
		}
		cellName := a.Args[0].String
		cellID, init, ok := l.lookupCell(cellName)
		if !ok {
			d := diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
				fmt.Sprintf("unknown name %q in `modal`", cellName))
			if hint := l.suggestCellName(cellName); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			return ir.Node{}, d
		}
		initBool, isBool := init.(bool)
		if !isBool {
			return ir.Node{}, diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
				fmt.Sprintf("`modal` requires a bool cell; %q is %T", cellName, init))
		}
		if n.Bindings == nil {
			n.Bindings = map[string]ir.BindingRef{}
		}
		n.Bindings["visible"] = ir.BindingRef{CellID: cellID}
		n.Props["initial"] = initBool

	case "router":
		// `router` switches which child route is mounted. Two modes:
		//
		//   path-mode (`router`, no args): the active route is the one
		//   whose `path "..."` facet matches location.pathname. Navigation
		//   is History-API based (pushState + popstate); there is no cell.
		//
		//   legacy cell-mode (`router <cellRef>`): the cell holds a string
		//   matching one of the child route names; switching the cell swaps
		//   pages. Retained until callers migrate to path-mode.
		switch {
		case len(a.Args) == 0:
			// path-mode: nothing to bind — each route carries its own path.
		case len(a.Args) == 1 && a.Args[0].Kind == ast.ValueIdent:
			cellName := a.Args[0].String
			cellID, init, ok := l.lookupCell(cellName)
			if !ok {
				d := diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
					fmt.Sprintf("unknown cell %q in `router`", cellName))
				if hint := l.suggestCellName(cellName); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
			if n.Bindings == nil {
				n.Bindings = map[string]ir.BindingRef{}
			}
			n.Bindings["active"] = ir.BindingRef{CellID: cellID}
			n.Props["initial"], _ = init.(string)
		default:
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"`router` takes either no args (path-driven) or one cell reference (legacy)")
		}

	case "route":
		// `route "name"` — one branch inside a router. The name is a
		// string literal: the route's stable id, and (in legacy cell-mode)
		// the value matched against the router cell. An optional `path
		// "..."` facet makes the route part of a path-driven router; the
		// route's remaining children are its view subtree.
		if len(a.Args) != 1 || a.Args[0].Kind != ast.ValueString {
			return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
				"`route` takes one string argument (the route name)")
		}
		n.Props["name"] = a.Args[0].String
		// Two passes so a guard's args (which may name a `:param`) resolve
		// after the params are minted, regardless of source order, and so
		// inherited (enclosing-group) guards resolve against this route's
		// own params. Pass 1 collects facets + mints params; pass 2 resolves
		// own + inherited guards.
		var viewKids []*ast.Node
		var paramSpecs []any
		var ownGuards []rawGuard
		isPublic := l.groupPublicDepth > 0
		for _, c := range a.Children {
			switch c.Kind {
			case "route-path":
				if len(c.Args) == 1 && c.Args[0].Kind == ast.ValueString {
					n.Props["path"] = c.Args[0].String
				}
			case "route-public":
				isPublic = true
				n.Props["public"] = true
			case "route-guard":
				if len(c.Args) < 1 || c.Args[0].Kind != ast.ValueIdent {
					return ir.Node{}, diag.New("lower", c.Pos.Line, c.Pos.Col,
						"`guard` needs an operation name")
				}
				ownGuards = append(ownGuards, rawGuard{op: c.Args[0].String, args: c.Args[1:]})
			case "route-layout":
				if len(c.Args) >= 1 {
					n.Props["layout"] = c.Args[0].String
				}
			case "route-params":
				// Each `:param` segment binds to a read-only cell seeded
				// from the matched URL segment at navigation time, so the
				// view references it like any other cell.
				for _, p := range c.Children {
					if p.Kind != "route-param" || len(p.Args) < 1 {
						continue
					}
					pname := p.Args[0].String
					ptype := "String"
					if len(p.Args) >= 2 {
						ptype = p.Args[1].String
					}
					l.cellCounter++
					id := fmt.Sprintf("c%d", l.cellCounter)
					l.cellsByName[pname] = id
					l.idToName[id] = pname
					l.cellInit[id] = ""
					paramSpecs = append(paramSpecs, map[string]any{
						"name": pname, "cell": id, "type": ptype,
					})
				}
			case "route-view":
				viewKids = append(viewKids, c.Children...)
			default:
				viewKids = append(viewKids, c)
			}
		}
		if len(paramSpecs) > 0 {
			n.Props["params"] = paramSpecs
		}
		// Pass 2: inherited group guards first (the outer boundary wraps),
		// then this route's own. `guard <op> [args]` runs on every
		// navigation; falsy/throw redirects. Args lower to `$cell.<id>` or
		// literals — the shape jsOpCallExpr consumes.
		var guards []any
		for _, rg := range l.groupGuardStack {
			spec, err := l.resolveGuard(rg.op, rg.args)
			if err != nil {
				return ir.Node{}, err
			}
			guards = append(guards, spec)
		}
		for _, rg := range ownGuards {
			spec, err := l.resolveGuard(rg.op, rg.args)
			if err != nil {
				return ir.Node{}, err
			}
			guards = append(guards, spec)
		}
		if len(guards) > 0 {
			n.Props["guards"] = guards
		}
		// Default-deny: a path-driven route must state its access posture —
		// either `public` or at least one `guard`. The compiler holds the
		// boundary so an agent can't silently expose a route by forgetting
		// to gate it. Legacy cell-routes (no path) are exempt.
		if pathPat, _ := n.Props["path"].(string); pathPat != "" {
			switch {
			case isPublic && len(guards) > 0:
				return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
					fmt.Sprintf("route %q is both `public` and `guard`ed — a route is one or the other", a.Args[0].String))
			case !isPublic && len(guards) == 0:
				return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
					fmt.Sprintf("route %q must declare `public` or a `guard` (default-deny: every path route states its access)", a.Args[0].String))
			}
		}
		for i, child := range viewKids {
			n.Children = append(n.Children, l.lowerNodeCollect(child, joinPath(path, i)))
		}
		// Auth-leak cross-check (B): a `public` route must not call an
		// auth-gated (`auth cookie`) op in its subtree — that would expose a
		// session operation behind an open route. Default-deny forces the
		// public/guard choice; this catches choosing `public` wrongly.
		if isPublic {
			refs := map[string]bool{}
			for i := range n.Children {
				collectOpRefs(n.Children[i], refs)
			}
			for op := range refs {
				if l.opIsAuthCookie(op) {
					return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
						fmt.Sprintf("route %q is `public` but calls auth-gated op %q (its backend uses `auth cookie`) — gate it with a `guard` instead of marking it public", a.Args[0].String, op))
				}
			}
		}
		return n, nil
	}

	if leafKinds[a.Kind] && len(a.Children) > 0 {
		return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
			fmt.Sprintf("%s cannot have children", a.Kind))
	}
	for i, child := range a.Children {
		// Walk via the collector so a single bad grandchild doesn't drop
		// every sibling on the floor — accumulate the error and continue.
		n.Children = append(n.Children, l.lowerNodeCollect(child, joinPath(path, i)))
	}
	return n, nil
}

// resolveSizingMarkers translates the uniform sizing vocabulary
// (width=/height= fit | fill | <px>, parked in sizing-w/sizing-h
// marker props by the kwarg parsers) into the axis-specific layout
// props the renderers consume. `fill` along the parent's main axis
// is flex growth; across it, align-self stretch. `fit` hugs content;
// pixels are exact. Done as a whole-tree pass after lowering because
// a child can't know its parent's axis while its own kwargs parse.
//
// Markers on the root itself are dropped: the root owns the viewport
// unconditionally, so its size is never the author's to set (the
// root viewport modes — height=full/screen — travel separately).
func resolveSizingMarkers(n *ir.Node) {
	delete(n.Props, "sizing-w")
	delete(n.Props, "sizing-h")
	resolveChildSizing(n, false)
}

// resolveChildSizing resolves the fill/fit/px markers on n's children
// against the flex axis they actually live in. `horizontal` is the
// axis of the nearest real flex container above. if / for / for_item
// nodes are layout-transparent (the `if` wrapper is emitted
// display:contents; for-rows live in their list's container axis), so
// they pass the inherited axis straight through instead of resetting
// it to their own axis-less default.
func resolveChildSizing(n *ir.Node, horizontal bool) {
	switch n.Kind {
	case ir.KindIf, ir.KindFor, ir.KindForItem:
		// transparent: keep the enclosing container's axis
	default:
		horizontal = false
		if a, _ := n.Props["axis"].(string); a == "horizontal" {
			horizontal = true
		}
	}
	for i := range n.Children {
		c := &n.Children[i]
		if sw, ok := c.Props["sizing-w"].(string); ok {
			delete(c.Props, "sizing-w")
			switch sw {
			case "fill":
				if horizontal {
					if _, has := c.Props["flex"]; !has {
						c.Props["flex"] = 1
					}
				} else if _, has := c.Props["align-self"]; !has {
					c.Props["align-self"] = "stretch"
				}
			case "fit":
				c.Props["width"] = "fit-content"
			}
		}
		if sh, ok := c.Props["sizing-h"].(string); ok {
			delete(c.Props, "sizing-h")
			switch sh {
			case "fill":
				if !horizontal {
					if _, has := c.Props["flex"]; !has {
						c.Props["flex"] = 1
					}
				} else if _, has := c.Props["align-self"]; !has {
					c.Props["align-self"] = "stretch"
				}
			case "fit":
				c.Props["fixed-height"] = "fit-content"
			default: // "<n>px"
				c.Props["fixed-height"] = sh
			}
		}
		resolveChildSizing(c, horizontal)
	}
}

// declareOpLifecycleCells synthesizes the implicit lifecycle cells
// for one declared stream op: `<Op>.pending` / `<Op>.failed` (Bool) and
// `<Op>.error` (String). Registered under their dotted names in the
// ordinary cell table — exactly like inflated record-state leaves — so
// `if Chat.pending`, `text Chat.error`, `disabled=Chat.pending` all
// resolve through the existing machinery with no new reference syntax.
// Writes are runtime-only; lowerHandler rejects source assignments.
func (l *lowerer) declareOpLifecycleCells(opName string) {
	mint := func(field string, init any) string {
		l.cellCounter++
		id := fmt.Sprintf("c%d", l.cellCounter)
		name := opName + "." + field
		l.cellsByName[name] = id
		l.idToName[id] = name
		l.cellInit[id] = init
		return id
	}
	l.opCells[opName] = opLifecycle{
		pendingID: mint("pending", false),
		failedID:  mint("failed", false),
		errorID:   mint("error", ""),
	}
}

// implicitOpCellOwner reports whether name addresses one of the
// implicit stream-lifecycle cells (`<Op>.pending` / `.failed` /
// `.error`), returning the owning op's name. Used to reject source
// writes to runtime-owned state.
func (l *lowerer) implicitOpCellOwner(name string) (string, bool) {
	idx := strings.LastIndex(name, ".")
	if idx <= 0 {
		return "", false
	}
	op, field := name[:idx], name[idx+1:]
	if field != "pending" && field != "failed" && field != "error" {
		return "", false
	}
	if _, ok := l.opCells[op]; !ok {
		return "", false
	}
	return op, true
}

// stampOpLifecycle attaches the op's lifecycle cell ids to a
// call_op_stream action so the emitter can wrap the call: pending
// true + failed/error reset before, error capture on failure, pending
// false when the last overlapping call settles.
func (l *lowerer) stampOpLifecycle(a *ir.Action, opName string) {
	lc, ok := l.opCells[opName]
	if !ok {
		return
	}
	a.Args["pending_cell"] = lc.pendingID
	a.Args["failed_cell"] = lc.failedID
	a.Args["error_cell"] = lc.errorID
}

// lookupCell resolves a cell name to (id, initial-value, ok).
func (l *lowerer) lookupCell(name string) (string, any, bool) {
	id, ok := l.cellsByName[name]
	if !ok {
		return "", nil, false
	}
	return id, l.cellInit[id], true
}

// rawGuard is a guard parsed but not yet resolved against cells — the form
// pushed onto groupGuardStack so a member route can resolve a group guard's
// args (which may name the route's own `:param`s) after minting them.
type rawGuard struct {
	op   string
	args []ast.Value
}

// lowerGroup lowers a `group |> guard ... |> layout ...` block. The pipe
// facets (parsed as the group's leading children) are pushed as inherited
// context so each member route adopts the group's guards (resolved against
// that route's own params) and satisfies default-deny. The group node stays
// in the IR (KindGroup) so codegen can flatten it back into the router's
// route set; `layout` is recorded for the (future) chrome wrapper.
func (l *lowerer) lowerGroup(a *ast.Node, path string) ir.Node {
	n := ir.Node{Kind: ir.KindGroup, ID: path, Props: map[string]any{}}
	var members []*ast.Node
	pushedGuards := 0
	pushedPublic := false
	for _, c := range a.Children {
		switch c.Kind {
		case "route-guard":
			if len(c.Args) < 1 || c.Args[0].Kind != ast.ValueIdent {
				l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
					"group `guard` needs an operation name"))
				continue
			}
			l.groupGuardStack = append(l.groupGuardStack, rawGuard{op: c.Args[0].String, args: c.Args[1:]})
			pushedGuards++
		case "route-public":
			l.groupPublicDepth++
			pushedPublic = true
		case "route-layout":
			if len(c.Args) >= 1 {
				n.Props["layout"] = c.Args[0].String
			}
		default:
			members = append(members, c)
		}
	}
	for i, m := range members {
		n.Children = append(n.Children, l.lowerNodeCollect(m, joinPath(path, i)))
	}
	l.groupGuardStack = l.groupGuardStack[:len(l.groupGuardStack)-pushedGuards]
	if pushedPublic {
		l.groupPublicDepth--
	}
	return n
}

// resolveGuard turns a guard op + its arg AST values into the {op, args}
// spec jsOpCallExpr consumes. Ident args resolve to `$cell.<id>` (incl.
// minted `:param` cells); string/int args become literals.
func (l *lowerer) resolveGuard(op string, args []ast.Value) (map[string]any, error) {
	gargs := []any{}
	for _, ga := range args {
		switch ga.Kind {
		case ast.ValueString:
			gargs = append(gargs, ga.String)
		case ast.ValueInt:
			gargs = append(gargs, ga.Int)
		case ast.ValueIdent:
			cid, _, ok := l.lookupCell(ga.String)
			if !ok {
				d := diag.New("lower", ga.Pos.Line, ga.Pos.Col,
					fmt.Sprintf("unknown name %q in guard argument", ga.String))
				if hint := l.suggestCellName(ga.String); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return nil, d
			}
			gargs = append(gargs, "$cell."+cid)
		}
	}
	return map[string]any{"op": op, "args": gargs}, nil
}

// collectOpRefs gathers the names of every operation invoked by a node
// subtree's event handlers, recursing into sequence actions and child
// nodes. Feeds the route auth-leak cross-check.
func collectOpRefs(n ir.Node, into map[string]bool) {
	for _, a := range n.Handlers {
		collectActionOps(a, into)
	}
	for i := range n.Children {
		collectOpRefs(n.Children[i], into)
	}
}

func collectActionOps(a ir.Action, into map[string]bool) {
	switch a.Kind {
	case "call_op", "call_op_spread", "call_op_list", "call_op_stream":
		if op, ok := a.Args["op"].(string); ok {
			into[op] = true
		}
	case "sequence":
		if inner, ok := a.Args["actions"].([]any); ok {
			for _, raw := range inner {
				if ia, ok := raw.(ir.Action); ok {
					collectActionOps(ia, into)
				}
			}
		}
	}
}

// opIsAuthCookie reports whether op routes through a backend whose auth
// method is `cookie` — i.e. a session-authenticated operation. Backends
// and ops are lowered before the view body, so this is resolvable while
// lowering a route.
func (l *lowerer) opIsAuthCookie(op string) bool {
	backend := ""
	for _, q := range l.loweredQueries {
		if q.Name == op {
			backend = q.Backend
		}
	}
	if backend == "" {
		for _, c := range l.loweredCommands {
			if c.Name == op {
				backend = c.Backend
			}
		}
	}
	if backend == "" {
		for _, s := range l.loweredStreams {
			if s.Name == op {
				backend = s.Backend
			}
		}
	}
	if backend == "" {
		return false
	}
	for _, b := range l.loweredBackends {
		if b.Name == backend {
			return b.Auth.Method == "cookie"
		}
	}
	return false
}

// lowerHandler turns one statement into a declarative ir.Action.
//
// Supported shapes:
//
//	cell = literal                       → set(cell, literal)
//	cell = cell + literal  (same cell)   → add(cell, +literal)
//	cell = cell - literal  (same cell)   → add(cell, -literal)
//	cell = !cell           (same cell)   → toggle(cell)
//	list.append(value)                   → append_item(list, value)
//	list.remove(cellRef)                 → remove_item(list, target=cellRef)
//
// Other expression shapes (cross-cell math, multi-binop, …) are deferred
// and error here so authors learn the boundary explicitly.
func (l *lowerer) lowerHandler(stmt *ast.Node) (ir.Action, error) {
	if stmt.Kind == "seq" {
		// Multi-statement handler: lower each child, wrap in a single
		// "sequence" action. Runtime applies inner actions in order.
		actions := make([]ir.Action, 0, len(stmt.Children))
		for _, c := range stmt.Children {
			a, err := l.lowerHandler(c)
			if err != nil {
				return ir.Action{}, err
			}
			actions = append(actions, a)
		}
		// Encode the sequence as an Action with Args["actions"] = []ir.Action.
		// The JSON wire shape is { "kind": "sequence", "args": { "actions": [...] } }.
		raw := make([]any, 0, len(actions))
		for _, a := range actions {
			raw = append(raw, a)
		}
		return ir.Action{Kind: "sequence", Args: map[string]any{"actions": raw}}, nil
	}
	if stmt.Kind == "method_call" {
		return l.lowerMethodCall(stmt)
	}
	if stmt.Kind == "navigate" {
		path := ""
		if len(stmt.Args) > 0 {
			path = stmt.Args[0].String
		}
		if path == "" {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				"navigate needs a path, e.g. `navigate \"/\"`")
		}
		return ir.Action{Kind: "navigate", Args: map[string]any{"path": path}}, nil
	}
	if stmt.Kind == "op_call_stmt" {
		// Fire-and-forget: result is discarded. Target cell empty.
		return l.lowerOpCall(stmt, "")
	}
	if stmt.Kind == "stream_assign" {
		return l.lowerStreamAssign(stmt)
	}
	if stmt.Kind != "assign" {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			"handler must be an assignment or method call")
	}
	if len(stmt.Args) == 0 || stmt.Args[0].Kind != ast.ValueIdent {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			"handler missing target cell")
	}
	lhsName := stmt.Args[0].String

	// The implicit stream-lifecycle cells are runtime-owned: the stream
	// wrapper sets pending/failed/error around each call. A source write
	// would fight the runtime, so it's rejected outright.
	if op, ro := l.implicitOpCellOwner(lhsName); ro {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("%q is read-only — the runtime maintains it while %s %q is in flight / on failure", lhsName, l.knownOps[op].kind, op))
	}

	// Record-state spread: `cell = OpReturningRecord(args)` where `cell`
	// is a record-typed state (inflated to per-leaf cells, not present
	// in cellsByName under the bare name). Intercept before the normal
	// lookupCell path so the "unknown cell" diagnostic doesn't fire for
	// what's actually a perfectly valid record-spread assignment.
	if recordTypeName, isRecord := l.recordStates[lhsName]; isRecord {
		return l.lowerRecordSpread(stmt, lhsName, recordTypeName)
	}

	cellID, _, ok := l.lookupCell(lhsName)
	if !ok {
		d := diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("unknown cell %q in handler", lhsName))
		if hint := l.suggestCellName(lhsName); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.Action{}, d
	}
	if len(stmt.Children) != 1 {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			"handler assignment missing right-hand side")
	}
	rhs := stmt.Children[0]

	// Discriminated-union construction: when the LHS is a union cell,
	// `cell = variant` (unit) and `cell = variant(payload)` build a
	// tagged value. A real op call returning the union, or a cross-cell
	// assignment from another union cell, fall through to the normal
	// handling below.
	if unionType, isUnion := l.unionStates[cellID]; isUnion {
		if act, handled, err := l.lowerUnionConstruct(rhs, cellID, unionType); err != nil {
			return ir.Action{}, err
		} else if handled {
			return act, nil
		}
	}

	// Pattern: cell = OpName(args…) — op call, result lands in cell.
	// Reject list / structured-list targets (need leaf-spread semantics
	// that aren't designed yet) but allow scalar primitive cells.
	if rhs.Kind == "op_call" {
		if _, isList := l.listCells[cellID]; isList {
			return l.lowerListPopulate(stmt, rhs, lhsName, cellID)
		}
		return l.lowerOpCall(rhs, cellID)
	}

	// Pattern A: cell = literal → set. Both pure literals (int/string) and
	// the bool idents true/false are recognized here.
	if rhs.Kind == "lit" && len(rhs.Args) == 1 {
		return ir.Action{
			Kind:   "set",
			CellID: cellID,
			Args:   map[string]any{"value": astLiteralToAny(rhs.Args[0])},
		}, nil
	}
	if rhs.Kind == "ref" && len(rhs.Args) == 1 {
		switch rhs.Args[0].String {
		case "true", "false":
			return ir.Action{
				Kind:   "set",
				CellID: cellID,
				Args:   map[string]any{"value": rhs.Args[0].String == "true"},
			}, nil
		default:
			// cell = otherCell (cross-cell assignment). The value is a
			// $cell.<id> sentinel so the codegen reads the live value.
			rhsID, _, rhsOK := l.lookupCell(rhs.Args[0].String)
			if rhsOK {
				return ir.Action{
					Kind:   "set",
					CellID: cellID,
					Args:   map[string]any{"value": "$cell." + rhsID},
				}, nil
			}
		}
	}

	// Pattern: cell = !cell (same cell) → toggle.
	if rhs.Kind == "not" && len(rhs.Children) == 1 {
		inner := rhs.Children[0]
		if inner.Kind == "ref" && len(inner.Args) == 1 && inner.Args[0].String == lhsName {
			return ir.Action{Kind: "toggle", CellID: cellID}, nil
		}
	}

	// Pattern B: cell = cell ± literal (same cell on LHS as on left of binop).
	if strings.HasPrefix(rhs.Kind, "binop:") && len(rhs.Children) == 2 {
		op := strings.TrimPrefix(rhs.Kind, "binop:")
		left := rhs.Children[0]
		right := rhs.Children[1]
		if left.Kind == "ref" && len(left.Args) == 1 &&
			left.Args[0].String == lhsName &&
			right.Kind == "lit" && len(right.Args) == 1 &&
			right.Args[0].Kind == ast.ValueInt {
			delta := right.Args[0].Int
			if op == "-" {
				delta = -delta
			}
			return ir.Action{
				Kind:   "add",
				CellID: cellID,
				Args:   map[string]any{"delta": delta},
			}, nil
		}
	}

	return ir.Action{}, diag.New("lower", rhs.Pos.Line, rhs.Pos.Col,
		"S0 only supports `cell = literal` or `cell = cell ± literal` (same cell)")
}

// applyTextString configures a text node from a string literal that may
// contain `${ident}` interpolations.
//
// Cases:
//   - no `${...}` markers: static text.
//   - one `${ident}` and no surrounding literal characters (e.g. `"${count}"`):
//     plain cell binding, no template.
//   - one `${ident}` mixed with literal text (e.g. `"count: ${count}"`):
//     cell binding plus a template string with "${0}" as the value
//     placeholder.
//   - two or more `${...}`: error — multi-cell text bindings need wire-
//     format work that we haven't done yet.
func (l *lowerer) applyTextString(n *ir.Node, raw string, pos ast.Pos) error {
	parts, err := parseInterp(raw, pos)
	if err != nil {
		return err
	}
	// Count interpolations and locate the (single) ref.
	refIdx := -1
	refCount := 0
	for i, p := range parts {
		if p.isRef {
			refCount++
			if refIdx == -1 {
				refIdx = i
			}
		}
	}
	if refCount == 0 {
		n.Props["text"] = raw
		return nil
	}
	if refCount > 1 {
		return diag.New("lower", pos.Line, pos.Col,
			"text interpolation supports a single ${...} per string (multi-cell deferred)")
	}

	refName := parts[refIdx].text
	cellID, init, ok := l.lookupCell(refName)
	if !ok {
		d := diag.New("lower", pos.Line, pos.Col,
			fmt.Sprintf("unknown name %q in interpolation", refName))
		if hint := l.suggestCellName(refName); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return d
	}

	// Build the template with "${0}" in the ref's slot.
	var tmpl strings.Builder
	for i, p := range parts {
		if i == refIdx {
			tmpl.WriteString("${0}")
		} else {
			tmpl.WriteString(p.text)
		}
	}
	tmplStr := tmpl.String()

	// Initial text: render the template with the cell's initial value, so
	// SSR / first paint matches what the runtime would compute.
	n.Props["text"] = strings.ReplaceAll(tmplStr, "${0}", fmt.Sprintf("%v", init))

	// When the template is just the placeholder, drop the template — the
	// runtime's plain-bind path is identical and slightly cheaper.
	if tmplStr == "${0}" {
		tmplStr = ""
	}
	if n.Bindings == nil {
		n.Bindings = map[string]ir.BindingRef{}
	}
	n.Bindings["text"] = ir.BindingRef{CellID: cellID, Template: tmplStr}
	return nil
}

// interpPart is one chunk of a parsed interpolated string: either a literal
// run of text, or a reference to a cell name.
type interpPart struct {
	isRef bool
	text  string
}

// parseInterp splits a raw string at `${ident}` markers. The ident inside
// `${...}` is captured as a ref part; everything else is a literal part.
// Unterminated `${` is an error.
func parseInterp(s string, pos ast.Pos) ([]interpPart, error) {
	var parts []interpPart
	var buf strings.Builder
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			if buf.Len() > 0 {
				parts = append(parts, interpPart{text: buf.String()})
				buf.Reset()
			}
			j := i + 2
			for j < len(s) && s[j] != '}' {
				j++
			}
			if j >= len(s) {
				return nil, diag.New("lower", pos.Line, pos.Col,
					"unterminated `${` in string")
			}
			ident := s[i+2 : j]
			if ident == "" {
				return nil, diag.New("lower", pos.Line, pos.Col,
					"empty `${}` in string")
			}
			parts = append(parts, interpPart{isRef: true, text: ident})
			i = j + 1
			continue
		}
		buf.WriteByte(s[i])
		i++
	}
	if buf.Len() > 0 {
		parts = append(parts, interpPart{text: buf.String()})
	}
	return parts, nil
}

// lowerMatch handles `match <cell>` blocks over a discriminated-union
// cell. It validates the cell is union-typed, lowers one arm subtree
// per `| variant [as bind]` line (with the payload binding scoped to
// that arm via real binding cells the arm subtree reads through the
// normal flush machinery), and enforces strict exhaustiveness: every
// variant must have exactly one arm, with no unknown or duplicate
// arms and no wildcard.
func (l *lowerer) lowerMatch(a *ast.Node, path string) (ir.Node, error) {
	if len(a.Args) != 1 || a.Args[0].Kind != ast.ValueIdent {
		return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
			"match needs the form `match <cell>`")
	}
	cellName := a.Args[0].String
	cellID, _, ok := l.lookupCell(cellName)
	if !ok {
		d := diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
			fmt.Sprintf("unknown name %q in `match`", cellName))
		if hint := l.suggestCellName(cellName); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.Node{}, d
	}
	typeName, isUnion := l.unionStates[cellID]
	if !isUnion {
		return ir.Node{}, diag.New("lower", a.Args[0].Pos.Line, a.Args[0].Pos.Col,
			fmt.Sprintf("`match` requires a discriminated-union cell; %q is not one (declare it as `state %s : <UnionType>`)", cellName, cellName))
	}
	td := l.knownTypes[typeName]

	// Index the union's variants for arm validation.
	specByName := map[string]ir.VariantSpec{}
	for _, vs := range td.VariantSpecs {
		specByName[vs.Name] = vs
	}

	out := ir.Node{
		Kind: ir.KindMatch,
		ID:   path,
		// tagged=true → the cell holds `{tag, value}` (a payload-carrying
		// union); false → a plain string enum, compared by equality.
		Props: map[string]any{"cell": cellID, "union": typeName, "tagged": td.HasPayloads()},
	}
	covered := map[string]bool{}
	for i, arm := range a.Children {
		if arm == nil || arm.Kind == "__error__" {
			continue
		}
		if arm.Kind != "match_arm" {
			return ir.Node{}, diag.New("lower", arm.Pos.Line, arm.Pos.Col,
				"match body lines must be `| variant [as name]` arms")
		}
		variant := arm.Args[0].String
		spec, known := specByName[variant]
		if !known {
			d := diag.New("lower", arm.Pos.Line, arm.Pos.Col,
				fmt.Sprintf("`%s` is not a variant of union %q", variant, typeName))
			if hint := suggestFromSet(variant, td.Variants); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			return ir.Node{}, d
		}
		if covered[variant] {
			return ir.Node{}, diag.New("lower", arm.Pos.Line, arm.Pos.Col,
				fmt.Sprintf("duplicate match arm for variant %q", variant))
		}
		covered[variant] = true

		bindName := ""
		if len(arm.Args) >= 2 {
			bindName = arm.Args[1].String
		}
		if bindName != "" && spec.Payload == nil {
			return ir.Node{}, diag.New("lower", arm.Args[1].Pos.Line, arm.Args[1].Pos.Col,
				fmt.Sprintf("variant %q is a unit variant and carries no payload to bind with `as`", variant))
		}

		armNode := ir.Node{
			Kind:  ir.KindMatchArm,
			ID:    fmt.Sprintf("%s/arm-%d", path, i),
			Props: map[string]any{"variant": variant},
		}

		// Register the payload binding as real cell(s) scoped to this
		// arm, lower the body against them, then restore the symbol
		// table. Primitive payloads bind one scalar cell; record
		// payloads inflate one leaf cell per field (dotted access).
		type savedEntry struct {
			key string
			id  string
			had bool
		}
		var saved []savedEntry
		save := func(key string) {
			v, ok := l.cellsByName[key]
			saved = append(saved, savedEntry{key, v, ok})
		}

		if bindName != "" {
			armNode.Props["bind"] = bindName
			pt := *spec.Payload
			switch {
			case primitiveTypes[pt.Name]:
				l.cellCounter++
				bid := fmt.Sprintf("c%d", l.cellCounter)
				l.cellInit[bid] = scalarDefault(pt)
				save(bindName)
				l.cellsByName[bindName] = bid
				armNode.Props["bindCell"] = bid
				armNode.Props["bindLeaves"] = []any{} // primitive: value goes straight into bindCell
			default:
				rtd, isRec := l.knownTypes[pt.Name]
				if !isRec || rtd.Kind != "record" {
					return ir.Node{}, diag.New("lower", arm.Args[1].Pos.Line, arm.Args[1].Pos.Col,
						fmt.Sprintf("payload type %q of variant %q must be a primitive or a record to bind", pt.Name, variant))
				}
				leaves, err := l.flattenRecordFields("", rtd)
				if err != nil {
					return ir.Node{}, err
				}
				var leafRefs []any
				for _, lf := range leaves {
					l.cellCounter++
					lid := fmt.Sprintf("c%d", l.cellCounter)
					l.cellInit[lid] = lf.defVal
					key := bindName + "." + lf.name
					save(key)
					l.cellsByName[key] = lid
					leafRefs = append(leafRefs, map[string]any{"path": lf.name, "cell": lid})
				}
				armNode.Props["bindLeaves"] = leafRefs
			}
		}

		for j, c := range arm.Children {
			child, err := l.lowerNode(c, joinPath(armNode.ID, j))
			if err != nil {
				// restore before bubbling
				for _, s := range saved {
					if s.had {
						l.cellsByName[s.key] = s.id
					} else {
						delete(l.cellsByName, s.key)
					}
				}
				return ir.Node{}, err
			}
			armNode.Children = append(armNode.Children, child)
		}
		for _, s := range saved {
			if s.had {
				l.cellsByName[s.key] = s.id
			} else {
				delete(l.cellsByName, s.key)
			}
		}
		out.Children = append(out.Children, armNode)
	}

	// Strict exhaustiveness: every variant must be covered.
	var missing []string
	for _, v := range td.Variants {
		if !covered[v] {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
			fmt.Sprintf("match on union %q is not exhaustive: missing %s", typeName, strings.Join(quoteEach(missing), ", ")))
	}
	if len(out.Children) == 0 {
		return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
			fmt.Sprintf("match on union %q has no arms", typeName))
	}
	return out, nil
}

// lowerUnionConstruct recognizes the two discriminated-union build
// forms in an assignment whose LHS is a union cell:
//
//	cell = variant            (unit variant → {tag})
//	cell = variant(payload)    (payloaded variant → {tag, value})
//
// It returns handled=false (no error) when the RHS is neither — a real
// op call returning the union, or a cross-cell copy — so the caller
// continues with its normal handling.
func (l *lowerer) lowerUnionConstruct(rhs *ast.Node, cellID, typeName string) (ir.Action, bool, error) {
	td := l.knownTypes[typeName]
	tagged := td.HasPayloads()
	spec := func(name string) (ir.VariantSpec, bool) {
		for _, vs := range td.VariantSpecs {
			if vs.Name == name {
				return vs, true
			}
		}
		return ir.VariantSpec{}, false
	}

	// Unit construction: `cell = variant` (a bare ref that names a
	// variant). A ref that doesn't name a variant (another union cell,
	// say) falls through.
	if rhs.Kind == "ref" && len(rhs.Args) == 1 {
		if vs, ok := spec(rhs.Args[0].String); ok {
			if vs.Payload != nil {
				return ir.Action{}, false, diag.New("lower", rhs.Pos.Line, rhs.Pos.Col,
					fmt.Sprintf("variant %q carries a payload — construct it as `%s(<value>)`", vs.Name, vs.Name))
			}
			return ir.Action{
				Kind:   "set_variant",
				CellID: cellID,
				Args:   map[string]any{"tag": vs.Name, "tagged": tagged},
			}, true, nil
		}
		return ir.Action{}, false, nil
	}

	// Payloaded construction: `cell = variant(payload)`. A call whose
	// head doesn't name a variant (a real op) falls through.
	if rhs.Kind == "op_call" && len(rhs.Args) >= 1 {
		head := rhs.Args[0].String
		vs, ok := spec(head)
		if !ok {
			return ir.Action{}, false, nil
		}
		if vs.Payload == nil {
			return ir.Action{}, false, diag.New("lower", rhs.Pos.Line, rhs.Pos.Col,
				fmt.Sprintf("variant %q is a unit variant — write `%s` without a payload", head, head))
		}
		if len(rhs.Children) != 1 {
			return ir.Action{}, false, diag.New("lower", rhs.Pos.Line, rhs.Pos.Col,
				fmt.Sprintf("variant %q takes exactly one payload value", head))
		}
		payload, err := resolveAppendArg(rhs.Children[0], l)
		if err != nil {
			return ir.Action{}, false, err
		}
		return ir.Action{
			Kind:   "set_variant",
			CellID: cellID,
			Args:   map[string]any{"tag": head, "payload": payload, "tagged": tagged},
		}, true, nil
	}

	return ir.Action{}, false, nil
}

// quoteEach wraps each string in backticks for diagnostic lists.
func quoteEach(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = "`" + x + "`"
	}
	return out
}

// lowerFor handles `for <name> in <list>` blocks. The shape is the same
// IR-level pattern the Go ui.For emits: one KindForItem per existing child
// + one template KindForItem with the `$ITEM` sentinel cell that the
// runtime substitutes on append.
func (l *lowerer) lowerFor(a *ast.Node, path string) (ir.Node, error) {
	if len(a.Args) != 3 ||
		a.Args[0].Kind != ast.ValueIdent ||
		a.Args[1].Kind != ast.ValueIdent || a.Args[1].String != "in" ||
		a.Args[2].Kind != ast.ValueIdent {
		return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
			"for needs the form `for <name> in <list>`")
	}
	loopVar := a.Args[0].String
	listName := a.Args[2].String

	listID, _, ok := l.lookupCell(listName)
	if !ok {
		d := diag.New("lower", a.Args[2].Pos.Line, a.Args[2].Pos.Col,
			fmt.Sprintf("unknown name %q in `for`", listName))
		if hint := l.suggestCellName(listName); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.Node{}, d
	}
	childIDs, isList := l.listCells[listID]
	if !isList {
		return ir.Node{}, diag.New("lower", a.Args[2].Pos.Line, a.Args[2].Pos.Col,
			fmt.Sprintf("%q is not a list cell", listName))
	}

	// Optional filter=<cell> kwarg: show/hide rows based on text match.
	var filterCellID string
	for k, v := range a.Kwargs {
		if k == "filter" {
			if v.Kind != ast.ValueIdent {
				return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
					"for: filter= must be a cell name")
			}
			fid, _, fok := l.lookupCell(v.String)
			if !fok {
				return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("for: unknown cell %q in filter=", v.String))
			}
			filterCellID = fid
		} else {
			return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
				fmt.Sprintf("for: unknown keyword arg %q", k))
		}
	}

	if len(a.Children) != 1 {
		return ir.Node{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
			"for body must be a single component (wrap multiple in a `stack`)")
	}
	body := a.Children[0]

	// Structured-list field shape (nil for scalar lists). When present,
	// the body can write `item.field` and we register one alias per
	// (loopVar, field) pair so lookupCell resolves the dotted name to a
	// per-row dotted cell id like "$ITEM.label" / "<row>.label".
	fields := l.listFields[listID]

	// Save/restore loopVar AND any field aliases in the symbol table so
	// loops can nest without leaking bindings.
	type savedEntry struct {
		key string
		id  string
		had bool
	}
	var saved []savedEntry
	captureSave := func(key string) {
		v, ok := l.cellsByName[key]
		saved = append(saved, savedEntry{key: key, id: v, had: ok})
	}
	captureSave(loopVar)
	for _, f := range fields {
		captureSave(loopVar + "." + f.name)
	}
	defer func() {
		for _, s := range saved {
			if s.had {
				l.cellsByName[s.key] = s.id
			} else {
				delete(l.cellsByName, s.key)
			}
		}
	}()

	forProps := map[string]any{"cell": listID}
	if filterCellID != "" {
		forProps["filter-cell"] = filterCellID
	}
	out := ir.Node{
		Kind:  ir.KindFor,
		ID:    path,
		Props: forProps,
	}

	for i, childID := range childIDs {
		l.cellsByName[loopVar] = childID
		// Register dotted aliases pointing at each field's per-row cell.
		// Init values exist because append_struct_item populates them at
		// runtime; for SSR we use the field default (or, eventually, the
		// row's actual current values when reading them is supported).
		for _, f := range fields {
			fkey := loopVar + "." + f.name
			fid := childID + "." + f.name
			l.cellsByName[fkey] = fid
			if _, present := l.cellInit[fid]; !present {
				l.cellInit[fid] = f.defVal
			}
		}
		rowID := fmt.Sprintf("%s/row-%d", path, i)
		bodyIR, err := l.lowerNode(body, joinPath(rowID, 0))
		if err != nil {
			return ir.Node{}, err
		}
		out.Children = append(out.Children, ir.Node{
			Kind:     ir.KindForItem,
			ID:       rowID,
			Props:    map[string]any{"cell": childID, "parent": listID},
			Children: []ir.Node{bodyIR},
		})
	}

	// Template row: $ITEM as the loopVar. We need cellInit["$ITEM"] for the
	// template body's static text (the renderer reads it for the initial
	// text content of bound nodes inside the template). Use the first
	// child's value as a representative — and remove it after lowering so
	// "$ITEM" doesn't leak into the cells map shipped to the runtime.
	var templateInit any
	if len(childIDs) > 0 {
		templateInit = l.cellInit[childIDs[0]]
	}
	l.cellInit["$ITEM"] = templateInit
	l.cellsByName[loopVar] = "$ITEM"
	// Register $ITEM.<field> aliases + their initial values for SSR. The
	// template body refers to these by dotted name; runtime substitutes
	// $ITEM → <row-id> in attribute values on append.
	for _, f := range fields {
		fkey := loopVar + "." + f.name
		fid := "$ITEM." + f.name
		l.cellsByName[fkey] = fid
		l.cellInit[fid] = f.defVal
	}
	tmplID := fmt.Sprintf("%s/tmpl", path)
	tmplBody, err := l.lowerNode(body, joinPath(tmplID, 0))
	delete(l.cellInit, "$ITEM")
	// Strip $ITEM.<field> too — these would otherwise ship in the
	// initial cells map even though they only exist as substitution
	// placeholders, not real runtime cells.
	for _, f := range fields {
		delete(l.cellInit, "$ITEM."+f.name)
	}
	if err != nil {
		return ir.Node{}, err
	}
	out.Children = append(out.Children, ir.Node{
		Kind: ir.KindForItem,
		ID:   tmplID,
		Props: map[string]any{
			"cell":     "$ITEM",
			"parent":   listID,
			"template": true,
		},
		Children: []ir.Node{tmplBody},
	})

	return out, nil
}

// lowerOpCall turns an `op_call` (assignment-RHS) or `op_call_stmt`
// (statement) AST node into a `call_op` Action. Validates that:
//
//   - the op name resolves to a declared query / command,
//   - the arity matches the declared inputs,
//   - each arg is either a literal or a cell ref (richer expressions
//     in arg position are deferred).
//
// targetCellID is "" for fire-and-forget calls; the runtime awaits
// the promise but discards the result. When non-empty, the runtime
// writes the awaited result into that cell and fires its bindings.
//
// Args wire shape:
//
//	{
//	  "op":   "<Name>",
//	  "kind": "query" | "command",
//	  "args": [ <resolved-arg>, ... ],  // literals direct, cell refs as "$cell.<id>"
//	}
func (l *lowerer) lowerOpCall(c *ast.Node, targetCellID string) (ir.Action, error) {
	if len(c.Args) == 0 || c.Args[0].Kind != ast.ValueIdent {
		return ir.Action{}, diag.New("lower", c.Pos.Line, c.Pos.Col,
			"op call missing name")
	}
	name := c.Args[0].String
	sig, ok := l.knownOps[name]
	if !ok {
		d := diag.New("lower", c.Args[0].Pos.Line, c.Args[0].Pos.Col,
			fmt.Sprintf("unknown query / command %q", name))
		cands := make([]string, 0, len(l.knownOps))
		for k := range l.knownOps {
			cands = append(cands, k)
		}
		if hint := suggestFromSet(name, cands); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.Action{}, d
	}
	if len(c.Children) != len(sig.inputs) {
		return ir.Action{}, diag.New("lower", c.Pos.Line, c.Pos.Col,
			fmt.Sprintf("%s %q takes %d arg(s), got %d", sig.kind, name, len(sig.inputs), len(c.Children)))
	}
	args := make([]any, 0, len(c.Children))
	for _, arg := range c.Children {
		v, err := resolveAppendArg(arg, l)
		if err != nil {
			return ir.Action{}, err
		}
		args = append(args, v)
	}
	act := ir.Action{
		Kind:   "call_op",
		CellID: targetCellID,
		Args: map[string]any{
			"op":   name,
			"kind": sig.kind,
			"args": args,
		},
	}
	// Commands carry the lifecycle cells so the emitter wraps the inline
	// await (pending around it, failed/error on a throw). Queries are
	// fire-on-mount reads with no in-flight affordance, so they stay bare.
	if sig.kind == "command" {
		l.stampOpLifecycle(&act, name)
	}
	// `Op(...) then navigate "<path>"` success hook: the navigation runs
	// in the op's success path (after the await, inside the lifecycle
	// try for commands), so a failed op trips .failed and does NOT
	// navigate.
	if tn, ok := c.Kwargs["then_navigate"]; ok {
		act.Args["then_navigate"] = tn.String
	}
	return act, nil
}

// lowerStreamAssign turns `cell <- StreamOp(args)` into a
// call_op_stream action. The op must resolve to a declared `stream`
// returning a scalar String, and the target must be a plain String
// cell — at runtime the cell is cleared, then each delta is appended
// as it arrives and the binding flushes per chunk. List/record targets
// and non-String element types are out of scope until the message-list
// streaming shape is designed.
func (l *lowerer) lowerStreamAssign(stmt *ast.Node) (ir.Action, error) {
	if len(stmt.Args) == 0 || stmt.Args[0].Kind != ast.ValueIdent {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			"stream-into missing target cell")
	}

	// Tuple form `(t1, t2, ...) <- StreamOp(...)`: fan one stream op into
	// N channels of a record-typed delta.
	if len(stmt.Args) > 1 {
		return l.lowerStreamMultiChannel(stmt)
	}

	lhsName := stmt.Args[0].String
	if op, ro := l.implicitOpCellOwner(lhsName); ro {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("%q is read-only — the runtime maintains it while %s %q is in flight / on failure", lhsName, l.knownOps[op].kind, op))
	}

	// `listCell.last.<field> <- StreamOp(args)` streams into the most
	// recently appended row's field — the transcript pattern (append an
	// empty assistant row, then stream the reply into it).
	if strings.Contains(lhsName, ".last.") {
		return l.lowerStreamIntoRow(stmt, lhsName)
	}

	cellID, _, ok := l.lookupCell(lhsName)
	if !ok {
		d := diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("unknown cell %q in `<-` statement", lhsName))
		if hint := l.suggestCellName(lhsName); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.Action{}, d
	}
	if _, isList := l.listCells[cellID]; isList {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("`<-` target %q is a list cell; streaming into a list row isn't supported yet — stream into a String cell", lhsName))
	}
	if len(stmt.Children) != 1 {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			"`<-` missing stream op call on the right")
	}

	// Reuse lowerOpCall to validate name + arity + args, then transform
	// the result into call_op_stream and stamp the target cell.
	inner, err := l.lowerOpCall(stmt.Children[0], "")
	if err != nil {
		return ir.Action{}, err
	}
	opName, _ := inner.Args["op"].(string)
	sig := l.knownOps[opName]
	if sig.kind != "stream" {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("`<-` requires a stream op; %q is a %s — use `=` for queries / commands", opName, sig.kind))
	}
	if sig.ret.Name != "String" || sig.ret.Optional {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("stream %q must return String to fill a String cell (got %q)", opName, sig.ret.Name))
	}
	inner.Kind = "call_op_stream"
	inner.CellID = cellID
	l.stampOpLifecycle(&inner, opName)
	return inner, nil
}

// lowerStreamIntoRow handles `listCell.last.<field> <- StreamOp(args)`:
// stream the op's deltas into the named field of the list's most
// recently appended row. The transcript pattern — append an empty
// assistant row, then stream the reply into its text field — needs
// this so a chat keeps multi-turn history rather than one scalar reply.
func (l *lowerer) lowerStreamIntoRow(stmt *ast.Node, lhsName string) (ir.Action, error) {
	idx := strings.Index(lhsName, ".last.")
	listName := lhsName[:idx]
	field := lhsName[idx+len(".last."):]
	if strings.Contains(field, ".") {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("`<-` into %q: only a single field after `.last.` is supported", lhsName))
	}
	cellID, _, ok := l.lookupCell(listName)
	if !ok {
		d := diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("unknown list %q in `<-` statement", listName))
		if hint := l.suggestCellName(listName); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.Action{}, d
	}
	fields, isList := l.listFields[cellID]
	if !isList {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("`.last` requires a structured list; %q is not one", listName))
	}
	var fld *fieldSpec
	for i := range fields {
		if fields[i].name == field {
			fld = &fields[i]
			break
		}
	}
	if fld == nil {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("list %q has no field %q", listName, field))
	}
	if fld.ftype != "String" {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("`<-` field %q must be String to stream into (got %q)", field, fld.ftype))
	}

	inner, err := l.lowerOpCall(stmt.Children[0], "")
	if err != nil {
		return ir.Action{}, err
	}
	opName, _ := inner.Args["op"].(string)
	sig := l.knownOps[opName]
	if sig.kind != "stream" {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("`<-` requires a stream op; %q is a %s", opName, sig.kind))
	}
	if sig.ret.Name != "String" || sig.ret.Optional {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("stream %q must return String (got %q)", opName, sig.ret.Name))
	}
	inner.Kind = "call_op_stream"
	inner.Args["list"] = cellID
	inner.Args["field"] = field
	l.stampOpLifecycle(&inner, opName)
	return inner, nil
}

// lowerStreamMultiChannel handles `(t1, t2, ...) <- StreamOp(args)`: one
// stream op (one backend request) fanned into N live targets, each bound
// to one channel of the op's record-typed delta. Targets map positionally
// to the record's fields — target[i] receives the channel named by the
// i-th field. All targets are either plain String cells (scalar form) or
// `<list>.last.<field>` row fields on the same list (transcript form);
// the two cannot be mixed in one call.
//
// The emitted call_op_stream carries `channels`, a slice of
// {channel, cell} (scalar) or {channel, field} (row) bindings the runtime
// demuxes each delta against. The row form also stamps `list`.
func (l *lowerer) lowerStreamMultiChannel(stmt *ast.Node) (ir.Action, error) {
	if len(stmt.Children) != 1 {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			"`<-` missing stream op call on the right")
	}
	inner, err := l.lowerOpCall(stmt.Children[0], "")
	if err != nil {
		return ir.Action{}, err
	}
	opName, _ := inner.Args["op"].(string)
	sig := l.knownOps[opName]
	if sig.kind != "stream" {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("`<-` requires a stream op; %q is a %s — use `=` for queries / commands", opName, sig.kind))
	}
	if len(sig.channels) == 0 {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("stream %q returns a single String; use `cell <- %s(...)` for one target, not a `(...)` tuple — a tuple needs a record-typed (multi-channel) stream", opName, opName))
	}
	if len(stmt.Args) != len(sig.channels) {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("stream %q has %d channels (%s) but the target list has %d — they must match positionally",
				opName, len(sig.channels), strings.Join(sig.channels, ", "), len(stmt.Args)))
	}

	// Classify the targets: all row (`.last.`) or all scalar. Mixing the
	// two forms in one call is rejected.
	rowForm := strings.Contains(stmt.Args[0].String, ".last.")
	for _, t := range stmt.Args {
		if strings.Contains(t.String, ".last.") != rowForm {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				"stream-into targets must be all `<list>.last.<field>` rows or all plain cells, not a mix")
		}
	}

	bindings := make([]any, 0, len(stmt.Args))

	if rowForm {
		var listCellID string
		for i, t := range stmt.Args {
			lhsName := t.String
			idx := strings.Index(lhsName, ".last.")
			listName := lhsName[:idx]
			field := lhsName[idx+len(".last."):]
			if strings.Contains(field, ".") {
				return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
					fmt.Sprintf("`<-` into %q: only a single field after `.last.` is supported", lhsName))
			}
			cellID, _, ok := l.lookupCell(listName)
			if !ok {
				d := diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
					fmt.Sprintf("unknown list %q in `<-` statement", listName))
				if hint := l.suggestCellName(listName); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Action{}, d
			}
			if listCellID == "" {
				listCellID = cellID
			} else if cellID != listCellID {
				return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
					"all `.last.` stream-into targets must address the same list")
			}
			fields, isList := l.listFields[cellID]
			if !isList {
				return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
					fmt.Sprintf("`.last` requires a structured list; %q is not one", listName))
			}
			var fld *fieldSpec
			for fi := range fields {
				if fields[fi].name == field {
					fld = &fields[fi]
					break
				}
			}
			if fld == nil {
				return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
					fmt.Sprintf("list %q has no field %q", listName, field))
			}
			if fld.ftype != "String" {
				return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
					fmt.Sprintf("`<-` field %q must be String to stream into (got %q)", field, fld.ftype))
			}
			bindings = append(bindings, map[string]any{"channel": sig.channels[i], "field": field})
		}
		inner.Kind = "call_op_stream"
		inner.Args["list"] = listCellID
		inner.Args["channels"] = bindings
		l.stampOpLifecycle(&inner, opName)
		return inner, nil
	}

	// Scalar form: each target is a plain String cell.
	for i, t := range stmt.Args {
		lhsName := t.String
		if op, ro := l.implicitOpCellOwner(lhsName); ro {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				fmt.Sprintf("%q is read-only — the runtime maintains it while %s %q is in flight / on failure", lhsName, l.knownOps[op].kind, op))
		}
		cellID, _, ok := l.lookupCell(lhsName)
		if !ok {
			d := diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				fmt.Sprintf("unknown cell %q in `<-` statement", lhsName))
			if hint := l.suggestCellName(lhsName); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			return ir.Action{}, d
		}
		if _, isList := l.listCells[cellID]; isList {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				fmt.Sprintf("`<-` target %q is a list cell; stream into a String cell or a `%s.last.<field>` row", lhsName, lhsName))
		}
		bindings = append(bindings, map[string]any{"channel": sig.channels[i], "cell": cellID})
	}
	inner.Kind = "call_op_stream"
	inner.Args["channels"] = bindings
	l.stampOpLifecycle(&inner, opName)
	return inner, nil
}

// lowerRecordSpread turns `cell = OpReturningRecord(args)` into a
// call_op_spread action. The op's return type must be the same
// non-optional, non-list record as the LHS state's type; the
// runtime awaits the call, then writes each leaf field of the
// response into the corresponding inflated leaf cell.
//
// The action's wire shape extends call_op with a `spread` slice
// of {path, cell} pairs giving the codegen everything it needs
// without re-walking the type at emit time.
func (l *lowerer) lowerRecordSpread(stmt *ast.Node, lhsName, recordTypeName string) (ir.Action, error) {
	if len(stmt.Children) != 1 {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("record-typed state %q can only be assigned from a query / command call", lhsName))
	}
	rhs := stmt.Children[0]
	if rhs.Kind != "op_call" {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("record-typed state %q can only be assigned from a query / command call (RHS is %q)", lhsName, rhs.Kind))
	}

	// Reuse lowerOpCall to validate / resolve args. Empty target cell;
	// we transform the resulting action into call_op_spread below.
	inner, err := l.lowerOpCall(rhs, "")
	if err != nil {
		return ir.Action{}, err
	}

	opName, _ := inner.Args["op"].(string)
	sig := l.knownOps[opName]
	if sig.ret.Optional {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("can't spread optional return type %q? into record state %q", sig.ret.Name, lhsName))
	}
	if sig.ret.Name == "List" {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("can't spread list return type into record state %q", lhsName))
	}
	if sig.ret.Name != recordTypeName {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("%s %q returns %q, but state %q is %q",
				sig.kind, opName, sig.ret.Name, lhsName, recordTypeName))
	}

	td, ok := l.knownTypes[recordTypeName]
	if !ok || td.Kind != "record" {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("internal: state %q references unknown record %q", lhsName, recordTypeName))
	}
	leaves, err := l.flattenRecordFields("", td)
	if err != nil {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("record state %q: %v", lhsName, err))
	}

	spread := make([]any, 0, len(leaves))
	for _, leaf := range leaves {
		cellID, ok := l.cellsByName[lhsName+"."+leaf.name]
		if !ok {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				fmt.Sprintf("internal: record state %q leaf %q has no cell", lhsName, leaf.name))
		}
		spread = append(spread, map[string]any{"path": leaf.name, "cell": cellID})
	}

	inner.Kind = "call_op_spread"
	inner.Args["spread"] = spread
	return inner, nil
}

// lowerListPopulate handles `listCell = OpReturningList(args)`. The
// op must return List<Record> where the record matches the list's
// row schema. The produced action is `call_op_list`: the codegen
// awaits the call, clears the list, and appends each element as a
// struct row. The `fields` key lists the row's field names so the
// codegen knows which keys to read from each response element.
func (l *lowerer) lowerListPopulate(stmt, rhs *ast.Node, lhsName, cellID string) (ir.Action, error) {
	inner, err := l.lowerOpCall(rhs, "")
	if err != nil {
		return ir.Action{}, err
	}

	opName, _ := inner.Args["op"].(string)
	sig := l.knownOps[opName]

	if sig.ret.Name != "List" || len(sig.ret.GenericArgs) != 1 {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("%s %q returns %q, need List<Record> to populate list state %q",
				sig.kind, opName, sig.ret.Name, lhsName))
	}
	elemType := sig.ret.GenericArgs[0]
	td, ok := l.knownTypes[elemType.Name]
	if !ok || td.Kind != "record" {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			fmt.Sprintf("%s %q returns List<%s>, but %s is not a known record type",
				sig.kind, opName, elemType.Name, elemType.Name))
	}

	fields := l.listFields[cellID]
	fieldNames := make([]any, 0, len(fields))
	for _, f := range fields {
		fieldNames = append(fieldNames, f.name)
	}

	inner.Kind = "call_op_list"
	inner.CellID = cellID
	inner.Args["fields"] = fieldNames
	return inner, nil
}

// lowerMethodCall turns `receiver.method(args)` statements into actions.
// Supported methods (S0):
//
//	list.append(literal)  → append_item action on the list cell
//	list.remove(cellRef)  → remove_item action targeting the cellRef
func (l *lowerer) lowerMethodCall(stmt *ast.Node) (ir.Action, error) {
	if len(stmt.Args) != 2 {
		return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
			"method call missing receiver or method name")
	}
	receiverName := stmt.Args[0].String
	method := stmt.Args[1].String

	cellID, _, ok := l.lookupCell(receiverName)
	if !ok {
		d := diag.New("lower", stmt.Args[0].Pos.Line, stmt.Args[0].Pos.Col,
			fmt.Sprintf("unknown cell %q in method call", receiverName))
		if hint := l.suggestCellName(receiverName); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.Action{}, d
	}

	switch method {
	case "append":
		if _, isList := l.listCells[cellID]; !isList {
			return ir.Action{}, diag.New("lower",
				stmt.Args[0].Pos.Line, stmt.Args[0].Pos.Col,
				fmt.Sprintf("%q is not a list cell; .append() requires a list", receiverName))
		}
		// Structured list — positional args fill fields in decl order;
		// missing tail args fall back to declared defaults. Cell-ref args
		// resolve at runtime via $cell.<id>; literals embed directly.
		if fields, structured := l.listFields[cellID]; structured {
			if len(stmt.Children) > len(fields) {
				return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
					fmt.Sprintf(".append on structured list %q expects ≤ %d args (one per field), got %d",
						receiverName, len(fields), len(stmt.Children)))
			}
			fieldVals := map[string]any{}
			for i, f := range fields {
				if i < len(stmt.Children) {
					v, err := resolveAppendArg(stmt.Children[i], l)
					if err != nil {
						return ir.Action{}, err
					}
					fieldVals[f.name] = v
				} else {
					fieldVals[f.name] = f.defVal
				}
			}
			return ir.Action{
				Kind:   "append_struct_item",
				CellID: cellID,
				Args:   map[string]any{"fields": fieldVals},
			}, nil
		}
		// Scalar list path — single value, literal or cell ref.
		if len(stmt.Children) != 1 {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				".append(value) takes exactly one argument")
		}
		v, err := resolveAppendArg(stmt.Children[0], l)
		if err != nil {
			return ir.Action{}, err
		}
		return ir.Action{
			Kind:   "append_item",
			CellID: cellID,
			Args:   map[string]any{"value": v},
		}, nil

	case "remove":
		if _, isList := l.listCells[cellID]; !isList {
			return ir.Action{}, diag.New("lower",
				stmt.Args[0].Pos.Line, stmt.Args[0].Pos.Col,
				fmt.Sprintf("%q is not a list cell; .remove() requires a list", receiverName))
		}
		if len(stmt.Children) != 1 {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				".remove(item) takes exactly one argument")
		}
		argNode := stmt.Children[0]
		if argNode.Kind != "ref" || len(argNode.Args) == 0 {
			return ir.Action{}, diag.New("lower", argNode.Pos.Line, argNode.Pos.Col,
				".remove() takes a cell reference (e.g. the loop variable inside `for`)")
		}
		targetName := argNode.Args[0].String
		targetID, _, ok := l.lookupCell(targetName)
		if !ok {
			d := diag.New("lower", argNode.Pos.Line, argNode.Pos.Col,
				fmt.Sprintf("unknown cell %q in .remove()", targetName))
			if hint := l.suggestCellName(targetName); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			return ir.Action{}, d
		}
		return ir.Action{
			Kind:   "remove_item",
			CellID: cellID,
			Args:   map[string]any{"target": targetID},
		}, nil

	case "clear":
		if _, isList := l.listCells[cellID]; !isList {
			return ir.Action{}, diag.New("lower", stmt.Args[0].Pos.Line, stmt.Args[0].Pos.Col,
				fmt.Sprintf("%q is not a list cell; .clear() requires a list", receiverName))
		}
		if len(stmt.Children) != 0 {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				".clear() takes no arguments")
		}
		return ir.Action{Kind: "clear_list", CellID: cellID}, nil

	case "swap":
		if _, isList := l.listCells[cellID]; !isList {
			return ir.Action{}, diag.New("lower", stmt.Args[0].Pos.Line, stmt.Args[0].Pos.Col,
				fmt.Sprintf("%q is not a list cell; .swap() requires a list", receiverName))
		}
		if len(stmt.Children) != 2 {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				".swap(i, j) takes two integer arguments")
		}
		i, ierr := intLitOnly(stmt.Children[0])
		j, jerr := intLitOnly(stmt.Children[1])
		if ierr != nil {
			return ir.Action{}, ierr
		}
		if jerr != nil {
			return ir.Action{}, jerr
		}
		return ir.Action{Kind: "swap_items", CellID: cellID, Args: map[string]any{"i": i, "j": j}}, nil

	case "select":
		if _, isList := l.listCells[cellID]; !isList {
			return ir.Action{}, diag.New("lower", stmt.Args[0].Pos.Line, stmt.Args[0].Pos.Col,
				fmt.Sprintf("%q is not a list cell; .select() requires a list", receiverName))
		}
		if len(stmt.Children) != 1 {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				".select(item) takes exactly one argument")
		}
		argNode := stmt.Children[0]
		if argNode.Kind != "ref" || len(argNode.Args) == 0 {
			return ir.Action{}, diag.New("lower", argNode.Pos.Line, argNode.Pos.Col,
				".select() takes a cell reference (typically the loop variable inside `for`)")
		}
		targetName := argNode.Args[0].String
		targetID, _, ok := l.lookupCell(targetName)
		if !ok {
			d := diag.New("lower", argNode.Pos.Line, argNode.Pos.Col,
				fmt.Sprintf("unknown cell %q in .select()", targetName))
			if hint := l.suggestCellName(targetName); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			return ir.Action{}, d
		}
		return ir.Action{Kind: "select_in_list", CellID: cellID, Args: map[string]any{"target": targetID}}, nil

	case "create_random":
		if _, isList := l.listCells[cellID]; !isList {
			return ir.Action{}, diag.New("lower", stmt.Args[0].Pos.Line, stmt.Args[0].Pos.Col,
				fmt.Sprintf("%q is not a list cell; .create_random() requires a list", receiverName))
		}
		if len(stmt.Children) != 1 {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				".create_random(count) takes one integer argument")
		}
		count, cerr := intLitOnly(stmt.Children[0])
		if cerr != nil {
			return ir.Action{}, cerr
		}
		return ir.Action{Kind: "create_batch_random", CellID: cellID,
			Args: map[string]any{"count": count, "replace": true}}, nil

	case "append_random":
		if _, isList := l.listCells[cellID]; !isList {
			return ir.Action{}, diag.New("lower", stmt.Args[0].Pos.Line, stmt.Args[0].Pos.Col,
				fmt.Sprintf("%q is not a list cell; .append_random() requires a list", receiverName))
		}
		if len(stmt.Children) != 1 {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				".append_random(count) takes one integer argument")
		}
		count, cerr := intLitOnly(stmt.Children[0])
		if cerr != nil {
			return ir.Action{}, cerr
		}
		return ir.Action{Kind: "create_batch_random", CellID: cellID,
			Args: map[string]any{"count": count, "replace": false}}, nil

	case "update_every":
		if _, isList := l.listCells[cellID]; !isList {
			return ir.Action{}, diag.New("lower", stmt.Args[0].Pos.Line, stmt.Args[0].Pos.Col,
				fmt.Sprintf("%q is not a list cell; .update_every() requires a list", receiverName))
		}
		if len(stmt.Children) != 2 {
			return ir.Action{}, diag.New("lower", stmt.Pos.Line, stmt.Pos.Col,
				".update_every(stride, suffix) takes a stride and a suffix")
		}
		stride, serr := intLitOnly(stmt.Children[0])
		if serr != nil {
			return ir.Action{}, serr
		}
		suffix, suferr := stringLitOnly(stmt.Children[1])
		if suferr != nil {
			return ir.Action{}, suferr
		}
		return ir.Action{Kind: "update_every", CellID: cellID,
			Args: map[string]any{"stride": stride, "suffix": suffix}}, nil

	default:
		d := diag.New("lower", stmt.Args[1].Pos.Line, stmt.Args[1].Pos.Col,
			fmt.Sprintf("unknown method %q", method))
		if hint := suggestFromSet(method, []string{
			"append", "remove", "clear", "swap", "select",
			"create_random", "append_random", "update_every",
		}); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.Action{}, d
	}
}

// intLitOnly extracts an int from a literal expression node, erroring
// otherwise. Used by list-method arguments that v0 only accepts as ints.
func intLitOnly(n *ast.Node) (int64, error) {
	if n.Kind == "lit" && len(n.Args) == 1 && n.Args[0].Kind == ast.ValueInt {
		return n.Args[0].Int, nil
	}
	return 0, diag.New("lower", n.Pos.Line, n.Pos.Col,
		"expected integer literal")
}

// stringLitOnly extracts a string from a literal expression node.
func stringLitOnly(n *ast.Node) (string, error) {
	if n.Kind == "lit" && len(n.Args) == 1 && n.Args[0].Kind == ast.ValueString {
		return n.Args[0].String, nil
	}
	return "", diag.New("lower", n.Pos.Line, n.Pos.Col,
		"expected string literal")
}

// astLiteralToAny converts an ast.Value literal to its plain Go form for
// embedding in an Action's Args (which is map[string]any → JSON).
func astLiteralToAny(v ast.Value) any {
	switch v.Kind {
	case ast.ValueInt:
		return v.Int
	case ast.ValueString:
		return v.String
	default:
		return nil
	}
}

// suggestCellName returns a Levenshtein-based hint over the current view's
// declared cell names. Used for typos like `text conut` when `count` exists.
func (l *lowerer) suggestCellName(bad string) string {
	cands := make([]string, 0, len(l.cellsByName))
	for k := range l.cellsByName {
		cands = append(cands, k)
	}
	return suggestFromSet(bad, cands)
}

// --- Diagnostic helpers ---

func suggestFromSet(bad string, candidates []string) string {
	type scored struct {
		name string
		dist int
	}
	limit := 3
	if l := len(bad)/2 + 1; l < limit {
		limit = l
	}
	var ranked []scored
	for _, c := range candidates {
		d := levenshtein(bad, c)
		if d <= limit {
			ranked = append(ranked, scored{c, d})
		}
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].dist < ranked[j].dist })
	if len(ranked) == 0 {
		return ""
	}
	if len(ranked) == 1 {
		return fmt.Sprintf("%q", ranked[0].name)
	}
	return fmt.Sprintf("%q or %q", ranked[0].name, ranked[1].name)
}

func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			m := prev[j] + 1
			if cur[j-1]+1 < m {
				m = cur[j-1] + 1
			}
			if prev[j-1]+cost < m {
				m = prev[j-1] + cost
			}
			cur[j] = m
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func joinPath(parent string, idx int) string {
	parent = strings.TrimSuffix(parent, "/")
	return fmt.Sprintf("%s/%d", parent, idx)
}

// --- Type decls ---

// primitiveTypes is the closed set of built-in primitive type names
// that fields can reference without a prior `type` declaration. Used
// both to reject shadowing (no `type Int = …`) and to validate field
// type references.
var primitiveTypes = map[string]bool{
	"Int":    true,
	"String": true,
	"Bool":   true,
}

// builtinGenerics is the closed v0 set of generic-head names. Each
// entry maps the head to its required arity. `List<T>` is the only
// one for now; Map<K,V>, Set<T>, etc. land when needed.
var builtinGenerics = map[string]int{
	"List": 1,
}

// lowerTypes validates and emits the source-declared types in
// declaration order. Two-pass: pass 1 collects names + rejects
// duplicates / primitive shadows; pass 2 resolves each field's
// type reference against the collected name set.
//
// Errors are recorded into l.diags, not returned — a malformed type
// decl shouldn't abort the whole compile, since types aren't (yet)
// load-bearing for any other lowering stage.
func (l *lowerer) lowerTypes() []ir.TypeDecl {
	// Pass 1: collect declared names, error on dupes / primitive
	// shadows. Builds the name set without yet validating fields.
	known := map[string]bool{}
	for _, n := range l.typeNodes {
		if len(n.Args) == 0 || n.Args[0].Kind != ast.ValueIdent {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				"type declaration missing name"))
			continue
		}
		name := n.Args[0].String
		if primitiveTypes[name] {
			l.diags.Add(diag.New("lower", n.Args[0].Pos.Line, n.Args[0].Pos.Col,
				fmt.Sprintf("type name %q shadows a primitive", name)))
			continue
		}
		if known[name] {
			l.diags.Add(diag.New("lower", n.Args[0].Pos.Line, n.Args[0].Pos.Col,
				fmt.Sprintf("type %q is declared more than once", name)))
			continue
		}
		known[name] = true
	}

	// Pass 2: walk each declared type's body. The body is either ALL
	// field decls (record) or ALL variant decls (sum) — mixing is a
	// hard error. Sniff the first non-error child to decide which.
	out := make([]ir.TypeDecl, 0, len(l.typeNodes))
	for _, n := range l.typeNodes {
		if len(n.Args) == 0 || n.Args[0].Kind != ast.ValueIdent {
			continue // already errored above
		}
		name := n.Args[0].String
		if primitiveTypes[name] {
			continue // already errored above
		}
		td := l.lowerOneType(n, name, known)
		out = append(out, td)
	}
	return out
}

// lowerOneType processes one `type` decl. The body shape (record vs
// sum) is determined by sniffing the first non-error child; mixed
// bodies are rejected.
func (l *lowerer) lowerOneType(n *ast.Node, name string, known map[string]bool) ir.TypeDecl {
	td := ir.TypeDecl{Name: name}
	// Sniff body shape from the first real child.
	bodyKind := ""
	for _, c := range n.Children {
		if c == nil || c.Kind == "__error__" {
			continue
		}
		bodyKind = c.Kind
		break
	}
	switch bodyKind {
	case "":
		l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
			fmt.Sprintf("type %q has no body", name)))
		return td
	case "field_decl":
		td.Kind = "record"
		seen := map[string]bool{}
		for _, c := range n.Children {
			if c == nil || c.Kind == "__error__" {
				continue
			}
			if c.Kind != "field_decl" {
				l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
					fmt.Sprintf("type %q mixes records and sums; pick one body shape", name)))
				continue
			}
			f, err := lowerTypeField(c, known)
			if err != nil {
				l.diags.AddErr(err)
				continue
			}
			if seen[f.Name] {
				l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
					fmt.Sprintf("type %q has duplicate field %q", name, f.Name)))
				continue
			}
			seen[f.Name] = true
			td.Fields = append(td.Fields, f)
		}
		if len(td.Fields) == 0 {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				fmt.Sprintf("type %q has no fields", name)))
		}
	case "variant_decl":
		td.Kind = "sum"
		seen := map[string]bool{}
		for _, c := range n.Children {
			if c == nil || c.Kind == "__error__" {
				continue
			}
			if c.Kind != "variant_decl" {
				l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
					fmt.Sprintf("type %q mixes records and sums; pick one body shape", name)))
				continue
			}
			if len(c.Args) == 0 || c.Args[0].Kind != ast.ValueIdent {
				l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
					"variant declaration missing name"))
				continue
			}
			vname := c.Args[0].String
			if seen[vname] {
				l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
					fmt.Sprintf("type %q has duplicate variant %q", name, vname)))
				continue
			}
			seen[vname] = true
			spec := ir.VariantSpec{Name: vname}
			// `| name : Type` carries a single payload type. Resolve it
			// against the known type set, the same way a record field
			// type resolves. A bare `| name` stays a unit variant.
			if len(c.Args) >= 2 {
				pr, err := lowerTypeRef(c.Args[1], known)
				if err != nil {
					l.diags.AddErr(err)
				} else {
					spec.Payload = &pr
				}
			}
			td.Variants = append(td.Variants, vname)
			td.VariantSpecs = append(td.VariantSpecs, spec)
		}
		if len(td.Variants) == 0 {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				fmt.Sprintf("type %q has no variants", name)))
		}
	default:
		l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
			fmt.Sprintf("type body lines must be field decls or `| variant` lines, got %q", bodyKind)))
	}
	return td
}

// lowerTypeField parses one `field : Type [= default]` line into an
// ir.TypeFieldSpec. Defaults are rejected at this stage — they're
// only meaningful inside structured-list state decls.
func lowerTypeField(n *ast.Node, known map[string]bool) (ir.TypeFieldSpec, error) {
	if len(n.Args) < 2 ||
		n.Args[0].Kind != ast.ValueIdent ||
		n.Args[1].Kind != ast.ValueIdent {
		return ir.TypeFieldSpec{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
			"field declaration must be `name : Type`")
	}
	fieldName := n.Args[0].String
	if len(n.Children) > 0 {
		return ir.TypeFieldSpec{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
			"type fields cannot have default values; defaults are only valid in `state` list decls")
	}
	tr, err := lowerTypeRef(n.Args[1], known)
	if err != nil {
		return ir.TypeFieldSpec{}, err
	}
	return ir.TypeFieldSpec{Name: fieldName, Type: tr}, nil
}

// lowerTypeRef converts one ast.Value (a type reference) into an
// ir.TypeRef, recursively for generic args. Validates that:
//   - Generic-head names are in the closed builtinGenerics set.
//   - Non-generic names resolve to a primitive or declared type.
//   - Generic arity matches the head's required arg count.
func lowerTypeRef(v ast.Value, known map[string]bool) (ir.TypeRef, error) {
	if v.Kind != ast.ValueIdent {
		return ir.TypeRef{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
			"type reference must be a name")
	}
	out := ir.TypeRef{Name: v.String, Optional: v.Optional}
	if len(v.GenericArgs) > 0 {
		arity, isGeneric := builtinGenerics[v.String]
		if !isGeneric {
			return ir.TypeRef{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
				fmt.Sprintf("type %q does not take generic arguments", v.String))
		}
		if len(v.GenericArgs) != arity {
			return ir.TypeRef{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
				fmt.Sprintf("generic %q expects %d type argument(s), got %d",
					v.String, arity, len(v.GenericArgs)))
		}
		for _, arg := range v.GenericArgs {
			inner, err := lowerTypeRef(arg, known)
			if err != nil {
				return ir.TypeRef{}, err
			}
			out.GenericArgs = append(out.GenericArgs, inner)
		}
		return out, nil
	}
	// Non-generic leaf: must resolve to primitive or declared type.
	if !primitiveTypes[v.String] && !known[v.String] {
		d := diag.New("lower", v.Pos.Line, v.Pos.Col,
			fmt.Sprintf("unknown type %q", v.String))
		cands := []string{"Int", "String", "Bool", "List"}
		for k := range known {
			cands = append(cands, k)
		}
		if hint := suggestFromSet(v.String, cands); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.TypeRef{}, d
	}
	return out, nil
}

// --- Query / Command decls ---

// lowerOps validates and emits the source-declared queries and
// commands. Returns them in declaration order (queries first, then
// commands, each in source order). Errors land in l.diags; a bad op
// decl doesn't abort the rest of the compile.
//
// Validation: all referenced types (input types + return type) must
// resolve to a primitive or a declared type. Duplicate names within
// the same kind are rejected. Cross-kind name collisions are allowed
// — a query and a command can share a name because they live in
// distinct namespaces at the wire level (and in the generated client
// stubs).
func (l *lowerer) lowerOps() (queries []ir.Query, commands []ir.Command, streams []ir.Stream) {
	known := l.knownTypeNames()
	seenQuery := map[string]bool{}
	seenCommand := map[string]bool{}
	seenStream := map[string]bool{}
	for _, n := range l.opNodes {
		if len(n.Args) < 2 || n.Args[0].Kind != ast.ValueIdent {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				fmt.Sprintf("%s declaration missing name or return type", n.Kind)))
			continue
		}
		name := n.Args[0].String

		ret, err := lowerTypeRef(n.Args[1], known)
		if err != nil {
			l.diags.AddErr(err)
			continue
		}

		var inputs []ir.TypeFieldSpec
		seenArg := map[string]bool{}
		var argErr bool
		var explicitBackend string
		var invalidates []string
		for _, c := range n.Children {
			if c == nil || c.Kind == "__error__" {
				continue
			}
			switch c.Kind {
			case "op-backend":
				if len(c.Args) > 0 {
					explicitBackend = c.Args[0].String
				}
				continue
			case "invalidates":
				for _, a := range c.Args {
					invalidates = append(invalidates, a.String)
				}
				continue
			case "field_decl":
				// fall through
			default:
				l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
					fmt.Sprintf("%s args must be typed `name : Type` declarations, got %q", n.Kind, c.Kind)))
				argErr = true
				continue
			}
			f, ferr := lowerTypeField(c, known)
			if ferr != nil {
				l.diags.AddErr(ferr)
				argErr = true
				continue
			}
			if seenArg[f.Name] {
				l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
					fmt.Sprintf("%s %q has duplicate arg %q", n.Kind, name, f.Name)))
				argErr = true
				continue
			}
			seenArg[f.Name] = true
			inputs = append(inputs, f)
		}
		if argErr {
			continue
		}

		// Bind to a backend. v1 policy:
		//   0 declared    -> "" (legacy relative URL — back-compat)
		//   1 declared    -> that backend, automatically
		//   N declared    -> must specify `backend X` explicitly
		// An explicit `backend X` must always name a declared
		// backend.
		opBackend := explicitBackend
		if opBackend != "" {
			if !l.knownBackends[opBackend] {
				l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
					fmt.Sprintf("%s %q: backend %q is not declared", n.Kind, name, opBackend)))
				continue
			}
		} else {
			switch len(l.loweredBackends) {
			case 0:
				// keep ""; codegen falls back to relative URLs
			case 1:
				opBackend = l.loweredBackends[0].Name
			default:
				l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
					fmt.Sprintf("%s %q: project declares %d backends — add `backend <Name>` to disambiguate",
						n.Kind, name, len(l.loweredBackends))))
				continue
			}
		}

		switch n.Kind {
		case "query":
			if seenQuery[name] {
				l.diags.Add(diag.New("lower", n.Args[0].Pos.Line, n.Args[0].Pos.Col,
					fmt.Sprintf("query %q is declared more than once", name)))
				continue
			}
			seenQuery[name] = true
			queries = append(queries, ir.Query{Name: name, Inputs: inputs, Return: ret, Backend: opBackend})
		case "command":
			if seenCommand[name] {
				l.diags.Add(diag.New("lower", n.Args[0].Pos.Line, n.Args[0].Pos.Col,
					fmt.Sprintf("command %q is declared more than once", name)))
				continue
			}
			seenCommand[name] = true
			commands = append(commands, ir.Command{
				Name: name, Inputs: inputs, Return: ret,
				Backend: opBackend, Invalidates: invalidates,
			})
		case "stream":
			if seenStream[name] {
				l.diags.Add(diag.New("lower", n.Args[0].Pos.Line, n.Args[0].Pos.Col,
					fmt.Sprintf("stream %q is declared more than once", name)))
				continue
			}
			seenStream[name] = true
			// A record-typed return makes this a multi-channel stream: the
			// record's fields become live channels, so one backend request
			// drives N live regions. All fields must be String — channels
			// are token streams, one per region.
			var channels []string
			if td, ok := l.knownTypes[ret.Name]; ok && td.Kind == "record" {
				if ret.Optional {
					l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
						fmt.Sprintf("stream %q: multi-channel return type %q cannot be optional", name, ret.Name)))
					continue
				}
				bad := false
				for _, f := range td.Fields {
					if f.Type.Name != "String" || f.Type.Optional || len(f.Type.GenericArgs) > 0 {
						l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
							fmt.Sprintf("stream %q: channel %q of return type %q must be String (got %q)",
								name, f.Name, ret.Name, f.Type.Name)))
						bad = true
						continue
					}
					channels = append(channels, f.Name)
				}
				if bad {
					continue
				}
				if len(channels) < 2 {
					l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
						fmt.Sprintf("stream %q: multi-channel return type %q needs at least two String fields", name, ret.Name)))
					continue
				}
			}
			streams = append(streams, ir.Stream{Name: name, Inputs: inputs, Return: ret, Backend: opBackend, Channels: channels})
		}
	}
	// Validate invalidates references after all queries are known so
	// invalidating a query declared later in the file works.
	queryNames := map[string]bool{}
	for _, q := range queries {
		queryNames[q.Name] = true
	}
	for ci := range commands {
		for _, q := range commands[ci].Invalidates {
			if !queryNames[q] {
				l.diags.Add(diag.New("lower",
					0, 0,
					fmt.Sprintf("command %q invalidates unknown query %q",
						commands[ci].Name, q)))
			}
		}
	}
	return queries, commands, streams
}

// --- Test decls ---

// lowerTest validates one `test` AST decl and produces an ir.Test. The
// test body shape is:
//
//	test "name" =
//	  scenario <View>      // legacy form — target a Sigil view in-file
//	  scenario in <App>    // new form — target a declared App
//	  <step>               // zero or more
//	  ...
//
// The legacy view form is preserved for the existing in-tree examples;
// the `in <App>` form resolves against apps declared anywhere in this
// compilation unit. Step recognition is kept narrow; unknown step verbs
// error with a "did you mean" suggestion.
func (l *lowerer) lowerTest(t *ast.Node, viewName string) (ir.Test, error) {
	if len(t.Args) != 1 || t.Args[0].Kind != ast.ValueString {
		return ir.Test{}, diag.New("lower", t.Pos.Line, t.Pos.Col,
			"test declaration needs a quoted name")
	}
	out := ir.Test{Name: t.Args[0].String}

	body := make([]*ast.Node, 0, len(t.Children))
	for _, c := range t.Children {
		if c == nil || c.Kind == "__error__" {
			continue
		}
		body = append(body, c)
	}
	if len(body) == 0 {
		return ir.Test{}, diag.New("lower", t.Pos.Line, t.Pos.Col,
			fmt.Sprintf("test %q has no body", out.Name))
	}

	head := body[0]
	if head.Kind != "scenario" {
		return ir.Test{}, diag.New("lower", head.Pos.Line, head.Pos.Col,
			fmt.Sprintf("test %q must start with `scenario <View>` or `scenario in <App>`", out.Name))
	}

	// Two shapes: `scenario <View>` (legacy) or `scenario in <App>` (new).
	switch len(head.Args) {
	case 1:
		if head.Args[0].Kind != ast.ValueIdent {
			return ir.Test{}, diag.New("lower", head.Pos.Line, head.Pos.Col,
				"`scenario` takes a single view name or `in <App>`")
		}
		targetView := head.Args[0].String
		if viewName != "" && targetView != viewName {
			return ir.Test{}, diag.New("lower", head.Args[0].Pos.Line, head.Args[0].Pos.Col,
				fmt.Sprintf("test references unknown view %q; only %q is declared in this file",
					targetView, viewName))
		}
		out.View = targetView
	case 2:
		if head.Args[0].Kind != ast.ValueIdent || head.Args[0].String != "in" ||
			head.Args[1].Kind != ast.ValueIdent {
			return ir.Test{}, diag.New("lower", head.Pos.Line, head.Pos.Col,
				"`scenario` takes a view name or `in <App>`")
		}
		appName := head.Args[1].String
		if _, ok := l.knownApps[appName]; !ok {
			d := diag.New("lower", head.Args[1].Pos.Line, head.Args[1].Pos.Col,
				fmt.Sprintf("test references unknown app %q", appName))
			knownNames := make([]string, 0, len(l.knownApps))
			for n := range l.knownApps {
				knownNames = append(knownNames, n)
			}
			if hint := suggestFromSet(appName, knownNames); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			return ir.Test{}, d
		}
		out.App = appName
	default:
		return ir.Test{}, diag.New("lower", head.Pos.Line, head.Pos.Col,
			"`scenario` takes a view name or `in <App>`")
	}

	// Resolve the target's capability. A legacy `scenario <View>` is always
	// native. A `scenario in <App>` is foreign (Observe floor only) when its
	// web target carries the `external` flag — then introspection verbs
	// (which read Sigil-internal state) have no cell map and are refused.
	external := false
	if out.App != "" {
		if tgt, ok := l.knownApps[out.App].Targets["web"]; ok {
			external, _ = tgt.Config["external"].(bool)
		}
	}

	// Inline any flow invocations into concrete steps before lowering.
	steps, err := l.expandFlowSteps(body[1:])
	if err != nil {
		return ir.Test{}, err
	}
	for _, step := range steps {
		s, err := lowerStep(step)
		if err != nil {
			return ir.Test{}, err
		}
		if external {
			if verb, isIntrospect := introspectVerbs[s.Kind]; isIntrospect {
				return ir.Test{}, diag.New("lower", s.Line, s.Col,
					fmt.Sprintf("`%s` needs a Sigil-native target, but app %q is `external` (no cell map). Assert on rendered output instead — e.g. `expect-text` or `expect-count`.",
						verb, out.App))
			}
		}
		out.Steps = append(out.Steps, s)
	}
	return out, nil
}

// lowerApp validates one `app` AST decl and produces an ir.App. v0 body
// vocabulary is just `target <name>` blocks; each target's body is
// per-target config (today only `host "..."` for web). The lowerer
// treats the config bag as a string→any map and lets the runner / test
// codegen consume what they understand per target; unknown keys are
// preserved so future target adapters can pick them up without a
// schema migration here.
func (l *lowerer) lowerApp(a *ast.Node) (ir.App, error) {
	if len(a.Args) != 1 || a.Args[0].Kind != ast.ValueIdent {
		return ir.App{}, diag.New("lower", a.Pos.Line, a.Pos.Col,
			"app declaration needs a name identifier")
	}
	out := ir.App{
		Name:    a.Args[0].String,
		Targets: map[string]ir.AppTarget{},
	}

	for _, child := range a.Children {
		if child == nil || child.Kind == "__error__" {
			continue
		}
		if child.Kind != "target" {
			return ir.App{}, diag.New("lower", child.Pos.Line, child.Pos.Col,
				fmt.Sprintf("app %q: expected `target <name>` block, got %q",
					out.Name, child.Kind))
		}
		if len(child.Args) != 1 || child.Args[0].Kind != ast.ValueIdent {
			return ir.App{}, diag.New("lower", child.Pos.Line, child.Pos.Col,
				"`target` takes a single name identifier")
		}
		tname := child.Args[0].String
		if _, dup := out.Targets[tname]; dup {
			return ir.App{}, diag.New("lower", child.Pos.Line, child.Pos.Col,
				fmt.Sprintf("app %q: target %q already declared", out.Name, tname))
		}
		cfg := map[string]any{}
		for _, k := range child.Children {
			if k == nil || k.Kind == "__error__" {
				continue
			}
			// Each config line is an invocation: `host "..."` -> Kind="host",
			// Args[0] is the value. v0 recognizes single-string values plus
			// bare boolean flags (`external` -> cfg["external"]=true). A bare
			// flag marks a foreign (non-Sigil) target: the page is something
			// Sigil never compiled, so native-only checks (root-owns-viewport
			// layout invariants, cell introspection) don't apply to it.
			if len(k.Args) == 0 {
				cfg[k.Kind] = true
				continue
			}
			if len(k.Args) != 1 {
				return ir.App{}, diag.New("lower", k.Pos.Line, k.Pos.Col,
					fmt.Sprintf("app %q target %q: `%s` takes a single value",
						out.Name, tname, k.Kind))
			}
			v := k.Args[0]
			switch v.Kind {
			case ast.ValueString:
				cfg[k.Kind] = v.String
			case ast.ValueInt:
				cfg[k.Kind] = v.Int
			case ast.ValueIdent:
				cfg[k.Kind] = v.String
			default:
				return ir.App{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("app %q target %q `%s`: unsupported value kind",
						out.Name, tname, k.Kind))
			}
		}
		out.Targets[tname] = ir.AppTarget{Name: tname, Config: cfg}
	}

	return out, nil
}

// testStepVerbs is the closed v0 vocabulary of step heads. Used for
// "did you mean" suggestions when an unknown verb appears.
var testStepVerbs = []string{
	"click", "fill", "expect-text", "expect-no-text", "expect-cell", "expect-count",
	"expect-path", "extract", "match", "wait", "wait-for",
}

// armAssertable is the set of step kinds allowed inside a `match` arm. v0
// keeps arms to pure assertions (no drive/extract/nav inside a branch) so
// the whole match reports as one step; richer arm bodies can follow.
var armAssertable = map[string]bool{
	"expect_text": true, "expect_no_text": true, "expect_count": true, "expect_path": true,
}

// introspectVerbs are step kinds that read Sigil-internal state (the cell
// map today; the contract / router later) and so require a native target.
// Keyed by IR kind, valued by the surface spelling for diagnostics. A
// foreign (external) target exposes only the Observe floor, so using one
// of these against it is a compile error — see lowerTest.
var introspectVerbs = map[string]string{
	"expect_cell": "expect-cell",
}

// lowerStep converts one body line into an ir.Step. Each verb has its
// own arg shape; unknown verbs surface a Levenshtein suggestion.
func lowerStep(n *ast.Node) (ir.Step, error) {
	mkStep := func(kind string, args map[string]any) ir.Step {
		return ir.Step{Kind: kind, Args: args, Line: n.Pos.Line, Col: n.Pos.Col}
	}
	switch n.Kind {
	case "click":
		role, name, err := readRoleAndName(n, "click")
		if err != nil {
			return ir.Step{}, err
		}
		return mkStep("click", map[string]any{"role": role, "name": name}), nil
	case "fill":
		if len(n.Args) != 3 ||
			n.Args[0].Kind != ast.ValueIdent ||
			n.Args[1].Kind != ast.ValueString ||
			n.Args[2].Kind != ast.ValueString {
			return ir.Step{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
				"`fill` takes <role> <name-string> <value-string>")
		}
		return mkStep("fill", map[string]any{
			"role":  n.Args[0].String,
			"name":  n.Args[1].String,
			"value": n.Args[2].String,
		}), nil
	case "expect-text":
		if len(n.Args) != 1 || n.Args[0].Kind != ast.ValueString {
			return ir.Step{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
				"`expect-text` takes one string argument (visible text to assert)")
		}
		return mkStep("expect_text", map[string]any{"text": n.Args[0].String}), nil
	case "expect-no-text":
		if len(n.Args) != 1 || n.Args[0].Kind != ast.ValueString {
			return ir.Step{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
				"`expect-no-text` takes one string argument (visible text to assert is absent)")
		}
		return mkStep("expect_no_text", map[string]any{"text": n.Args[0].String}), nil
	case "expect-cell":
		if len(n.Args) != 2 || n.Args[0].Kind != ast.ValueIdent {
			return ir.Step{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
				"`expect-cell` takes <cell-name> <expected-value>")
		}
		val, err := stepExpectedValue(n.Args[1])
		if err != nil {
			return ir.Step{}, err
		}
		return mkStep("expect_cell", map[string]any{
			"name":  n.Args[0].String,
			"value": val,
		}), nil
	case "expect-count":
		// Observe-floor cardinality assertion: how many elements match a
		// selector. The foreign-target analog of `expect-cell <name> <n>`
		// — works against any frontend, Sigil or not.
		if len(n.Args) != 2 || n.Args[0].Kind != ast.ValueString || n.Args[1].Kind != ast.ValueInt {
			return ir.Step{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
				"`expect-count` takes `\"<selector>\" <count>` (e.g. `expect-count \".agent\" 3`)")
		}
		return mkStep("expect_count", map[string]any{
			"sel":   n.Args[0].String,
			"count": int(n.Args[1].Int),
		}), nil
	case "expect-path":
		if len(n.Args) != 1 || n.Args[0].Kind != ast.ValueString {
			return ir.Step{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
				"`expect-path` takes one string argument (the URL path to assert, e.g. \"/welcome\")")
		}
		return mkStep("expect_path", map[string]any{"path": n.Args[0].String}), nil
	case "match":
		// Branch on observed page state:
		//   match text-of "<selector>"
		//     | "value"
		//       <assertions>
		// Reuses the match_arm parse shape (here arms are string literals,
		// not union variants). Arms hold their own nested steps — the seed
		// of the scenario-IR generalization (a step contains steps).
		if len(n.Args) != 2 || n.Args[0].Kind != ast.ValueIdent || n.Args[0].String != "text-of" ||
			n.Args[1].Kind != ast.ValueString {
			return ir.Step{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
				"`match` takes `text-of \"<selector>\"` with `| \"value\"` arms")
		}
		if len(n.Children) == 0 {
			return ir.Step{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
				"`match` needs at least one `| \"value\"` arm")
		}
		arms := make([]ir.StepArm, 0, len(n.Children))
		seen := map[string]bool{}
		for _, armNode := range n.Children {
			if armNode == nil || armNode.Kind == "__error__" {
				continue
			}
			if armNode.Kind != "match_arm" || len(armNode.Args) != 1 || armNode.Args[0].Kind != ast.ValueString {
				return ir.Step{}, diag.New("lower", armNode.Pos.Line, armNode.Pos.Col,
					"scenario match arms are string literals: `| \"value\"`")
			}
			lit := armNode.Args[0].String
			if seen[lit] {
				return ir.Step{}, diag.New("lower", armNode.Pos.Line, armNode.Pos.Col,
					fmt.Sprintf("duplicate match arm %q", lit))
			}
			seen[lit] = true
			armSteps := make([]ir.Step, 0, len(armNode.Children))
			for _, sn := range armNode.Children {
				if sn == nil || sn.Kind == "__error__" {
					continue
				}
				s, err := lowerStep(sn)
				if err != nil {
					return ir.Step{}, err
				}
				if !armAssertable[s.Kind] {
					return ir.Step{}, diag.New("lower", sn.Pos.Line, sn.Pos.Col,
						fmt.Sprintf("match arms support assertions only (expect-text / expect-no-text / expect-count / expect-path); got %q", sn.Kind))
				}
				armSteps = append(armSteps, s)
			}
			if len(armSteps) == 0 {
				return ir.Step{}, diag.New("lower", armNode.Pos.Line, armNode.Pos.Col,
					fmt.Sprintf("match arm %q has no steps", lit))
			}
			arms = append(arms, ir.StepArm{Match: lit, Steps: armSteps})
		}
		st := mkStep("match", map[string]any{"source": "text-of", "sel": n.Args[1].String})
		st.Arms = arms
		return st, nil
	case "extract":
		// `extract text-of "<selector>" as <name>` captures a value from
		// the page into a binding that later steps interpolate as
		// ${<name>}. The binding lives runner-side (Go), which is what lets
		// it survive a full-page navigation. `text-of` is the v0 source;
		// `attr <a> of` / `path` follow when a challenge needs them.
		if len(n.Args) != 4 ||
			n.Args[0].Kind != ast.ValueIdent || n.Args[0].String != "text-of" ||
			n.Args[1].Kind != ast.ValueString ||
			n.Args[2].Kind != ast.ValueIdent || n.Args[2].String != "as" ||
			n.Args[3].Kind != ast.ValueIdent {
			return ir.Step{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
				"`extract` takes `text-of \"<selector>\" as <name>`")
		}
		return mkStep("extract", map[string]any{
			"source": "text-of",
			"sel":    n.Args[1].String,
			"as":     n.Args[3].String,
		}), nil
	case "wait":
		if len(n.Args) != 1 || n.Args[0].Kind != ast.ValueInt {
			return ir.Step{}, diag.New("lower", n.Pos.Line, n.Pos.Col,
				"`wait` takes one integer (milliseconds)")
		}
		return mkStep("wait", map[string]any{"ms": int(n.Args[0].Int)}), nil
	case "wait-for":
		role, name, err := readRoleAndName(n, "wait-for")
		if err != nil {
			return ir.Step{}, err
		}
		return mkStep("wait_for", map[string]any{"role": role, "name": name}), nil
	default:
		d := diag.New("lower", n.Pos.Line, n.Pos.Col,
			fmt.Sprintf("unknown test step %q", n.Kind))
		if hint := suggestFromSet(n.Kind, testStepVerbs); hint != "" {
			d.Suggestion = "did you mean " + hint + "?"
		}
		return ir.Step{}, d
	}
}

// stepExpectedValue resolves the "expected value" arg of expect-cell to
// a Go-typed value. Accepts int / string literals, plus the bare-ident
// bools `true` / `false`. Idents that aren't bools error — expectations
// are concrete values, not cell-ref redirection.
func stepExpectedValue(v ast.Value) (any, error) {
	switch v.Kind {
	case ast.ValueInt:
		return v.Int, nil
	case ast.ValueString:
		return v.String, nil
	case ast.ValueIdent:
		switch v.String {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	return nil, diag.New("lower", v.Pos.Line, v.Pos.Col,
		"expected value must be a literal (int / string / true / false)")
}

// readRoleAndName parses the common `<role-ident> <name-string>` shape
// shared by click and wait-for.
func readRoleAndName(n *ast.Node, verb string) (string, string, error) {
	if len(n.Args) != 2 ||
		n.Args[0].Kind != ast.ValueIdent ||
		n.Args[1].Kind != ast.ValueString {
		return "", "", diag.New("lower", n.Pos.Line, n.Pos.Col,
			fmt.Sprintf("`%s` takes <role> <name-string> (e.g. `%s button \"+\"`)", verb, verb))
	}
	return n.Args[0].String, n.Args[1].String, nil
}

// --- Module root + user-defined components ---

// stdlibKinds is the set of kinds a user-defined component name is not
// allowed to shadow. Keeps the shadowing diagnostic precise — a user
// can't replace `card` or redefine `view` from a file.
var stdlibKinds = []string{
	"card", "stack", "title", "text", "button", "input",
	"if", "for", "view", "component", "state",
}

// classifyModule sweeps a `module` root, registers any `component` decls,
// and returns the single `view` child to lower. Errors are recorded into
// l.diags; classifyModule returns nil when it found no view to lower at
// all (caller still gets the diagnostics back via l.diags.Err()).
func (l *lowerer) classifyModule(mod *ast.Node) *ast.Node {
	var view *ast.Node
	for _, c := range mod.Children {
		switch c.Kind {
		case "__error__":
			continue
		case "component":
			l.registerComponent(c)
		case "flow":
			l.registerFlow(c)
		case "theme":
			l.registerTheme(c)
		case "test":
			l.testNodes = append(l.testNodes, c)
		case "story":
			l.storyNodes = append(l.storyNodes, c)
		case "type":
			l.typeNodes = append(l.typeNodes, c)
		case "app":
			l.appNodes = append(l.appNodes, c)
		case "query", "command", "stream":
			l.opNodes = append(l.opNodes, c)
		case "icons":
			l.iconNodes = append(l.iconNodes, c)
		case "fonts":
			l.fontNodes = append(l.fontNodes, c)
		case "backend":
			l.backendNodes = append(l.backendNodes, c)
		case "session":
			l.sessionNodes = append(l.sessionNodes, c)
		case "view":
			if view != nil {
				l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
					"only one `view` is allowed per file"))
				continue
			}
			view = c
		default:
			l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
				fmt.Sprintf("expected `component`, `theme`, `view`, `app`, `test`, `story`, `type`, `query`, `command`, `icons`, `fonts`, `backend`, or `session` at top level, got %q", c.Kind)))
		}
	}
	if view == nil {
		// A file with no view is OK if it carries apps, tests, types,
		// or queries/commands — e.g. a tests-only file declaring an
		// external app target. Only error when the file contributes
		// nothing.
		if len(l.appNodes) == 0 && len(l.testNodes) == 0 &&
			len(l.storyNodes) == 0 &&
			len(l.typeNodes) == 0 && len(l.opNodes) == 0 &&
			len(l.iconNodes) == 0 && len(l.backendNodes) == 0 &&
			len(l.sessionNodes) == 0 && len(l.components) == 0 &&
			len(l.flows) == 0 && len(l.themes) == 0 {
			if l.diags.Empty() {
				l.diags.Add(diag.New("lower", mod.Pos.Line, mod.Pos.Col,
					"file has no declarations"))
			}
		}
		return nil
	}
	return view
}

// lowerStories compiles each `story` decl into its own standalone
// document. A story body is an anonymous view body: it gets the full
// compile checking a view gets (component arity, kwargs, handlers,
// state rules) by construction, because each story re-lowers a
// synthetic module — the shared decls a body may reference
// (components, themes, types, ops, icons, backends, sessions) plus a
// synthetic view wrapping the story's children. Isolation falls out:
// every story owns its cell namespace and, downstream, its own bundle,
// which is what lets `sigil stories` serve each one untangled from the
// main view and from its siblings.
func (l *lowerer) lowerStories(mod *ast.Node) []ir.Story {
	shared := make([]*ast.Node, 0, len(mod.Children))
	for _, c := range mod.Children {
		switch c.Kind {
		case "view", "test", "story", "app", "__error__":
			// The main view would collide with the synthetic one; tests,
			// apps, and the stories themselves are inert metadata for a
			// story document (and excluding stories bounds the recursion).
		default:
			shared = append(shared, c)
		}
	}
	out := make([]ir.Story, 0, len(l.storyNodes))
	seen := map[string]bool{}
	for _, sn := range l.storyNodes {
		if len(sn.Args) == 0 || sn.Args[0].Kind != ast.ValueString || sn.Args[0].String == "" {
			l.diags.Add(diag.New("lower", sn.Pos.Line, sn.Pos.Col,
				"story needs a quoted name"))
			continue
		}
		name := sn.Args[0].String
		if seen[name] {
			l.diags.Add(diag.New("lower", sn.Args[0].Pos.Line, sn.Args[0].Pos.Col,
				fmt.Sprintf("story %q already declared", name)))
			continue
		}
		seen[name] = true

		body := make([]*ast.Node, 0, len(sn.Children))
		for _, c := range sn.Children {
			if c.Kind != "__error__" {
				body = append(body, c)
			}
		}
		if len(body) == 0 {
			l.diags.Add(diag.New("lower", sn.Pos.Line, sn.Pos.Col,
				fmt.Sprintf("story %q has no body", name)))
			continue
		}

		view := &ast.Node{
			Kind:     "view",
			Pos:      sn.Pos,
			Args:     []ast.Value{{Kind: ast.ValueIdent, String: "Story", Pos: sn.Pos}},
			Children: body,
		}
		synth := &ast.Node{
			Kind:     "module",
			Pos:      mod.Pos,
			Children: append(append([]*ast.Node{}, shared...), view),
		}
		doc, err := Lower(synth)
		if err != nil {
			// Body nodes are shared with the original AST, so positions in
			// these diagnostics already point at the story's source lines.
			l.mergeDiags(err)
			continue
		}
		doc.Name = name
		out = append(out, ir.Story{Name: name, Doc: doc, Line: sn.Pos.Line, Col: sn.Pos.Col})
	}
	return out
}

// mergeDiags folds a (possibly multi-) diagnostic error from a nested
// Lower call into this lowerer's collector, preserving each item.
func (l *lowerer) mergeDiags(err error) {
	var multi *diag.MultiError
	if errors.As(err, &multi) {
		for _, d := range multi.Items {
			l.diags.Add(d)
		}
		return
	}
	l.diags.AddErr(err)
}

// registerComponent validates a `component` decl and records it for
// later inlining. All validation errors are accumulated; we still record
// the definition (best-effort) so its later call sites get a coherent
// "unknown component" rather than spurious noise.
func (l *lowerer) registerComponent(c *ast.Node) {
	if len(c.Args) == 0 || c.Args[0].Kind != ast.ValueIdent {
		l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
			"component declaration missing name"))
		return
	}
	name := c.Args[0].String
	// Shadowing check: refuse to redefine a stdlib kind.
	for _, k := range stdlibKinds {
		if name == k {
			d := diag.New("lower", c.Args[0].Pos.Line, c.Args[0].Pos.Col,
				fmt.Sprintf("component name %q shadows a stdlib kind", name))
			if hint := suggestFromSet(name, stdlibKinds); hint != "" {
				d.Suggestion = "rename it (not " + hint + ")"
			}
			l.diags.Add(d)
			return
		}
	}
	if _, dup := l.components[name]; dup {
		l.diags.Add(diag.New("lower", c.Args[0].Pos.Line, c.Args[0].Pos.Col,
			fmt.Sprintf("component %q already declared", name)))
		return
	}

	// Params start at Args[1:]; Args[0] is the component name.
	params := make([]param, 0, len(c.Args)-1)
	seen := map[string]bool{}
	for i := 1; i < len(c.Args); i++ {
		pv := c.Args[i]
		if seen[pv.String] {
			l.diags.Add(diag.New("lower", pv.Pos.Line, pv.Pos.Col,
				fmt.Sprintf("duplicate parameter %q in component %q", pv.String, name)))
			return
		}
		seen[pv.String] = true
		if pv.Variadic {
			// Must be the last param.
			if i != len(c.Args)-1 {
				l.diags.Add(diag.New("lower", pv.Pos.Line, pv.Pos.Col,
					fmt.Sprintf("variadic param *%s must be the last parameter", pv.String)))
				return
			}
		}
		params = append(params, param{name: pv.String, variadic: pv.Variadic})
	}

	// Filter out __error__ body lines but otherwise leave the body
	// shape intact for substitution-time traversal.
	body := make([]*ast.Node, 0, len(c.Children))
	for _, child := range c.Children {
		if child.Kind == "__error__" {
			continue
		}
		body = append(body, child)
	}
	if len(body) == 0 {
		l.diags.Add(diag.New("lower", c.Pos.Line, c.Pos.Col,
			fmt.Sprintf("component %q has no body", name)))
		return
	}

	def := &componentDef{
		name:   name,
		params: params,
		body:   body,
		pos:    c.Pos,
	}
	l.components[name] = def
	l.compOrder = append(l.compOrder, name)

	// Body shape validation: no `state`, no foreign `*splice`. Walk the
	// body's children and recurse — these are static checks against the
	// AST shape, before any substitution.
	validParams := map[string]bool{}
	variadicParam := ""
	for _, p := range params {
		validParams[p.name] = true
		if p.variadic {
			variadicParam = p.name
		}
	}
	for _, n := range body {
		l.validateComponentBody(n, name, variadicParam)
	}
}

// validateComponentBody walks a component body subtree and reports any
// disallowed shapes: nested `state`, `splice` referencing something other
// than the variadic param, and `*name` splice when there is no variadic.
func (l *lowerer) validateComponentBody(n *ast.Node, compName, variadicParam string) {
	if n == nil || n.Kind == "__error__" {
		return
	}
	switch n.Kind {
	case "state":
		l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
			fmt.Sprintf("component %q cannot declare state (components are pure in v0)", compName)))
	case "splice":
		var refName string
		if len(n.Args) > 0 {
			refName = n.Args[0].String
		}
		if variadicParam == "" || refName != variadicParam {
			l.diags.Add(diag.New("lower", n.Pos.Line, n.Pos.Col,
				fmt.Sprintf("`*%s` in component %q must reference the variadic param", refName, compName)))
		}
	}
	for _, c := range n.Children {
		l.validateComponentBody(c, compName, variadicParam)
	}
	for _, h := range n.Handlers {
		l.validateComponentBody(h, compName, variadicParam)
	}
}

// substValue is the resolved binding for one parameter at a call site.
// Exactly one of cell / literal / children is meaningful per instance.
type substValue struct {
	cell     string      // non-empty when the arg is a cell reference
	literal  *ast.Value  // non-nil when the arg is a string/int literal
	children []*ast.Node // non-nil when the param is variadic
	variadic bool        // marks variadic bindings even when children is empty
}

// lowerUserComponent dispatches a call to a user-defined component:
// bind args to params, deep-substitute the body, then lower the
// substituted body in place at the call site's path.
func (l *lowerer) lowerUserComponent(call *ast.Node, def *componentDef, path string) (ir.Node, error) {
	if l.inlining[def.name] {
		return ir.Node{}, diag.New("lower", call.Pos.Line, call.Pos.Col,
			fmt.Sprintf("recursive component invocation: %q", def.name))
	}
	if len(call.Kwargs) > 0 {
		return ir.Node{}, diag.New("lower", call.Pos.Line, call.Pos.Col,
			fmt.Sprintf("component %q does not accept keyword args (v0)", def.name))
	}
	if len(call.Handlers) > 0 {
		return ir.Node{}, diag.New("lower", call.Pos.Line, call.Pos.Col,
			fmt.Sprintf("component %q does not accept event handlers (v0)", def.name))
	}

	// Build the substitution table. Positional args bind to non-variadic
	// params in order; the (optional) variadic param captures the
	// invocation's indented children.
	numPositional := 0
	hasVariadic := false
	for _, p := range def.params {
		if p.variadic {
			hasVariadic = true
		} else {
			numPositional++
		}
	}
	if len(call.Args) < numPositional {
		return ir.Node{}, diag.New("lower", call.Pos.Line, call.Pos.Col,
			fmt.Sprintf("component %q expects %d arg(s), got %d", def.name, numPositional, len(call.Args)))
	}
	if !hasVariadic && len(call.Args) > numPositional {
		return ir.Node{}, diag.New("lower", call.Pos.Line, call.Pos.Col,
			fmt.Sprintf("component %q expects %d arg(s), got %d", def.name, numPositional, len(call.Args)))
	}
	if hasVariadic && len(call.Args) > numPositional {
		// Trailing positional args beyond declared params are not bound
		// to the variadic — the variadic captures children, not args.
		// Tell the author explicitly.
		return ir.Node{}, diag.New("lower", call.Args[numPositional].Pos.Line, call.Args[numPositional].Pos.Col,
			fmt.Sprintf("component %q expects %d positional arg(s); extra args are not bound to the variadic (children are)", def.name, numPositional))
	}

	subs := map[string]substValue{}
	argIdx := 0
	for _, p := range def.params {
		if p.variadic {
			// Filter __error__ children from the call site.
			kids := make([]*ast.Node, 0, len(call.Children))
			for _, c := range call.Children {
				if c.Kind == "__error__" {
					continue
				}
				kids = append(kids, c)
			}
			subs[p.name] = substValue{children: kids, variadic: true}
			continue
		}
		v := call.Args[argIdx]
		argIdx++
		switch v.Kind {
		case ast.ValueIdent:
			cellID, _, ok := l.lookupCell(v.String)
			if !ok {
				d := diag.New("lower", v.Pos.Line, v.Pos.Col,
					fmt.Sprintf("unknown name %q passed to component %q", v.String, def.name))
				if hint := l.suggestCellName(v.String); hint != "" {
					d.Suggestion = "did you mean " + hint + "?"
				}
				return ir.Node{}, d
			}
			// Bind to the source name (cellsByName key) — substitution
			// rewrites ident references and lookupCell resolves it at
			// the inlined use site.
			_ = cellID
			subs[p.name] = substValue{cell: v.String}
		case ast.ValueString, ast.ValueInt:
			lit := v
			subs[p.name] = substValue{literal: &lit}
		default:
			return ir.Node{}, diag.New("lower", v.Pos.Line, v.Pos.Col,
				fmt.Sprintf("unsupported arg kind for component %q", def.name))
		}
	}

	l.inlining[def.name] = true
	defer delete(l.inlining, def.name)

	// Substitute the body. Use the parent-loop pattern so splices expand
	// at their slot rather than being recursed-into as a single node.
	substituted := substChildren(def.body, subs)

	switch len(substituted) {
	case 0:
		return ir.Node{Kind: ir.KindFragment, ID: path}, nil
	case 1:
		return l.lowerNode(substituted[0], path)
	default:
		// Multi-line body inlined: wrap in a synthetic vertical stack
		// so the call-site slot remains a single IR node. Matches the
		// view-body multi-child convention.
		wrap := ir.Node{
			Kind:  ir.KindStack,
			ID:    path,
			Props: map[string]any{"axis": "vertical", "gap": 0},
		}
		for i, c := range substituted {
			wrap.Children = append(wrap.Children,
				l.lowerNodeCollect(c, joinPath(path, i)))
		}
		return wrap, nil
	}
}

// substChildren walks a parent's children list and produces the
// post-substitution list. Splice nodes expand to the variadic param's
// captured children; everything else recurses through substAST.
func substChildren(children []*ast.Node, subs map[string]substValue) []*ast.Node {
	out := make([]*ast.Node, 0, len(children))
	for _, c := range children {
		if c.Kind == "splice" {
			if len(c.Args) == 0 {
				continue
			}
			name := c.Args[0].String
			sub, ok := subs[name]
			if !ok || !sub.variadic {
				// Not a variadic — should've been caught during
				// registration. Drop silently to avoid double-error.
				continue
			}
			for _, sc := range sub.children {
				out = append(out, substAST(sc, subs))
			}
			continue
		}
		out = append(out, substAST(c, subs))
	}
	return out
}

// substAST returns a deep-cloned copy of n with parameter references
// rewritten according to subs. Splice expansion is the parent loop's
// job; this function returns exactly one node per call.
func substAST(n *ast.Node, subs map[string]substValue) *ast.Node {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case "ref":
		if len(n.Args) == 1 && n.Args[0].Kind == ast.ValueIdent {
			if sub, ok := subs[n.Args[0].String]; ok {
				return substRef(n, sub)
			}
		}
	case "splice":
		// Splice should always be handled by substChildren; arriving
		// here means it was used in an unexpected position (e.g. as a
		// handler body). Return a fragment-like placeholder; the
		// lower-time validator already diagnosed it.
		return cloneNode(n, subs)
	case "for":
		// The loop variable shadows any outer param of the same name
		// inside the for-body. Snapshot subs, drop the loop var, then
		// recurse — and restore on the way back out. Mirrors the
		// runtime-side save/restore in lowerFor.
		out := &ast.Node{
			Kind: n.Kind,
			Pos:  n.Pos,
			Args: cloneArgs(n.Args, subs),
		}
		if len(n.Args) >= 1 && n.Args[0].Kind == ast.ValueIdent {
			// `for <loopVar> in <list>` — the loop var name should NOT
			// be substituted even if it happens to match a param.
			// cloneArgs treated args generically — overwrite arg 0 with
			// the original loop-var name to undo any substitution.
			out.Args[0] = n.Args[0]
		}
		loopVar := ""
		if len(n.Args) >= 1 {
			loopVar = n.Args[0].String
		}
		childSubs := subs
		if loopVar != "" {
			if _, shadow := subs[loopVar]; shadow {
				childSubs = make(map[string]substValue, len(subs))
				for k, v := range subs {
					if k != loopVar {
						childSubs[k] = v
					}
				}
			}
		}
		out.Children = substChildren(n.Children, childSubs)
		out.Handlers = substHandlers(n.Handlers, childSubs)
		out.Kwargs = cloneKwargs(n.Kwargs, childSubs)
		return out
	case "method_call":
		// Args[0] = receiver (a cell name), Args[1] = method name.
		// Substitute the receiver but never the method name.
		out := &ast.Node{Kind: n.Kind, Pos: n.Pos}
		if len(n.Args) >= 1 {
			out.Args = append(out.Args, substIdentArg(n.Args[0], subs))
		}
		if len(n.Args) >= 2 {
			out.Args = append(out.Args, n.Args[1])
		}
		out.Children = substChildren(n.Children, subs)
		out.Handlers = substHandlers(n.Handlers, subs)
		out.Kwargs = cloneKwargs(n.Kwargs, subs)
		return out
	case "assign":
		// Args[0] is the LHS cell name. Rewrite under the same rule as a
		// bare ident. Critically, the binop pattern `cell = cell ± lit`
		// stays consistent because both come from the same subs[param]
		// — see lowerHandler's pattern matcher.
		out := &ast.Node{Kind: n.Kind, Pos: n.Pos}
		if len(n.Args) >= 1 {
			out.Args = append(out.Args, substIdentArg(n.Args[0], subs))
		}
		for i := 1; i < len(n.Args); i++ {
			out.Args = append(out.Args, n.Args[i])
		}
		out.Children = substChildren(n.Children, subs)
		return out
	}

	return cloneNode(n, subs)
}

// substRef rewrites a `ref(name)` node where name was a parameter.
//   - cell binding → ref(cellName)
//   - literal binding → lit(value)
//   - variadic → error (cannot use a children-set as a value).
func substRef(n *ast.Node, sub substValue) *ast.Node {
	pos := n.Args[0].Pos
	switch {
	case sub.variadic:
		// Surface as a synthetic __error__ node with a diagnostic in Pos —
		// but we don't have access to l here. Easiest: emit a `ref` with
		// a sentinel name that lookupCell will fail on, producing a
		// readable error. Better: leave it as ref(*name) so the parent
		// loop returns the "unknown name" error at use time. We pick the
		// sentinel form so the message names the param.
		return &ast.Node{
			Kind: "ref",
			Pos:  pos,
			Args: []ast.Value{{Kind: ast.ValueIdent, String: "*" + n.Args[0].String, Pos: pos}},
		}
	case sub.cell != "":
		return &ast.Node{
			Kind: "ref",
			Pos:  pos,
			Args: []ast.Value{{Kind: ast.ValueIdent, String: sub.cell, Pos: pos}},
		}
	case sub.literal != nil:
		v := *sub.literal
		v.Pos = pos
		return &ast.Node{
			Kind: "lit",
			Pos:  pos,
			Args: []ast.Value{v},
		}
	}
	return n
}

// substIdentArg rewrites a single Value that holds an ident which may be
// a parameter. Strings get interpolation-rewriting; ints are passed
// through unchanged.
func substIdentArg(v ast.Value, subs map[string]substValue) ast.Value {
	if v.Kind == ast.ValueIdent {
		if sub, ok := subs[v.String]; ok {
			switch {
			case sub.cell != "":
				return ast.Value{Kind: ast.ValueIdent, String: sub.cell, Pos: v.Pos}
			case sub.literal != nil:
				out := *sub.literal
				out.Pos = v.Pos
				return out
			case sub.variadic:
				// Can't bind a children-set into a value position; leave
				// it for lookupCell to error on with a recognizable name.
				return ast.Value{Kind: ast.ValueIdent, String: "*" + v.String, Pos: v.Pos}
			}
		}
		return v
	}
	if v.Kind == ast.ValueString {
		return substInterpValue(v, subs)
	}
	return v
}

// substInterpValue rewrites `${param}` interpolations inside a string
// value. Cell-bound params stay as `${cellName}`; literal-bound params
// get spliced in directly so applyTextString sees zero `${...}` markers
// and takes the static path.
func substInterpValue(v ast.Value, subs map[string]substValue) ast.Value {
	if v.Kind != ast.ValueString {
		return v
	}
	parts, err := parseInterp(v.String, v.Pos)
	if err != nil {
		// Malformed interpolation will re-error at lower time anyway —
		// pass through unchanged so we don't double-diagnose.
		return v
	}
	hit := false
	for i, p := range parts {
		if !p.isRef {
			continue
		}
		sub, ok := subs[p.text]
		if !ok {
			continue
		}
		hit = true
		switch {
		case sub.cell != "":
			parts[i] = interpPart{isRef: true, text: sub.cell}
		case sub.literal != nil:
			parts[i] = interpPart{isRef: false, text: literalAsString(*sub.literal)}
		case sub.variadic:
			// Variadic in interpolation is a usage error — leave name
			// alone so applyTextString reports "unknown name".
			parts[i] = interpPart{isRef: true, text: "*" + p.text}
		}
	}
	if !hit {
		return v
	}
	var b strings.Builder
	for _, p := range parts {
		if p.isRef {
			b.WriteString("${")
			b.WriteString(p.text)
			b.WriteString("}")
		} else {
			b.WriteString(p.text)
		}
	}
	out := v
	out.String = b.String()
	return out
}

// literalAsString renders a literal arg for splicing into an interpolated
// string. Strings go in raw (no extra quoting); ints render base-10.
func literalAsString(v ast.Value) string {
	switch v.Kind {
	case ast.ValueString:
		return v.String
	case ast.ValueInt:
		return strconv.FormatInt(v.Int, 10)
	}
	return ""
}

// cloneNode performs a shape-preserving deep clone that runs each ident
// arg through the substitution rules. Used for kinds that don't need
// special-case handling (everything except `ref` / `splice` / `for` /
// `method_call` / `assign`).
func cloneNode(n *ast.Node, subs map[string]substValue) *ast.Node {
	out := &ast.Node{
		Kind:     n.Kind,
		Pos:      n.Pos,
		Args:     cloneArgs(n.Args, subs),
		Kwargs:   cloneKwargs(n.Kwargs, subs),
		Handlers: substHandlers(n.Handlers, subs),
	}
	out.Children = substChildren(n.Children, subs)
	return out
}

func cloneArgs(args []ast.Value, subs map[string]substValue) []ast.Value {
	if len(args) == 0 {
		return nil
	}
	out := make([]ast.Value, 0, len(args))
	for _, a := range args {
		out = append(out, substIdentArg(a, subs))
	}
	return out
}

func cloneKwargs(kw map[string]ast.Value, subs map[string]substValue) map[string]ast.Value {
	if len(kw) == 0 {
		return nil
	}
	out := make(map[string]ast.Value, len(kw))
	for k, v := range kw {
		out[k] = substIdentArg(v, subs)
	}
	return out
}

func substHandlers(h map[string]*ast.Node, subs map[string]substValue) map[string]*ast.Node {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]*ast.Node, len(h))
	for k, v := range h {
		out[k] = substAST(v, subs)
	}
	return out
}

// registerTheme converts a source-level `theme` decl into a
// pkg/theme.Theme and appends it to the lowerer's collection. Validates
// the name vocabulary (against IntentTones), parses colors (via the same
// hex parser the contrast validator uses), and runs Validate so a theme
// that fails WCAG AA never reaches the renderer.
func (l *lowerer) registerTheme(t *ast.Node) {
	if len(t.Args) == 0 || t.Args[0].Kind != ast.ValueIdent {
		l.diags.Add(diag.New("lower", t.Pos.Line, t.Pos.Col,
			"theme declaration missing name"))
		return
	}
	name := t.Args[0].String

	// Resolve the `extends` base. Default is Light — a theme that only
	// overrides primary still gets sensible spacing/radii/text scale.
	base := theme.Light
	baseName := "light"
	if extendsArg, ok := t.Kwargs["extends"]; ok {
		switch extendsArg.String {
		case "light":
			base = theme.Light
			baseName = "light"
		case "dark":
			base = theme.Dark
			baseName = "dark"
		default:
			l.diags.Add(diag.New("lower", extendsArg.Pos.Line, extendsArg.Pos.Col,
				fmt.Sprintf("unknown base theme %q (expected `light` or `dark`)", extendsArg.String)))
			return
		}
	}

	delta := theme.Theme{
		Name:  name,
		Tones: map[string]theme.ColorPair{},
	}
	for _, child := range t.Children {
		if child.Kind == "__error__" {
			continue
		}
		if child.Kind == "text_binding" {
			l.registerThemeText(&delta, base, child)
			continue
		}
		if child.Kind != "tone_binding" || len(child.Args) < 2 {
			l.diags.Add(diag.New("lower", child.Pos.Line, child.Pos.Col,
				"theme body lines must be `tone = \"#bg\" on \"#fg\"`, `outline/muted = \"#hex\"`, or `text <token> = …`"))
			continue
		}
		toneName := child.Args[0].String

		// Single-color form: the standalone neutrals. `outline` paints
		// every border/divider; `muted` every secondary caption — the
		// pair that makes an app read tinted instead of stock zinc.
		if toneName == "outline" || toneName == "muted" {
			if len(child.Args) != 2 {
				l.diags.Add(diag.New("lower", child.Args[0].Pos.Line, child.Args[0].Pos.Col,
					fmt.Sprintf("%s takes a single color (no `on` pair): %s = \"#hex\"", toneName, toneName)))
				continue
			}
			color := child.Args[1].String
			// Parse-validate the hex now; contrast for muted is gated
			// against the composed theme after Extends below.
			if _, err := theme.ContrastRatio(color, "#ffffff"); err != nil {
				l.diags.Add(diag.New("lower", child.Args[1].Pos.Line, child.Args[1].Pos.Col,
					fmt.Sprintf("theme %q %s: %v", name, toneName, err)))
				continue
			}
			if toneName == "outline" {
				delta.Outline = color
			} else {
				delta.Muted = color
			}
			continue
		}
		if len(child.Args) != 3 {
			l.diags.Add(diag.New("lower", child.Pos.Line, child.Pos.Col,
				fmt.Sprintf("tone %q needs a pair: %s = \"#bg\" on \"#fg\"", toneName, toneName)))
			continue
		}
		valid := false
		for _, t := range theme.IntentTones {
			if t == toneName && toneName != "default" && toneName != "muted" {
				valid = true
				break
			}
		}
		if !valid {
			d := diag.New("lower", child.Args[0].Pos.Line, child.Args[0].Pos.Col,
				fmt.Sprintf("unknown or non-overridable tone %q", toneName))
			if hint := suggestFromSet(toneName, theme.IntentTones); hint != "" {
				d.Suggestion = "did you mean " + hint + "?"
			}
			l.diags.Add(d)
			continue
		}
		delta.Tones[toneName] = theme.ColorPair{
			BG: child.Args[1].String,
			FG: child.Args[2].String,
		}
	}

	final := delta.Extends(base)
	final.Name = name
	final.ExtendsName = baseName
	if err := final.Validate(); err != nil {
		l.diags.Add(diag.New("lower", t.Pos.Line, t.Pos.Col, err.Error()))
		return
	}
	// Muted is caption TEXT, so it carries the same AA bar as tone
	// pairs — checked against the composed theme's surface and page
	// backgrounds (the two surfaces captions actually sit on).
	if delta.Muted != "" {
		for _, bgTone := range []string{"surface", "page"} {
			bg := final.Tones[bgTone].BG
			ratio, err := theme.ContrastRatio(final.Muted, bg)
			if err == nil && ratio < 4.5 {
				l.diags.Add(diag.New("lower", t.Pos.Line, t.Pos.Col,
					fmt.Sprintf("theme %q muted: contrast %.2f:1 against the %s background (%s) is below WCAG AA (4.5:1)",
						name, ratio, bgTone, bg)))
				return
			}
		}
	}
	l.themes = append(l.themes, &final)
}

// registerThemeText folds one `text <token> = …` theme body line into
// the delta's TextScale. An existing token (body, heading-md, …) is
// overridden field-by-field starting from the base entry; a new token
// (wordmark, mono, …) starts from body-equivalent defaults so a
// family-only declaration doesn't produce a zero-size style. Newly
// declared tokens become valid `size=` values on text/badge.
func (l *lowerer) registerThemeText(delta *theme.Theme, base theme.Theme, child *ast.Node) {
	if len(child.Args) < 2 {
		l.diags.Add(diag.New("lower", child.Pos.Line, child.Pos.Col,
			"text binding needs a token and at least one value"))
		return
	}
	token := child.Args[0].String
	ts, ok := base.TextScale[token]
	if !ok {
		ts = theme.TextStyle{Size: 14, Weight: 400}
	}
	ints := 0
	pendingTracking := false
	for _, v := range child.Args[1:] {
		switch v.Kind {
		case ast.ValueString:
			ts.Family = v.String
		case ast.ValueIdent:
			// Parser admits italic / caps / tracking here.
			switch v.String {
			case "italic":
				ts.Italic = true
			case "caps":
				ts.Caps = true
			case "tracking":
				pendingTracking = true
			}
		case ast.ValueInt:
			if pendingTracking {
				ts.Tracking = int(v.Int)
				pendingTracking = false
				continue
			}
			switch ints {
			case 0:
				ts.Size = int(v.Int)
			case 1:
				ts.Weight = int(v.Int)
			default:
				l.diags.Add(diag.New("lower", v.Pos.Line, v.Pos.Col,
					"text binding takes at most two bare integers (size, then weight)"))
				return
			}
			ints++
		}
	}
	if pendingTracking {
		l.diags.Add(diag.New("lower", child.Pos.Line, child.Pos.Col,
			fmt.Sprintf("text %q: `tracking` needs an integer (1/100 em, e.g. tracking 10 = 0.1em)", token)))
		return
	}
	if ts.Size <= 0 || ts.Size > 200 {
		l.diags.Add(diag.New("lower", child.Pos.Line, child.Pos.Col,
			fmt.Sprintf("text %q: size %d out of range (1–200)", token, ts.Size)))
		return
	}
	if ts.Weight < 100 || ts.Weight > 900 {
		l.diags.Add(diag.New("lower", child.Pos.Line, child.Pos.Col,
			fmt.Sprintf("text %q: weight %d out of range (100–900)", token, ts.Weight)))
		return
	}
	if ts.Tracking < 0 || ts.Tracking > 100 {
		l.diags.Add(diag.New("lower", child.Pos.Line, child.Pos.Col,
			fmt.Sprintf("text %q: tracking %d out of range (0–100, in 1/100 em)", token, ts.Tracking)))
		return
	}
	if delta.TextScale == nil {
		delta.TextScale = map[string]theme.TextStyle{}
	}
	delta.TextScale[token] = ts
	l.themeTextTokens[token] = true
}

// suggestKindOrComponent merges stdlib kinds with declared user
// components for "did you mean" hints on unknown invocations.
func (l *lowerer) suggestKindOrComponent(bad string) string {
	cands := make([]string, 0, len(validKinds)+len(l.components)+1)
	for k := range validKinds {
		cands = append(cands, k)
	}
	for k := range l.components {
		cands = append(cands, k)
	}
	cands = append(cands, "for")
	return suggestFromSet(bad, cands)
}
