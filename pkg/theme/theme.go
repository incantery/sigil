// Package theme is Sigil's styling realization layer.
//
// Source-side primitives reference *intent* (tone, prominence, density). A
// Theme is one renderer's concrete realization of that intent vocabulary —
// what color does "danger" map to, what pixel value does "space-md" mean.
// Themes compose along multiple axes (color mode × contrast × text scale ×
// motion × density) so a single source tree drives every accessible variant
// without per-axis source forks.
//
// The theme is the design system's manifest: open the theme file, read every
// design decision in one place. A messy theme is a messy design system, made
// legible. See [[feedback-autonomous-execution]] and the L9 design
// conversation for the rationale.
package theme

import "fmt"

// ColorPair is the unit of "color used as background" + "color used on top
// of that background." Pairing is mandatory — contrast is meaningful only as
// a relationship, never per-color. The validator (Validate, below) enforces
// WCAG AA on every pair before the theme can be used.
type ColorPair struct {
	BG string
	FG string
}

// TextStyle bundles the size+weight that a single text token resolves to.
// Renderer-specific units (px/pt/em) are decided by the target — here we
// store unitless logical scale; HTML treats as px, PDF as pt, etc.
type TextStyle struct {
	Size     int    // size in target-native units
	Weight   int    // 100..900 like CSS font-weight
	Family   string // optional named font family
	Italic   bool   // italic style (HTML: font-style)
	Tracking int    // letter-spacing in 1/100 em (10 = 0.1em); 0 = normal
	Caps     bool   // uppercase transform (HTML: text-transform)
}

// Elevation is the structured shadow recipe a card uses when its
// `elevation=` kwarg names it. Renderers translate this into their
// native idiom: HTML/CSS box-shadow, SwiftUI .shadow(...), etc.
// Storing as components (vs. pre-rendered CSS strings) keeps the
// theme renderer-agnostic.
type Elevation struct {
	Y       int     // vertical offset in target units
	Blur    int     // blur radius
	Spread  int     // spread radius
	Opacity float64 // 0..1 — opacity of the shadow's color (black)
}

// Theme is one fully-realized realization layer. Every category of token
// has values; missing-token lookups fall back to defaults at render time.
//
// Two themes related by Extends share intent vocabulary; only the override
// keys are stored on the derived theme. Modifier is the third tier (delta
// on top of any base, not a theme itself).
type Theme struct {
	Name            string
	ExtendsName     string               // base theme name ("light"/"dark"); empty if not derived
	Tones           map[string]ColorPair // "primary" -> bg+fg pair; "surface", "danger", ...
	Spacing         map[string]int       // "md" -> 16 (target-native)
	Radii           map[string]int       // "md" -> 6
	TextScale       map[string]TextStyle // "body" -> {14, 400, ""}
	Elevations      map[string]Elevation // "md" -> structured shadow recipe
	ContainerWidths map[string]int       // "medium" -> 720 (target-native, used by `container width=`)
	Outline         string               // standalone outline/border color (no on-pair)
	Muted           string               // muted/secondary text color
	BorderPx        int                  // default border width
}

// Modifier is an additive overlay (high-contrast, large-text, reduced-motion).
// Apply on top of a base Theme; only the set fields override. Modifiers are
// composable — high-contrast + large-text can both apply.
type Modifier struct {
	Name        string
	ToneOverlay map[string]ColorPair // partial override; missing tones unchanged
	BorderPx    int                  // 0 means "no change"
	TextScale   map[string]TextStyle // partial override; e.g. large-text scales sizes
	Outline     string               // empty = unchanged
	Muted       string               // empty = unchanged
}

// Apply produces a new Theme that is base with mod overlaid. Original
// theme is not mutated; safe to keep light + dark and produce
// light-high-contrast / dark-high-contrast on demand.
func (mod Modifier) Apply(base Theme) Theme {
	out := Theme{
		Name:            base.Name + "+" + mod.Name,
		Tones:           copyTones(base.Tones),
		Spacing:         copyInts(base.Spacing),
		Radii:           copyInts(base.Radii),
		TextScale:       copyText(base.TextScale),
		Elevations:      copyElevations(base.Elevations),
		ContainerWidths: copyInts(base.ContainerWidths),
		Outline:         base.Outline,
		Muted:           base.Muted,
		BorderPx:        base.BorderPx,
	}
	for k, v := range mod.ToneOverlay {
		out.Tones[k] = v
	}
	for k, v := range mod.TextScale {
		out.TextScale[k] = v
	}
	if mod.Outline != "" {
		out.Outline = mod.Outline
	}
	if mod.Muted != "" {
		out.Muted = mod.Muted
	}
	if mod.BorderPx != 0 {
		out.BorderPx = mod.BorderPx
	}
	return out
}

