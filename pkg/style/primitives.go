package style

import "github.com/incantery/mako/pkg/ir"

// SpecFor returns the Spec for one IR node, applying per-primitive
// defaults and then overlaying any author kwargs (tone, size,
// elevation, etc.) read from n.Props.
//
// This is THE single source of truth for "what styling does primitive
// X get." Renderers consume the returned Spec; they never read
// styling props off the node directly. New primitive = add a case
// here, no renderer change.
//
// The primitives are *opinionated* (cards get padding+radius+border by
// default) — they're not pure-bare headless. The reset.css baseline
// guarantees a known starting point, then SpecFor layers in the
// design system's per-primitive defaults. A future refactor can move
// these defaults into theme so themes can override "what a card looks
// like by default"; for v1 they live here.
func SpecFor(n ir.Node) Spec {
	switch n.Kind {
	case ir.KindCard:
		return cardDefaults().Merge(cardFromProps(n))
	case ir.KindButton:
		return buttonDefaults().Merge(buttonFromProps(n))
	case ir.KindText:
		return textDefaults().Merge(textFromProps(n))
	case ir.KindCode:
		return codeDefaults()
	case ir.KindTitle:
		return titleDefaults(n).Merge(titleFromProps(n))
	case ir.KindIcon:
		return iconDefaults().Merge(iconFromProps(n))
	case ir.KindDivider:
		return dividerDefaults().Merge(dividerFromProps(n))
	case ir.KindPulse:
		return pulseFromProps(n)
	case ir.KindBadge:
		return badgeDefaults().Merge(badgeFromProps(n))
	case ir.KindBar:
		return barDefaults().Merge(barFromProps(n))
	case ir.KindStack:
		return stackDefaults().Merge(stackFromProps(n))
	case ir.KindContainer:
		return containerDefaults().Merge(containerFromProps(n))
	case ir.KindTextInput:
		return inputDefaults().Merge(inputFromProps(n))
	case ir.KindIFrame:
		return iframeDefaults()
	case ir.KindRouter:
		return routerDefaults()
	case ir.KindRoute:
		return routeDefaults()
	default:
		return Spec{}
	}
}

func iframeDefaults() Spec {
	// Width/height are literal per-instance values handled by the
	// renderer (inline style); the Spec only carries the visual chrome.
	return Spec{Surface: "surface", Border: "outline", Radius: "md"}
}

func routerDefaults() Spec {
	return Spec{Direction: DirColumn, Flex: 1, Align: AlignStretch}
}

func routeDefaults() Spec {
	return Spec{Direction: DirColumn, Flex: 1, Align: AlignStretch}
}

// --- code ---

// codeDefaults gives a code block its themed chrome: a recessed surface,
// outline border, rounded corners, and roomy padding. The monospace font,
// preserved whitespace, and horizontal scroll are structural (a `pre` rule
// in structural.css), not design tokens.
func codeDefaults() Spec {
	return Spec{
		Padding: "md",
		Radius:  "md",
		Border:  "outline",
		Surface: "surface",
	}
}

// --- card ---

func cardDefaults() Spec {
	return Spec{
		Padding:   "md",
		Radius:    "md",
		Border:    "outline",
		Surface:   "surface",
		Direction: DirColumn,
	}
}

func cardFromProps(n ir.Node) Spec {
	out := Spec{Elevation: elevationFromProps(n)}
	// Uniform sizing vocabulary (same as stack): exact/hug values
	// land in the Fixed slots; the fill case arrives pre-resolved as
	// flex / align-self from lower's sizing pass.
	if w := propStr(n, "width"); w != "" {
		out.FixedWidth = w
	}
	if fh := propStr(n, "fixed-height"); fh != "" {
		out.FixedHeight = fh
	}
	if flex, ok := n.Props["flex"].(int); ok && flex > 0 {
		out.Flex = flex
	}
	t := toneFromProps(n)
	switch t {
	case "":
		// no override — defaults stand
	case "muted":
		// muted is a single color, not a paired tone; can't paint a
		// surface with it. Treat as foreground+border treatment so
		// the card visibly shifts without an inaccessible bg/fg pair.
		out.Color = t
		out.Border = t
	default:
		out.Surface = t
		out.Border = t
	}
	applyChildLayoutProps(n, &out)
	return out
}

// --- button ---

