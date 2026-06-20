package html

import (
	"fmt"
	"sort"
	"strings"

	"github.com/incantery/sigil/pkg/ir"
	"github.com/incantery/sigil/pkg/style"
)

// Resolver translates style.Spec values into atomic CSS class names
// and accumulates the set of class rules that were actually used over
// the lifetime of a render. Tailwind's structural win, but the
// "atoms" are *semantic token references* (s-surface-accent,
// s-pad-md, s-elev-sm), not raw utility primitives — so a theme
// change rewrites class bodies in place without touching authored
// source.
//
// Also tracks the (icon-set, icon-name) pairs referenced during
// render so the document can hoist only-used SVG <symbol>s into a
// single <defs> block at the top of <body> — same atomic-dedupe
// idea applied to icon definitions instead of classes.
//
// Usage:
//
//	r := newResolver(doc.IconSets)
//	classes := r.classes(style.SpecFor(node))   // walk the tree
//	... include classes in HTML output ...
//	stylesheet := r.stylesheet()                 // CSS rules
//	defs       := r.iconDefs()                   // SVG <symbol>s
//
// The resolver is the *only* place in the HTML renderer that names a
// CSS unit. Every per-primitive style switch in html.go is gone —
// primitives.SpecFor → resolver.classes is the whole styling pipe.
type resolver struct {
	used      map[string]string                  // class name → CSS rule body
	iconSets  map[string]map[string]ir.IconAsset // set name → icon name → asset
	usedIcons map[string]bool                    // "set/name" key set
	iconOrder []iconRef                          // ordered for stable output
}

type iconRef struct{ Set, Name string }

func newResolver(sets []ir.IconSet) *resolver {
	idx := make(map[string]map[string]ir.IconAsset, len(sets))
	for _, s := range sets {
		idx[s.Name] = s.Icons
	}
	return &resolver{
		used:      map[string]string{},
		iconSets:  idx,
		usedIcons: map[string]bool{},
	}
}

