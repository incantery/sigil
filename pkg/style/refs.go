package style

// Token-reference types. Each ref is a string-typed pointer to a token
// in the active theme. Empty string = "unset" — the resolver emits
// nothing for an unset slot.
//
// These are distinct named types (rather than a bare `string`) so the
// Go type checker catches "passing a tone where a space was expected"
// at the primitives.go boundary. The values themselves are the same
// strings the source language uses ("accent", "md", "lg"); themes
// decide what they mean.

// ToneRef names a tone in Theme.Tones — "surface", "page", "primary",
// "accent", "danger", "success", "warning", "muted", or "default".
// "default" / "" both mean "no tone" — the resolver emits no class.
// "muted" is special-cased by the resolver because the theme stores
// it as a single color, not a pair.
type ToneRef string

// SpaceRef names a space scale entry — "xs", "sm", "md", "lg", "xl" —
// or a literal pixel length ("12px") when the author needs a value
// between scale steps (resolvers branch on the "px" suffix).
type SpaceRef string

// RadiusRef names a radius scale entry — "none", "sm", "md", "lg",
// "xl", "xxl", "full".
type RadiusRef string

// TextRef names a text-scale entry — "caption", "body", "body-strong",
// "heading-sm", "heading-md", "heading-lg".
type TextRef string

// ElevationRef names an elevation scale entry — "flat", "sm", "md", "lg".
type ElevationRef string

// WidthRef names a container-width scale entry — "narrow", "medium",
// "wide", or "fill" (the sentinel "no max-width" value).
type WidthRef string

// DirectionRef is the flex direction — "row" or "column".
type DirectionRef string

// AlignRef is the cross-axis alignment for flex containers —
// "start", "center", "end", "stretch".
type AlignRef string

// ShapeRef is a layout-shape token. Most primitives leave it empty.
// Bar / Badge use it to opt into specific child structure or radius
// extremes (pill = full radius + inline-flex) the resolver knows how
// to draw.
type ShapeRef string

const (
	ShapePill ShapeRef = "pill"
	ShapeBar  ShapeRef = "bar"
)

// AlignCenter / AlignStart etc. are exported constants for slot
// values that the lowering layer doesn't strictly need but
// per-primitive defaults in primitives.go reference frequently.
const (
	DirRow    DirectionRef = "row"
	DirColumn DirectionRef = "column"
)

const (
	AlignStart   AlignRef = "start"
	AlignCenter  AlignRef = "center"
	AlignEnd     AlignRef = "end"
	AlignStretch AlignRef = "stretch"
)

// WidthFill is the sentinel "no max-width" value — container width=full
// resolves here. Distinct from "" so the resolver can distinguish
// "explicit no constraint" from "no width opinion at all."
const WidthFill WidthRef = "fill"