func buttonDefaults() Spec {
	return Spec{
		Surface:   "primary",
		Border:    "primary",
		Radius:    "md",
		PadInline: "md",
		PadBlock:  "sm",
		Font:      "body-strong",
		Direction: DirRow,
		Align:     AlignCenter,
		Gap:       0,
	}
}

func buttonFromProps(n ir.Node) Spec {
	out := Spec{}
	t := toneFromProps(n)
	switch t {
	case "":
		// no override — defaults stand
	case "muted":
		out.Color = t
		out.Border = t
		// Drop the default primary surface so the muted treatment
		// isn't fighting the primary fill. A muted button is
		// "ghost-style" by convention.
		out.Surface = "surface"
	default:
		out.Surface = t
		out.Border = t
	}
	if rad := propStr(n, "radius"); rad != "" {
		out.Radius = RadiusRef(rad)
	}
	// Icon-only buttons (no label, icon set) drop the wide text
	// padding to the block value so `radius=full` yields a circle,
	// not an oval. Explicit padding kwargs still win below.
	if propStr(n, "label") == "" && propStr(n, "icon") != "" {
		out.PadInline = buttonDefaults().PadBlock
	}
	applyPaddingProps(n, &out)
	return out
}

// --- text ---

func textDefaults() Spec {
	return Spec{Font: "body"}
}

func textFromProps(n ir.Node) Spec {
	out := Spec{}
	if size := propStr(n, "size"); size != "" {
		out.Font = TextRef(size)
	}
	if t := toneFromProps(n); t != "" {
		out.Color = t
	}
	return out
}

// --- title ---

func titleDefaults(n ir.Node) Spec {
	size := propStr(n, "size")
	if size == "" {
		size = "md"
	}
	return Spec{Font: TextRef("heading-" + size)}
}

func titleFromProps(n ir.Node) Spec {
	out := Spec{}
	if t := toneFromProps(n); t != "" {
		out.Color = t
	}
	return out
}

// --- icon ---

func iconDefaults() Spec {
	// Icons are intrinsically sized via the SVG itself (1em square) and
	// inherit color from currentColor. No Spec needed; the renderer
	// emits the inline-flex wrapper directly.
	return Spec{}
}

func iconFromProps(n ir.Node) Spec {
	out := Spec{}
	if t := toneFromProps(n); t != "" {
		out.Color = t
	}
	return out
}

// --- divider ---

func dividerDefaults() Spec {
	// Divider renders as a 1-line hr; structural CSS uses currentColor
	// for the line so we paint it via the Color slot rather than Border
	// (which would put a stroke on all four sides of a zero-height hr).
	return Spec{Color: "outline"}
}

func dividerFromProps(n ir.Node) Spec {
	out := Spec{}
	if t := toneFromProps(n); t != "" {
		out.Color = t
	}
	return out
}

// --- pulse ---

func pulseFromProps(n ir.Node) Spec {
	// Dots paint with currentColor; tone= shifts the ink via the Color
	// slot. No other slots — geometry/animation are structural.
	out := Spec{}
	if t := toneFromProps(n); t != "" {
		out.Color = t
	}
	return out
}

// --- badge ---

func badgeDefaults() Spec {
	return Spec{
		Surface:   "surface",
		Border:    "outline",
		PadInline: "sm",
		PadBlock:  "xs",
		Font:      "caption",
		Shape:     ShapePill,
		Direction: DirRow,
		Align:     AlignCenter,
	}
}

func badgeFromProps(n ir.Node) Spec {
	out := Spec{}
	t := toneFromProps(n)
	switch t {
	case "":
		// no tone override — defaults stand
	case "muted":
		// muted is a single color, not a paired tone — keep the
		// default surface and tint fg+border with muted so the
		// badge stays accessible (a muted bg would have no paired fg).
		out.Color = t
		out.Border = t
	default:
		out.Surface = t
		out.Border = t
	}
	if size := propStr(n, "size"); size != "" {
		out.Font = TextRef(size)
	}
	return out
}

// --- bar ---

func barDefaults() Spec {
	return Spec{
		Surface: "outline",
		Radius:  "full",
		Shape:   ShapeBar,
	}
}

func barFromProps(n ir.Node) Spec {
	out := Spec{}
	if t := toneFromProps(n); t != "" {
		out.Color = t
	}
	return out
}

// --- stack ---

func stackDefaults() Spec {
	return Spec{
		Direction: DirColumn,
		Align:     AlignStretch,
	}
}