// classes returns the atomic class names for a Spec, registering each
// one's rule body in r.used. An empty Spec returns nil. Order is
// stable per slot type for diffability.
func (r *resolver) classes(s style.Spec) []string {
	if s.IsZero() {
		return nil
	}
	var out []string
	// Glass replaces the opaque surface paint with the translucent
	// recipe over the same tone — exactly one of the two classes is
	// emitted, so there is no stylesheet-order fight between them.
	switch {
	case s.Glass:
		tone := s.Surface
		if tone == "" {
			tone = "surface"
		}
		out = append(out, r.glassClass(tone))
	case s.Surface != "":
		out = append(out, r.surfaceClass(s.Surface))
	}
	if s.Color != "" {
		out = append(out, r.colorClass(s.Color))
	}
	if s.Border != "" {
		out = append(out, r.borderClass(s.Border))
	}
	if s.Aura != "" {
		out = append(out, r.auraClass(s.Aura))
	}
	if s.Shadow != "" {
		out = append(out, r.shadowClass(s.Shadow))
	}
	if s.Padding != "" {
		v := spaceVal(s.Padding)
		out = append(out, r.simpleClass("pad", string(s.Padding),
			fmt.Sprintf("padding:%s;", v)))
	}
	if s.PadInline != "" {
		v := spaceVal(s.PadInline)
		out = append(out, r.simpleClass("padx", string(s.PadInline),
			fmt.Sprintf("padding-left:%s;padding-right:%s;", v, v)))
	}
	if s.PadBlock != "" {
		v := spaceVal(s.PadBlock)
		out = append(out, r.simpleClass("pady", string(s.PadBlock),
			fmt.Sprintf("padding-top:%s;padding-bottom:%s;", v, v)))
	}
	if s.Radius != "" {
		out = append(out, r.simpleClass("radius", string(s.Radius),
			fmt.Sprintf("border-radius:var(--radius-%s);", s.Radius)))
	}
	if s.Font != "" {
		out = append(out, r.simpleClass("font", string(s.Font),
			fmt.Sprintf("font-size:var(--text-%s-size);font-weight:var(--text-%s-weight);font-family:var(--text-%s-family);font-style:var(--text-%s-style);letter-spacing:var(--text-%s-tracking);text-transform:var(--text-%s-transform);",
				s.Font, s.Font, s.Font, s.Font, s.Font, s.Font)))
	}
	if s.Elevation != "" && s.Elevation != "flat" {
		out = append(out, r.simpleClass("elev", string(s.Elevation),
			fmt.Sprintf("box-shadow:var(--elevation-%s);", s.Elevation)))
	}
	if s.Width != "" {
		out = append(out, r.widthClass(s.Width))
	}
	if s.Direction != "" {
		out = append(out, r.dirClass(s.Direction))
	}
	if s.Align != "" {
		out = append(out, r.alignClass(s.Align))
	}
	if s.AlignSelf != "" {
		out = append(out, r.alignSelfClass(s.AlignSelf))
	}
	if s.Gap != 0 {
		out = append(out, r.gapClass(s.Gap))
	}
	if s.Shape == style.ShapePill {
		out = append(out, r.simpleClass("shape", "pill",
			"border-radius:var(--radius-full);display:inline-flex;"))
	}
	if s.Flex > 0 {
		out = append(out, r.simpleClass("flex", fmt.Sprintf("%d", s.Flex),
			fmt.Sprintf("flex:%d;min-width:0;min-height:0;", s.Flex)))
	}
	// Viewport heights use dynamic units (dvh) with a static fallback
	// declared first: on mobile Safari 100vh is the LARGEST viewport
	// (toolbar collapsed), so a vh-sized shell overflows the visible
	// viewport by the toolbar height whenever the toolbar is showing.
	// dvh tracks the toolbar live (iOS 15.4+ / Chrome 108+); engines
	// that don't know dvh drop that declaration and keep the vh line.
	if s.Height == "full" {
		out = append(out, r.simpleClass("h", "full", "min-height:100vh;min-height:100dvh;"))
	}
	if s.Height == "screen" {
		// Exactly one viewport tall, clipped — the app-shell shape. The
		// page itself never scrolls; interior regions opt into their own
		// scrolling with scroll=y (which is what lets flex=1 transcript
		// panes actually overflow instead of growing the page). Because
		// the shell owns the true bottom edge, it also absorbs the home
		// indicator's safe-area inset (the page meta declares
		// viewport-fit=cover); an explicit pady= atomic on the same
		// stack sorts later in the stylesheet and wins, so authored
		// padding overrides the automatic inset.
		out = append(out, r.simpleClass("h", "screen",
			"height:100vh;height:100dvh;overflow:hidden;padding-bottom:env(safe-area-inset-bottom, 0px);"))
	}
	if s.Scroll == "y" {
		out = append(out, r.simpleClass("scroll", "y", "overflow-y:auto;"))
	}
	if s.FixedWidth != "" {
		out = append(out, r.simpleClass("fw", s.FixedWidth,
			fmt.Sprintf("width:%s;flex-shrink:0;", s.FixedWidth)))
	}
	if s.FixedHeight != "" {
		out = append(out, r.simpleClass("fh", s.FixedHeight,
			fmt.Sprintf("height:%s;flex-shrink:0;", s.FixedHeight)))
	}
	if s.MaxWidth != "" {
		out = append(out, r.simpleClass("maxw", s.MaxWidth,
			fmt.Sprintf("max-width:%s;", s.MaxWidth)))
	}
	if s.Columns != "" {
		out = append(out, r.gridClass(s.Columns))
	}
	return out
}

// classAttr returns ` class="..."` if there are any classes, "" otherwise.
// The leading space is part of the attribute, so callers can drop it
// straight into an opening tag without conditionals.
func (r *resolver) classAttr(classes []string) string {
	if len(classes) == 0 {
		return ""
	}
	return fmt.Sprintf(` class=%q`, strings.Join(classes, " "))
}

// useIcon records that a (set, name) icon pair was referenced in the
// render. Returns the DOM id that the renderer's <use href="#..."/>
// should target. Each unique pair is emitted as one <symbol> in the
// shared <defs> block; multiple usages share one symbol — same
// dedupe pattern atomic classes use, applied to SVG.
func (r *resolver) useIcon(set, name string) (id string, ok bool) {
	if _, found := r.iconSets[set][name]; !found {
		return "", false
	}
	key := set + "/" + name
	if !r.usedIcons[key] {
		r.usedIcons[key] = true
		r.iconOrder = append(r.iconOrder, iconRef{Set: set, Name: name})
	}
	return "sigil-icon-" + set + "-" + name, true
}