// Extends produces a new Theme that is base with delta's set fields
// replacing base's. Used to define "dark" as "light with these colors
// changed" without restating every spacing value.
func (delta Theme) Extends(base Theme) Theme {
	out := Theme{
		Name:            delta.Name,
		Tones:           copyTones(base.Tones),
		Spacing:         copyInts(base.Spacing),
		Radii:           copyInts(base.Radii),
		TextScale:       copyText(base.TextScale),
		Elevations:      copyElevations(base.Elevations),
		ContainerWidths: copyInts(base.ContainerWidths),
		Outline:         base.Outline,
		Muted:           base.Muted,
		BorderPx:        base.BorderPx,
	}
	for k, v := range delta.Tones {
		out.Tones[k] = v
	}
	for k, v := range delta.Spacing {
		out.Spacing[k] = v
	}
	for k, v := range delta.Radii {
		out.Radii[k] = v
	}
	for k, v := range delta.TextScale {
		out.TextScale[k] = v
	}
	for k, v := range delta.Elevations {
		out.Elevations[k] = v
	}
	for k, v := range delta.ContainerWidths {
		out.ContainerWidths[k] = v
	}
	if delta.Outline != "" {
		out.Outline = delta.Outline
	}
	if delta.Muted != "" {
		out.Muted = delta.Muted
	}
	if delta.BorderPx != 0 {
		out.BorderPx = delta.BorderPx
	}
	return out
}

// Validate runs the design-system audit: every color pair meets WCAG AA
// contrast (4.5:1 for normal text). Returns the first failure encountered.
// Run at theme registration so themes can never ship inaccessible.
func (t Theme) Validate() error {
	for name, pair := range t.Tones {
		ratio, err := ContrastRatio(pair.BG, pair.FG)
		if err != nil {
			return fmt.Errorf("theme %q tone %q: %w", t.Name, name, err)
		}
		if ratio < 4.5 {
			return fmt.Errorf(
				"theme %q tone %q: contrast %.2f:1 is below WCAG AA (4.5:1); pair (%s on %s) is inaccessible",
				t.Name, name, ratio, pair.FG, pair.BG)
		}
	}
	return nil
}

// MustValidate panics if Validate fails. Use at package init for static
// default themes — a built-in theme that fails contrast is a build error.
func (t Theme) MustValidate() Theme {
	if err := t.Validate(); err != nil {
		panic(err)
	}
	return t
}

// --- Defaults ---

// Light is Sigil's default base theme. Light surface, neutral primary
// (charcoal on white for a "system look" feel), Tailwind-flavored
// utility colors. All pairs verified AA at init.
var Light = Theme{
	Name: "light",
	Tones: map[string]ColorPair{
		"surface": {BG: "#ffffff", FG: "#18181b"},
		"page":    {BG: "#fafafa", FG: "#18181b"},
		"primary": {BG: "#18181b", FG: "#ffffff"},
		"accent":  {BG: "#2563eb", FG: "#ffffff"},
		"danger":  {BG: "#dc2626", FG: "#ffffff"},
		"success": {BG: "#15803d", FG: "#ffffff"},
		"warning": {BG: "#a16207", FG: "#ffffff"},
	},
	Spacing: map[string]int{
		"xs": 4, "sm": 8, "md": 16, "lg": 24, "xl": 32,
	},
	Radii: map[string]int{
		"none": 0, "sm": 4, "md": 6, "lg": 8, "xl": 16, "xxl": 22, "full": 9999,
	},
	TextScale: map[string]TextStyle{
		"caption":     {Size: 12, Weight: 400},
		"body":        {Size: 14, Weight: 400},
		"body-strong": {Size: 14, Weight: 500},
		"heading-sm":  {Size: 18, Weight: 600},
		"heading-md":  {Size: 24, Weight: 600},
		"heading-lg":  {Size: 32, Weight: 700},
	},
	Elevations: map[string]Elevation{
		// `flat` is the no-shadow base (cards default here so existing
		// docs render byte-identically). The rest follow a 2x-blur
		// progression with slight opacity bumps; tuned by eye on the
		// Pokédex demo at the light surface palette.
		"flat": {},
		"sm":   {Y: 1, Blur: 2, Opacity: 0.06},
		"md":   {Y: 4, Blur: 12, Opacity: 0.08},
		"lg":   {Y: 12, Blur: 32, Opacity: 0.12},
	},
	ContainerWidths: map[string]int{
		// Sensible reading widths in target-native units. `narrow` is
		// the form/dialog band; `medium` is the article / dashboard
		// column; `wide` is the marketing / data-grid band; `full`
		// has no constraint and is the historical default.
		"narrow": 480,
		"medium": 760,
		"wide":   1080,
	},
	Outline:  "#d4d4d8",
	Muted:    "#71717a",
	BorderPx: 1,
}

