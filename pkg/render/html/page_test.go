package html

import (
	"strings"
	"testing"

	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/lang/parser"
)

// renderSource compiles a .sigil source through parse → lower →
// WriteDoc and returns the full HTML page.
func renderSource(t *testing.T, src string) string {
	t.Helper()
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	var b strings.Builder
	if err := WriteDoc(&b, "test", doc); err != nil {
		t.Fatalf("WriteDoc: %v", err)
	}
	return b.String()
}

func assertPageContains(t *testing.T, page, want string) {
	t.Helper()
	if !strings.Contains(page, want) {
		t.Errorf("page missing %q", want)
	}
}

// TestChildLayoutKwargs locks the REQUEST-10 chat-bubble vocabulary:
// radius= overrides a primitive's corner token, align= positions the
// element itself within its parent stack (align-self), maxwidth= caps
// its width in pixels.
func TestChildLayoutKwargs(t *testing.T) {
	page := renderSource(t, `view Bubbles =
  state draft = ""
  stack gap=2
    card tone=primary align=end maxwidth=480 radius=xl
      text "user turn"
    stack align=start maxwidth=320 radius=full tone=surface
      text "iris turn"
    button "Send" radius=full
    input draft radius=lg placeholder="say"
`)
	// Atomic classes resolved from the new Spec slots.
	assertPageContains(t, page, "align-self:flex-end;")
	assertPageContains(t, page, "align-self:flex-start;")
	assertPageContains(t, page, "max-width:480px;")
	assertPageContains(t, page, "max-width:320px;")
	assertPageContains(t, page, "border-radius:var(--radius-xl);")
	assertPageContains(t, page, "border-radius:var(--radius-full);")
	assertPageContains(t, page, "border-radius:var(--radius-lg);")
	// The xl radius step ships in the theme scale.
	assertPageContains(t, page, "--radius-xl: 16px;")
}

// TestInputType locks the input type= masking kwarg (iris login
// page need) through the composed page: the default is text, password
// masks, and email hints the mobile keyboard. The HTML `type` is
// otherwise unreachable, so a login form couldn't mask its password
// field. The page is SPA-rendered, so the input's type is set on the
// client-built element (input.type = "…"), not a literal <input> tag.
func TestInputType(t *testing.T) {
	page := renderSource(t, `view Login =
  state email = ""
  state password = ""
  stack gap=2
    input email type=email placeholder="you@example.com"
    input password type=password placeholder="password"
    input email placeholder="defaults to text"
`)
	assertPageContains(t, page, `.type = "email";`)
	assertPageContains(t, page, `.type = "password";`)
	// The unset input keeps the text default.
	assertPageContains(t, page, `.type = "text";`)
}

// TestSizingVocabularyRendersCSS locks the uniform fit/fill/px
// sizing vocabulary all the way to emitted CSS — the IR-level lower
// test stops at props, but the Props→Spec mapping (per-kind, easy to
// drop for one kind) and the resolver classes are only covered here.
func TestSizingVocabularyRendersCSS(t *testing.T) {
	page := renderSource(t, `view App =
  stack horizontal
    stack width=fill
      text "main-axis fill -> flex"
    card width=fit height=200
      text "hug + exact px"
  stack
    card width=fill
      text "cross-axis fill -> stretch"
`)
	// main-axis fill → flex growth
	assertPageContains(t, page, "flex:1;")
	// fit → fit-content width
	assertPageContains(t, page, "width:fit-content;")
	// exact px height via the new FixedHeight slot
	assertPageContains(t, page, "height:200px;")
	// cross-axis fill → align-self stretch
	assertPageContains(t, page, "align-self:stretch;")
}