// iconDefs returns the inline <svg><defs>…</defs></svg> block
// containing one <symbol> per used icon. Returns "" if no icons
// were referenced, so the renderer skips emission entirely.
func (r *resolver) iconDefs() string {
	if len(r.iconOrder) == 0 {
		return ""
	}
	var b strings.Builder
	// Hide the defs block via the hashed stylesheet (.s-icon-defs in
	// structural.css), NOT an inline style="" attribute: a hash-based
	// CSP's style-src authorizes the <style> element's content but not
	// inline style attributes, so an inline style here would be
	// blocked and the SVG symbols would render as a visible band.
	b.WriteString(`<svg class="s-icon-defs" aria-hidden="true"><defs>`)
	for _, ref := range r.iconOrder {
		asset := r.iconSets[ref.Set][ref.Name]
		fmt.Fprintf(&b, `<symbol id="sigil-icon-%s-%s" viewBox=%q>%s</symbol>`,
			ref.Set, ref.Name, asset.ViewBox, asset.Web)
	}
	b.WriteString(`</defs></svg>`)
	return b.String()
}

// stylesheet returns the CSS for every class that has been registered,
// sorted by class name so the output is stable and diff-friendly.
func (r *resolver) stylesheet() string {
	names := make([]string, 0, len(r.used))
	for name := range r.used {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, ".%s { %s }\n", name, r.used[name])
	}
	return b.String()
}

// --- per-slot class builders ---

func (r *resolver) surfaceClass(t style.ToneRef) string {
	tone := string(t)
	name := "s-surface-" + tone
	if _, ok := r.used[name]; !ok {
		// `muted` is a single standalone color, not a paired tone —
		// "surface=muted" cannot meaningfully paint a background. Fall
		// back to fg+border-color treatment so the slot still produces
		// a visible affordance without an inaccessible bg/fg pair.
		// (primitives.go avoids this path for cards/buttons by mapping
		// tone=muted to Color/Border directly — this branch is a safety
		// net for anything that slips through.)
		if tone == "muted" {
			r.used[name] = "color:var(--color-muted);border-color:var(--color-muted);"
		} else {
			r.used[name] = fmt.Sprintf("background:var(--color-%s-bg);color:var(--color-%s-fg);",
				tone, tone)
		}
	}
	return name
}

// glassClass is the translucent surface recipe: the tone's background
// at 75% over a 14px backdrop blur, foreground unchanged. Engines
// without color-mix/backdrop-filter degrade to an unpainted surface —
// content stays readable on whatever is behind it.
func (r *resolver) glassClass(t style.ToneRef) string {
	tone := string(t)
	name := "s-glass-" + tone
	if _, ok := r.used[name]; !ok {
		if tone == "muted" {
			// Same safety net as surfaceClass: muted has no bg/fg pair
			// to mix from.
			r.used[name] = "color:var(--color-muted);border-color:var(--color-muted);"
		} else {
			r.used[name] = fmt.Sprintf(
				"background:color-mix(in srgb, var(--color-%s-bg) 75%%, transparent);color:var(--color-%s-fg);backdrop-filter:blur(14px);-webkit-backdrop-filter:blur(14px);",
				tone, tone)
		}
	}
	return name
}

// auraClass paints the soft radial glow: a tight ellipse of the tone's
// background hovering above the top edge (the Aura mock's geometry —
// a 480×320 glow at top:-120px, i.e. radii 240×160 centered at y=40px,
// fully faded above the first message), as background-image so it
// composes with any surface/glass background-color underneath. The
// fixed pixel ellipse keeps the glow identical across shell heights;
// the earlier percentage recipe stretched ~2× too tall on phones.
//
// The glow color is the tone mixed 60/40 toward the PAGE background
// before the 35% fade — ambient light, not a smudge of the raw tone:
// the saturated accent at 35% reads hard-edged, where the mock glows
// lighter-than-accent on light pages and deeper on dark ones. The
// page-mix produces both shifts from one recipe, per variant.
func (r *resolver) auraClass(t style.ToneRef) string {
	tone := string(t)
	name := "s-aura-" + tone
	if _, ok := r.used[name]; !ok {
		r.used[name] = fmt.Sprintf(
			"background-image:radial-gradient(240px 160px at 50%% 40px, color-mix(in srgb, color-mix(in srgb, var(--color-%s-bg) 60%%, var(--color-page-bg)) 35%%, transparent), transparent 65%%);background-repeat:no-repeat;",
			tone)
	}
	return name
}