// Dark is the dark realization. Inverts surface/page, lifts primary so
// it's distinguishable on dark backgrounds, recolors accent/danger/
// success/warning for dark-mode contrast.
var Dark = Theme{
	Name: "dark",
	Tones: map[string]ColorPair{
		"surface": {BG: "#18181b", FG: "#fafafa"},
		"page":    {BG: "#0a0a0a", FG: "#fafafa"},
		"primary": {BG: "#fafafa", FG: "#18181b"},
		"accent":  {BG: "#3b82f6", FG: "#0a0a0a"},
		"danger":  {BG: "#ef4444", FG: "#0a0a0a"},
		"success": {BG: "#22c55e", FG: "#0a0a0a"},
		"warning": {BG: "#eab308", FG: "#0a0a0a"},
	},
	Outline: "#404040",
	Muted:   "#a1a1aa",
}.Extends(Light)

// HighContrast is a modifier that ratchets every pair to the most extreme
// foreground possible, fattens borders, and removes the muted-text
// half-opacity look. Composes with Light or Dark.
var HighContrast = Modifier{
	Name: "high-contrast",
	ToneOverlay: map[string]ColorPair{
		// Pure-black / pure-white surfaces, max-contrast tones. These
		// pairs validate AAA (7:1) when applied on light; the dark
		// composition is computed at Apply time.
		"surface": {BG: "#ffffff", FG: "#000000"},
		"page":    {BG: "#ffffff", FG: "#000000"},
		"primary": {BG: "#000000", FG: "#ffffff"},
		"accent":  {BG: "#0000ee", FG: "#ffffff"},
		"danger":  {BG: "#a30000", FG: "#ffffff"},
		"success": {BG: "#005a00", FG: "#ffffff"},
		"warning": {BG: "#7a4f00", FG: "#ffffff"},
	},
	BorderPx: 2,
	Outline:  "#000000",
	Muted:    "#000000",
}

// DarkHighContrast is the dark-mode high-contrast composition, with
// surfaces flipped to black/white but otherwise mirroring HighContrast's
// intent. Surfaced as a named composition so renderers can ship CSS for
// all four base combinations without callers having to compose.
var DarkHighContrast = Modifier{
	Name: "high-contrast",
	ToneOverlay: map[string]ColorPair{
		"surface": {BG: "#000000", FG: "#ffffff"},
		"page":    {BG: "#000000", FG: "#ffffff"},
		"primary": {BG: "#ffffff", FG: "#000000"},
		"accent":  {BG: "#7dafff", FG: "#000000"},
		"danger":  {BG: "#ff6b6b", FG: "#000000"},
		"success": {BG: "#5cde5c", FG: "#000000"},
		"warning": {BG: "#ffd84d", FG: "#000000"},
	},
	BorderPx: 2,
	Outline:  "#ffffff",
	Muted:    "#ffffff",
}

// IntentTones is the canonical closed enum of `tone=...` values that the
// language allows on primitives AND that source-declared `theme` blocks
// can override. `page` is here too — it's the background-band tone that
// covers the body behind cards, and brand themes routinely want control
// over it.
var IntentTones = []string{
	"default", "surface", "page", "primary", "accent", "danger", "success", "warning", "muted",
}

// --- internal copy helpers (defensive: avoid aliasing maps across themes) ---

func copyTones(in map[string]ColorPair) map[string]ColorPair {
	out := make(map[string]ColorPair, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func copyInts(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func copyText(in map[string]TextStyle) map[string]TextStyle {
	out := make(map[string]TextStyle, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func copyElevations(in map[string]Elevation) map[string]Elevation {
	out := make(map[string]Elevation, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