func stackFromProps(n ir.Node) Spec {
	out := Spec{}
	isHorizontal := propStr(n, "axis") == "horizontal"
	if isHorizontal {
		out.Direction = DirRow
		out.Align = AlignCenter
	}
	if gap, ok := n.Props["gap"].(int); ok && gap > 0 {
		out.Gap = gap
	}
	if t := toneFromProps(n); t != "" {
		out.Surface = t
	}
	if flex, ok := n.Props["flex"].(int); ok && flex > 0 {
		out.Flex = flex
	}
	if h := propStr(n, "height"); h != "" {
		out.Height = h
		if isHorizontal {
			out.Align = AlignStretch
		}
	}
	if out.Flex > 0 && isHorizontal {
		out.Align = AlignStretch
	}
	if s := propStr(n, "scroll"); s != "" {
		out.Scroll = s
	}
	if w := propStr(n, "width"); w != "" {
		out.FixedWidth = w
	}
	if fh := propStr(n, "fixed-height"); fh != "" {
		out.FixedHeight = fh
	}
	if c := propStr(n, "columns"); c != "" {
		out.Columns = c
	}
	if b := propStr(n, "border"); b != "" {
		out.Border = ToneRef(b)
	}
	applyChildLayoutProps(n, &out)
	return out
}

// --- container ---

func containerDefaults() Spec {
	return Spec{Width: WidthFill, Direction: DirColumn}
}

func containerFromProps(n ir.Node) Spec {
	out := Spec{}
	if w := propStr(n, "width"); w != "" {
		out.Width = WidthRef(w)
	}
	return out
}

// --- text input ---

func inputDefaults() Spec {
	return Spec{
		Surface:   "surface",
		Border:    "outline",
		Radius:    "md",
		PadInline: "md",
		PadBlock:  "sm",
		Font:      "body",
	}
}

func inputFromProps(n ir.Node) Spec {
	out := Spec{}
	if t := toneFromProps(n); t != "" {
		out.Border = t
	}
	if rad := propStr(n, "radius"); rad != "" {
		out.Radius = RadiusRef(rad)
	}
	return out
}

// --- helpers ---

// applyChildLayoutProps reads the kwargs every block-level child can
// carry — radius= (corner override), align= (align-self within the
// parent), maxwidth= (px width cap). Shared by stack and card so the
// chat-bubble shape (`card tone=primary align=end maxwidth=480 radius=xl`)
// spells the same on either.
func applyChildLayoutProps(n ir.Node, out *Spec) {
	if rad := propStr(n, "radius"); rad != "" {
		out.Radius = RadiusRef(rad)
	}
	if al := propStr(n, "align-self"); al != "" {
		out.AlignSelf = AlignRef(al)
	}
	if mw := propStr(n, "maxwidth"); mw != "" {
		out.MaxWidth = mw
	}
	applyPaddingProps(n, out)
	applySurfaceProps(n, out)
}

// applyPaddingProps reads padding= / padx= / pady= (space token or
// "<n>px" literal — lowering already normalized integers). padx/pady
// override the uniform padding per axis: their atomic classes sort
// after s-pad- in the alphabetized stylesheet.
func applyPaddingProps(n ir.Node, out *Spec) {
	if p := propStr(n, "padding"); p != "" {
		out.Padding = SpaceRef(p)
	}
	if px := propStr(n, "padx"); px != "" {
		out.PadInline = SpaceRef(px)
	}
	if py := propStr(n, "pady"); py != "" {
		out.PadBlock = SpaceRef(py)
	}
}

// applySurfaceProps reads the expressive-surface vocabulary — the
// `glass` flag, `aura=<tone>` glow, `shadow=<tone>` tinted shadow.
// Shared by stack and card.
func applySurfaceProps(n ir.Node, out *Spec) {
	if g, _ := n.Props["glass"].(bool); g {
		out.Glass = true
	}
	if a := propStr(n, "aura"); a != "" {
		out.Aura = ToneRef(a)
	}
	if s := propStr(n, "shadow"); s != "" {
		out.Shadow = ToneRef(s)
	}
}

func toneFromProps(n ir.Node) ToneRef {
	t, _ := n.Props["tone"].(string)
	if t == "" || t == "default" {
		return ""
	}
	return ToneRef(t)
}

func elevationFromProps(n ir.Node) ElevationRef {
	e, _ := n.Props["elevation"].(string)
	if e == "" {
		return ""
	}
	return ElevationRef(e)
}

func propStr(n ir.Node, k string) string {
	if v, ok := n.Props[k].(string); ok {
		return v
	}
	return ""
}
