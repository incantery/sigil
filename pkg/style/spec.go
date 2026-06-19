// Package style is Sigil's renderer-agnostic styling substrate.
//
// A Spec is the typed styling intent of one primitive — surface tone,
// padding, radius, font scale, elevation, gap, etc. Slot values are
// always *token references* (e.g. ToneRef("accent"), SpaceRef("md")),
// never concrete pixels or hex colors. Themes resolve refs to
// target-native output; this package never names a CSS unit.
//
// The author-facing language (lowering kwargs to ir.Node Props) and the
// renderers (HTML/SwiftUI/terminal) meet in the middle here: lowering
// emits semantic Nodes, primitives.go translates Node → Spec once,
// per-target resolvers translate Spec → target output. Adding a
// rendering target means writing one resolver. Adding a primitive
// means adding one case to SpecFor. There is no other place that
// authors styling intent.
//
// Spec is intentionally a value type with all-public fields rather than
// an opaque builder — composition is plain Go struct overlay (see
// Spec.Merge). The slot list is closed: extending Spec means a typed
// edit here, not stringly-keyed prop-bag entries. That closed set is
// the design system's vocabulary, period.
package style

// Spec carries every styling decision for one node. Empty slots emit
// nothing — a zero-value Spec produces a bare semantic element. Themes
// + per-primitive defaults (see primitives.go) are responsible for
// populating slots when the author doesn't.
type Spec struct {
	// Surface paints background + foreground + border-color from one
	// tone pair. The dominant "fill" slot. Used by card / button /
	// stack / badge — anywhere a primitive has an own surface.
	Surface ToneRef

	// Color paints foreground only (no background change). Used by
	// text / title / icon — primitives that inherit their background
	// from the parent. The tone's BG is used as the "ink" color
	// (matches Sigil's text tone convention).
	Color ToneRef

	// Border draws a 1-unit border (width from theme BorderPx) in the
	// referenced tone. Independent of Surface — a primitive can have
	// no surface but still get an outline.
	Border ToneRef

	// Glass switches the surface paint to its translucent recipe:
	// the tone's background at 75% over a backdrop blur. Replaces the
	// opaque Surface treatment (the resolver emits one or the other);
	// an unset Surface glasses over the default surface tone.
	Glass bool

	// Aura paints a soft radial glow of the referenced tone fading
	// from the top edge — the ambient "something is alive here"
	// treatment. Emitted as a background-image so it layers over any
	// surface or glass paint.
	Aura ToneRef

	// Shadow casts the soft tone-tinted shadow recipe (large blur,
	// 10% tone) instead of the black elevation scale. When both are
	// set, Shadow wins (it sorts after s-elev- in the stylesheet).
	Shadow ToneRef

	// Padding sets uniform padding on all sides from a space-scale
	// token. PadInline / PadBlock override per-axis when set.
	Padding   SpaceRef
	PadInline SpaceRef
	PadBlock  SpaceRef

	// Radius rounds corners from the radii scale.
	Radius RadiusRef

	// Font picks a size+weight pair from the text scale. Used by
	// text/title/button labels — anywhere text is rendered.
	Font TextRef

	// Elevation applies a shadow recipe from the elevations scale.
	// "flat" / unset emits no shadow.
	Elevation ElevationRef

	// Width clamps the element to a container-width token (narrow /
	// medium / wide) or "fill" for 100%. Used by container.
	Width WidthRef

	// Direction picks row or column layout for flex containers
	// (stack, container). Empty = renderer default (column).
	Direction DirectionRef

	// Align is cross-axis alignment for flex containers.
	Align AlignRef

	// AlignSelf overrides THIS element's own cross-axis alignment
	// inside its parent flex container (per-child alignment — the
	// chat-bubble-hugs-the-end shape). Independent of Align, which
	// positions this element's children. In a stretch-aligned column,
	// an end/start-aligned child also shrinks to fit its content.
	AlignSelf AlignRef

	// Gap is the gap between flex children, in space-sm units (gap=3 =
	// 3 * --space-sm = 24px under the default scale). Zero = no gap.
	// Kept as an integer multiplier for v1 because the source language
	// uses `gap=N`; future cleanup may switch to SpaceRef.
	Gap int

	// Shape forces a specific layout shape (e.g. ShapePill for badge's
	// stadium look, ShapeBar for the bar's track + fill). Most
	// primitives leave this empty. Resolvers translate Shape to the
	// target's idiomatic geometry — for HTML this means full radius +
	// flex inline-block + specific child structure.
	Shape ShapeRef

	// Flex sets flex-grow so the element fills remaining space in its
	// parent flex container. 0 = no flex-grow (default). 1 = flex:1.
	Flex int

	// Height sets the element's height. "full" = min-height:100vh.
	Height string

	// Scroll enables overflow scrolling. "y" = overflow-y:auto.
	Scroll string

	// FixedWidth sets a fixed CSS width (e.g. "240px", "fit-content").
	// Distinct from Width which uses container-width scale tokens.
	FixedWidth string

	// FixedHeight sets a fixed CSS height (e.g. "240px",
	// "fit-content") — the height= sizing vocabulary's exact and
	// hug-content cases. The fill case never lands here; it resolves
	// to Flex (main axis) or AlignSelf stretch (cross axis) at lower
	// time.
	FixedHeight string

	// MaxWidth caps the element's width at a fixed CSS length (e.g.
	// "480px") while letting it shrink to fit below the cap. Pairs
	// with AlignSelf for chat bubbles that must not span the column.
	MaxWidth string

	// Columns switches the element to CSS grid with the given
	// grid-template-columns value (e.g. "2fr 1fr 80px 80px").
	Columns string
}