// TestChildLayoutKwargRejections locks the closed-vocabulary errors.
func TestChildLayoutKwargRejections(t *testing.T) {
	cases := []struct {
		name, src, wantErr string
	}{
		{"bad radius token", `view V =
  card radius=huge
    text "x"
`, "unknown radius"},
		{"bad align value", `view V =
  card align=middle
    text "x"
`, "unknown align"},
		{"maxwidth not int", `view V =
  card maxwidth=wide
    text "x"
`, "maxwidth= must be a positive integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = lower.Lower(root)
			if err == nil {
				t.Fatalf("expected lowering error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestFontTokensAndLoading locks the REQUEST-10 type vocabulary: theme
// `text` bindings override/declare text-scale tokens (family, italic,
// size, weight), a `fonts` decl loads the families, and the css2 URL
// requests exactly the variants the scale uses.
func TestFontTokensAndLoading(t *testing.T) {
	page := renderSource(t, `fonts google = "Spline Sans" "Instrument Serif"

theme Aura =
  primary = "#1d4ed8" on "#ffffff"
  text body = "Spline Sans"
  text wordmark = "Instrument Serif" italic 21
  text mono = 12 500

view App =
  stack gap=1
    text "iris" size=wordmark
    text "12:01" size=mono
    text "hello"
`)
	// Preconnect + one css2 stylesheet URL with computed variants:
	// Spline Sans is used at weight 400 (body override), Instrument
	// Serif at italic 400 (wordmark).
	assertPageContains(t, page, `<link rel="preconnect" href="https://fonts.googleapis.com">`)
	assertPageContains(t, page, "family=Spline+Sans:wght@400")
	assertPageContains(t, page, "family=Instrument+Serif:ital,wght@1,400")
	assertPageContains(t, page, "display=swap")

	// Per-token vars: declared family lands with the app stack as
	// fallback; italic flows into the style var; the new mono token
	// carries its size/weight.
	assertPageContains(t, page, `--text-wordmark-family: "Instrument Serif", var(--font-family);`)
	assertPageContains(t, page, "--text-wordmark-style: italic;")
	assertPageContains(t, page, "--text-wordmark-size: 21px;")
	assertPageContains(t, page, "--text-mono-size: 12px;")
	assertPageContains(t, page, "--text-mono-weight: 500;")
	assertPageContains(t, page, `--text-body-family: "Spline Sans", var(--font-family);`)

	// The font atomic class now resolves family + style per token.
	assertPageContains(t, page, "font-family:var(--text-wordmark-family);font-style:var(--text-wordmark-style);")

	// Type vocabulary is palette-independent: the wordmark token,
	// declared only on the (light-extending) Aura theme, must reach all
	// four realizations — light, dark, and both high-contrast variants —
	// or dark mode would resolve its vars to nothing. Each realization
	// emits under two selectors (media query + explicit data attr), so
	// the var appears 4 × 2 = 8 times.
	if got := strings.Count(page, "--text-wordmark-family:"); got != 8 {
		t.Errorf("wordmark family var in %d theme rule blocks, want 8 (4 realizations x 2 selectors)", got)
	}
}

// TestFontDeclErrors locks the closed-vocabulary diagnostics.
func TestFontDeclErrors(t *testing.T) {
	cases := []struct {
		name, src, wantErr string
	}{
		{"unknown provider", `fonts adobe = "Proxima Nova"

view V =
  text "x"
`, "unknown font provider"},
		{"weight out of range", `theme T =
  text body = 14 950

view V =
  text "x"
`, "weight 950 out of range"},
		{"custom token must be declared", `view V =
  text "x" size=wordmark
`, "unknown size"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = lower.Lower(root)
			if err == nil {
				t.Fatalf("expected lowering error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestExpressiveSurfaces locks the REQUEST-10 surface vocabulary:
// `glass` (translucent tone + backdrop blur, replacing the opaque
// surface paint), `aura=<tone>` (radial top glow as background-image,
// composing with any surface), and `shadow=<tone>` (soft tone-tinted
// box-shadow).
func TestExpressiveSurfaces(t *testing.T) {
	page := renderSource(t, `view Aura =
  stack gap=2 aura=accent
    card glass radius=xl shadow=accent
      text "composer"
    stack glass tone=primary
      text "pill"
`)
	// Glass replaces the surface paint — translucent bg over blur,
	// keyed to the element's tone (default surface, explicit primary).
	assertPageContains(t, page, ".s-glass-surface { background:color-mix(in srgb, var(--color-surface-bg) 75%, transparent);color:var(--color-surface-fg);backdrop-filter:blur(14px);")
	assertPageContains(t, page, ".s-glass-primary { background:color-mix(in srgb, var(--color-primary-bg) 75%, transparent);")
	// A glassed card must not also paint its opaque surface.
	if strings.Contains(page, ".s-surface-surface") {
		t.Errorf("glass card still emits the opaque s-surface-surface class")
	}
	// Aura: the mock's tight ellipse hovering above the top edge,
	// background-image so it layers over surface/glass paint.
	assertPageContains(t, page, ".s-aura-accent { background-image:radial-gradient(240px 160px at 50% 40px, color-mix(in srgb, color-mix(in srgb, var(--color-accent-bg) 60%, var(--color-page-bg)) 35%, transparent), transparent 65%);background-repeat:no-repeat; }")
	// Tinted shadow.
	assertPageContains(t, page, ".s-shadow-accent { box-shadow:0 4px 24px color-mix(in srgb, var(--color-accent-bg) 10%, transparent); }")
}

// TestExpressiveSurfaceErrors locks the closed-vocabulary diagnostics.
func TestExpressiveSurfaceErrors(t *testing.T) {
	cases := []struct {
		name, src, wantErr string
	}{
		{"aura tone closed", `view V =
  stack aura=muted
    text "x"
`, "unknown aura tone"},
		{"shadow tone closed", `view V =
  card shadow=glow
    text "x"
`, "unknown shadow tone"},
		{"card flag closed", `view V =
  card glassy
    text "x"
`, `did you mean "glass"?`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = lower.Lower(root)
			if err == nil {
				t.Fatalf("expected lowering error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestNeutralOverrides locks REQUEST 11 item 1: `outline = "#hex"` and
// `muted = "#hex"` single-color theme bindings reach the emitted vars —
// the violet-tinted neutrals that kill the zinc "gray wash".
func TestNeutralOverrides(t *testing.T) {
	page := renderSource(t, `theme Aura =
  outline = "#dfdde3"
  muted = "#737079"

view App =
  card
    text "hello"
`)
	assertPageContains(t, page, "--color-outline: #dfdde3;")
	assertPageContains(t, page, "--color-muted: #737079;")
}

// TestNeutralOverrideErrors locks the guard rails: muted is caption
// text and keeps the AA bar against the composed surface/page
// backgrounds; both names reject the paired form.
func TestNeutralOverrideErrors(t *testing.T) {
	cases := []struct {
		name, src, wantErr string
	}{
		{"muted below AA", `theme T =
  muted = "#cccccc"

view V =
  text "x"
`, "below WCAG AA"},
		{"outline rejects pair", `theme T =
  outline = "#dfdde3" on "#000000"

view V =
  text "x"
`, "outline takes a single color"},
		{"pair tone rejects single color", `theme T =
  primary = "#1d4ed8"

view V =
  text "x"
`, "needs a pair"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = lower.Lower(root)
			if err == nil {
				t.Fatalf("expected lowering error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestRefinementGeometry locks REQUEST 11 items 3+4: the xxl(22px)
// radius step, padding=/padx=/pady= on card+stack+button (space token
// or pixel integer), and icon-only buttons dropping to square padding
// so radius=full yields a circle.
func TestRefinementGeometry(t *testing.T) {
	page := renderSource(t, `view App =
  stack gap=1 padding=md
    card tone=primary radius=xxl padx=18 pady=12
      text "pill"
    button "Send" tone=primary
`)
	// xxl ships in the scale and resolves on the card.
	assertPageContains(t, page, "--radius-xxl: 22px;")
	assertPageContains(t, page, "border-radius:var(--radius-xxl);")
	// Pixel paddings pass through; token paddings keep the var.
	assertPageContains(t, page, "padding-left:18px;padding-right:18px;")
	assertPageContains(t, page, "padding-top:12px;padding-bottom:12px;")
	assertPageContains(t, page, "padding:var(--space-md);")
	// The labeled button keeps the wide md inline padding (the
	// icon-only square-padding rule is covered in pkg/style).
	assertPageContains(t, page, "padding-left:var(--space-md);padding-right:var(--space-md);")
}

// TestTextTrackingAndCaps locks REQUEST 11 item 5: `tracking <n>` (in
// 1/100 em) and `caps` on theme text bindings, flowing through
// per-token vars into the font atomic class.
func TestTextTrackingAndCaps(t *testing.T) {
	page := renderSource(t, `theme T =
  text label = "Spline Sans Mono" 11 500 tracking 10 caps

view App =
  text "chat name" size=label
`)
	assertPageContains(t, page, "--text-label-tracking: 0.1em;")
	assertPageContains(t, page, "--text-label-transform: uppercase;")
	// Untouched tokens keep neutral defaults.
	assertPageContains(t, page, "--text-body-tracking: normal;")
	assertPageContains(t, page, "--text-body-transform: none;")
	// The font class applies both.
	assertPageContains(t, page, "letter-spacing:var(--text-label-tracking);text-transform:var(--text-label-transform);")
}

// TestTrackingErrors locks the tracking guard rails.
func TestTrackingErrors(t *testing.T) {
	cases := []struct {
		name, src, wantErr string
	}{
		{"dangling tracking", `theme T =
  text label = "Mono" tracking

view V =
  text "x"
`, "needs an integer"},
		{"tracking out of range", `theme T =
  text label = 11 500 tracking 400

view V =
  text "x"
`, "tracking 400 out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = lower.Lower(root)
			if err == nil {
				t.Fatalf("expected lowering error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestModalPlacementVariants locks `modal side=bottom|left`: the
// backdrop carries a placement modifier class and the structural CSS
// ships the sheet/drawer geometry.
func TestModalPlacementVariants(t *testing.T) {
	page := renderSource(t, `view App =
  state sheetOpen = false
  state drawerOpen = false
  stack gap=1
    button "Settings" on click { sheetOpen = true }
    button "Menu" on click { drawerOpen = true }
  modal sheetOpen side=bottom
    text "settings"
  modal drawerOpen side=left
    text "modes"
`)
	assertPageContains(t, page, `"s-modal-backdrop s-modal-side-bottom"`)
	assertPageContains(t, page, `"s-modal-backdrop s-modal-side-left"`)
	assertPageContains(t, page, ".s-modal-backdrop.s-modal-side-bottom")
	assertPageContains(t, page, ".s-modal-side-left .s-modal-content")
	assertPageContains(t, page, "border-radius: var(--radius-xl) var(--radius-xl) 0 0;")
}

// TestPulsePrimitive locks the `pulse` working affordance: three
// aria-hidden dots, tone= recoloring via the Color slot, staggered
// keyframes with a reduced-motion fallback.
func TestPulsePrimitive(t *testing.T) {
	page := renderSource(t, `view App =
  stack gap=1
    pulse tone=accent
    text "Iris is thinking"
`)
	assertPageContains(t, page, "'<i></i><i></i><i></i>'")
	assertPageContains(t, page, "setAttribute('aria-hidden', 'true')")
	assertPageContains(t, page, `"s-pulse s-color-accent"`)
	assertPageContains(t, page, "@keyframes s-pulse")
	assertPageContains(t, page, "prefers-reduced-motion: reduce")
}

// TestModalSideErrors locks the closed side vocabulary.
func TestModalSideErrors(t *testing.T) {
	root, err := parser.Parse(`view V =
  state open = false
  text "x"
  modal open side=right
    text "y"
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = lower.Lower(root)
	if err == nil || !strings.Contains(err.Error(), "side= must be `bottom` (sheet) or `left` (drawer)") {
		t.Fatalf("expected side= vocabulary error, got: %v", err)
	}
}