// shadowClass is the tone-tinted soft shadow (large blur, 10% tone) —
// the Aura design's alternative to the black elevation scale.
func (r *resolver) shadowClass(t style.ToneRef) string {
	tone := string(t)
	name := "s-shadow-" + tone
	if _, ok := r.used[name]; !ok {
		r.used[name] = fmt.Sprintf(
			"box-shadow:0 4px 24px color-mix(in srgb, var(--color-%s-bg) 10%%, transparent);",
			tone)
	}
	return name
}

func (r *resolver) colorClass(t style.ToneRef) string {
	tone := string(t)
	name := "s-color-" + tone
	if _, ok := r.used[name]; !ok {
		switch tone {
		case "muted":
			r.used[name] = "color:var(--color-muted);"
		case "outline":
			r.used[name] = "color:var(--color-outline);"
		default:
			// Convention: the tone's BG is the "ink" color when used
			// as foreground. Matches Sigil's text tone semantics.
			r.used[name] = fmt.Sprintf("color:var(--color-%s-bg);", tone)
		}
	}
	return name
}

func (r *resolver) borderClass(t style.ToneRef) string {
	tone := string(t)
	name := "s-border-" + tone
	if _, ok := r.used[name]; !ok {
		var body string
		switch tone {
		case "outline":
			body = "border:var(--border-px) solid var(--color-outline);"
		case "muted":
			body = "border:var(--border-px) solid var(--color-muted);"
		default:
			body = fmt.Sprintf("border:var(--border-px) solid var(--color-%s-bg);", tone)
		}
		r.used[name] = body
	}
	return name
}

func (r *resolver) widthClass(w style.WidthRef) string {
	name := "s-w-" + string(w)
	if _, ok := r.used[name]; !ok {
		if w == style.WidthFill {
			r.used[name] = "width:100%;"
		} else {
			r.used[name] = fmt.Sprintf(
				"width:100%%;max-width:var(--container-width-%s);margin-left:auto;margin-right:auto;",
				w)
		}
	}
	return name
}

func (r *resolver) dirClass(d style.DirectionRef) string {
	name := "s-dir-" + string(d)
	if _, ok := r.used[name]; !ok {
		// display:flex is implicit in any direction so a primitive
		// can opt into flex layout by setting Direction alone.
		r.used[name] = fmt.Sprintf("display:flex;flex-direction:%s;", d)
	}
	return name
}

func (r *resolver) alignClass(a style.AlignRef) string {
	name := "s-align-" + string(a)
	if _, ok := r.used[name]; !ok {
		v := string(a)
		switch a {
		case style.AlignStart:
			v = "flex-start"
		case style.AlignEnd:
			v = "flex-end"
		}
		r.used[name] = fmt.Sprintf("align-items:%s;", v)
	}
	return name
}

func (r *resolver) alignSelfClass(a style.AlignRef) string {
	name := "s-self-" + string(a)
	if _, ok := r.used[name]; !ok {
		v := string(a)
		switch a {
		case style.AlignStart:
			v = "flex-start"
		case style.AlignEnd:
			v = "flex-end"
		}
		r.used[name] = fmt.Sprintf("align-self:%s;", v)
	}
	return name
}

func (r *resolver) gapClass(n int) string {
	name := fmt.Sprintf("s-gap-%d", n)
	if _, ok := r.used[name]; !ok {
		r.used[name] = fmt.Sprintf("gap:calc(%d * var(--space-sm));", n)
	}
	return name
}

// spaceVal renders a SpaceRef as its CSS value: scale tokens resolve
// through the theme var, literal pixel lengths ("12px") pass through.
func spaceVal(s style.SpaceRef) string {
	if strings.HasSuffix(string(s), "px") {
		return string(s)
	}
	return fmt.Sprintf("var(--space-%s)", s)
}

func (r *resolver) gridClass(cols string) string {
	safe := strings.NewReplacer(" ", "-", ".", "d").Replace(cols)
	name := "s-grid-" + safe
	if _, ok := r.used[name]; !ok {
		r.used[name] = fmt.Sprintf("display:grid;grid-template-columns:%s;align-items:center;", cols)
	}
	return name
}

// simpleClass is the dedup helper for single-rule slots (pad, radius,
// font, elev) where the class name and body have no special cases.
func (r *resolver) simpleClass(prefix, token, body string) string {
	name := "s-" + prefix + "-" + token
	if _, ok := r.used[name]; !ok {
		r.used[name] = body
	}
	return name
}