// Merge overlays other on top of s. Non-empty slots in other win.
// Used by primitives.go to apply author kwargs over per-primitive
// defaults: defaults.Merge(fromProps).
func (s Spec) Merge(other Spec) Spec {
	out := s
	if other.Surface != "" {
		out.Surface = other.Surface
	}
	if other.Color != "" {
		out.Color = other.Color
	}
	if other.Border != "" {
		out.Border = other.Border
	}
	if other.Glass {
		out.Glass = true
	}
	if other.Aura != "" {
		out.Aura = other.Aura
	}
	if other.Shadow != "" {
		out.Shadow = other.Shadow
	}
	if other.Padding != "" {
		out.Padding = other.Padding
	}
	if other.PadInline != "" {
		out.PadInline = other.PadInline
	}
	if other.PadBlock != "" {
		out.PadBlock = other.PadBlock
	}
	if other.Radius != "" {
		out.Radius = other.Radius
	}
	if other.Font != "" {
		out.Font = other.Font
	}
	if other.Elevation != "" {
		out.Elevation = other.Elevation
	}
	if other.Width != "" {
		out.Width = other.Width
	}
	if other.Direction != "" {
		out.Direction = other.Direction
	}
	if other.Align != "" {
		out.Align = other.Align
	}
	if other.AlignSelf != "" {
		out.AlignSelf = other.AlignSelf
	}
	if other.Gap != 0 {
		out.Gap = other.Gap
	}
	if other.Shape != "" {
		out.Shape = other.Shape
	}
	if other.Flex != 0 {
		out.Flex = other.Flex
	}
	if other.Height != "" {
		out.Height = other.Height
	}
	if other.Scroll != "" {
		out.Scroll = other.Scroll
	}
	if other.FixedWidth != "" {
		out.FixedWidth = other.FixedWidth
	}
	if other.FixedHeight != "" {
		out.FixedHeight = other.FixedHeight
	}
	if other.MaxWidth != "" {
		out.MaxWidth = other.MaxWidth
	}
	if other.Columns != "" {
		out.Columns = other.Columns
	}
	return out
}

// IsZero reports whether the Spec carries any styling intent. A zero
// Spec means "render bare semantic markup with no class decoration."
func (s Spec) IsZero() bool {
	return s == Spec{}
}
