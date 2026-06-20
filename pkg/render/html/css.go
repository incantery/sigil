package html

import (
	"fmt"
	"sort"
	"strings"

	"github.com/incantery/sigil/pkg/theme"
)

// writeThemeCSS emits a stylesheet that defines all of Sigil's tokens as
// CSS custom properties for the active theme composition. The strategy:
//
//	:root { ...light tokens... }                  // default
//	@media (prefers-color-scheme: dark) {
//	  :root { ...dark tokens... }                 // honored unless overridden
//	}
//	@media (prefers-contrast: more) {
//	  :root { ...light + high-contrast... }
//	  @media (prefers-color-scheme: dark) {       // nested matches both
//	    :root { ...dark + high-contrast... }
//	  }
//	}
//	html[data-color-mode="dark"] :root { ...dark... }   // explicit override
//	html[data-color-mode="light"] :root { ...light... }
//	html[data-contrast="high"] :root { ...high-contrast on top of color... }
//
// Authoring discipline: every concrete value we use in primitive rendering
// comes via var(--token-name). Primitives don't know about themes; they
// just consume tokens. New themes = update the maps, no primitive change.
func writeThemeCSS(b *strings.Builder, light, dark theme.Theme, hcLight, hcDark theme.Theme) {
	// Default = light. (Browsers with no preference pick this; explicit
	// data-color-mode="light" reinforces it.)
	b.WriteString(":root {\n")
	emitThemeVars(b, light)
	b.WriteString("}\n")

	// System dark mode follows browser preference. Wrapped in a media
	// query so it doesn't override an explicit data-color-mode="light".
	b.WriteString("@media (prefers-color-scheme: dark) {\n")
	b.WriteString("  :root:not([data-color-mode=\"light\"]) {\n")
	emitThemeVarsIndent(b, dark, "    ")
	b.WriteString("  }\n")
	b.WriteString("}\n")

	// Explicit overrides — these win over the media-query-derived choice.
	b.WriteString("html[data-color-mode=\"dark\"] {\n")
	emitThemeVarsIndent(b, dark, "  ")
	b.WriteString("}\n")
	b.WriteString("html[data-color-mode=\"light\"] {\n")
	emitThemeVarsIndent(b, light, "  ")
	b.WriteString("}\n")

	// High-contrast modifier — both system signal and explicit attribute.
	b.WriteString("@media (prefers-contrast: more) {\n")
	b.WriteString("  :root:not([data-contrast=\"normal\"]) {\n")
	emitThemeVarsIndent(b, hcLight, "    ")
	b.WriteString("  }\n")
	b.WriteString("  @media (prefers-color-scheme: dark) {\n")
	b.WriteString("    :root:not([data-color-mode=\"light\"]):not([data-contrast=\"normal\"]) {\n")
	emitThemeVarsIndent(b, hcDark, "      ")
	b.WriteString("    }\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")
	b.WriteString("html[data-contrast=\"high\"] {\n")
	emitThemeVarsIndent(b, hcLight, "  ")
	b.WriteString("}\n")
	b.WriteString("html[data-color-mode=\"dark\"][data-contrast=\"high\"] {\n")
	emitThemeVarsIndent(b, hcDark, "  ")
	b.WriteString("}\n")

	// Note: box-sizing/body/button/divider rules live in reset.css
	// and structural.css. Anything that's not a `--token-*: value`
	// definition for a theme realization belongs there, not here.
}

// emitThemeVars writes every token in a theme as a `--name: value;` line.
// Keys are sorted so output is stable across builds (CSS doesn't care, but
// diffs do).
func emitThemeVars(b *strings.Builder, t theme.Theme) {
	emitThemeVarsIndent(b, t, "  ")
}

func emitThemeVarsIndent(b *strings.Builder, t theme.Theme, indent string) {
	// Color pairs
	toneKeys := sortedKeys(t.Tones)
	for _, k := range toneKeys {
		p := t.Tones[k]
		fmt.Fprintf(b, "%s--color-%s-bg: %s;\n", indent, k, p.BG)
		fmt.Fprintf(b, "%s--color-%s-fg: %s;\n", indent, k, p.FG)
	}
	// Outline / muted standalone colors
	if t.Outline != "" {
		fmt.Fprintf(b, "%s--color-outline: %s;\n", indent, t.Outline)
	}
	if t.Muted != "" {
		fmt.Fprintf(b, "%s--color-muted: %s;\n", indent, t.Muted)
	}

	// Spacing scale
	for _, k := range sortedKeys(t.Spacing) {
		fmt.Fprintf(b, "%s--space-%s: %dpx;\n", indent, k, t.Spacing[k])
	}

	// Radii
	for _, k := range sortedKeys(t.Radii) {
		fmt.Fprintf(b, "%s--radius-%s: %dpx;\n", indent, k, t.Radii[k])
	}

	// Text scale (size + weight as separate vars so consumers can mix).
	// Size includes the `px` unit so consumers can write
	// `font-size:var(--text-X-size)` without unit gymnastics — CSS var
	// substitution does NOT compose `var(...)` with a trailing unit
	// token, so the earlier `var(...)px` shorthand silently dropped
	// every heading-size declaration and the page fell back to body
	// size for headings AND buttons. Embedding the unit makes the var
	// fully self-contained.
	for _, k := range sortedKeys(t.TextScale) {
		ts := t.TextScale[k]
		fmt.Fprintf(b, "%s--text-%s-size: %dpx;\n", indent, k, ts.Size)
		fmt.Fprintf(b, "%s--text-%s-weight: %d;\n", indent, k, ts.Weight)
		// Family + style vars are always emitted so the font atomic
		// class can reference them unconditionally; tokens without an
		// explicit family resolve through the app-wide stack.
		if ts.Family != "" {
			fmt.Fprintf(b, "%s--text-%s-family: %q, var(--font-family);\n", indent, k, ts.Family)
		} else {
			fmt.Fprintf(b, "%s--text-%s-family: var(--font-family);\n", indent, k)
		}
		style := "normal"
		if ts.Italic {
			style = "italic"
		}
		fmt.Fprintf(b, "%s--text-%s-style: %s;\n", indent, k, style)
		// Tracking (1/100 em) + caps transform — the mono-label
		// treatment. Defaults keep every token visually unchanged.
		if ts.Tracking != 0 {
			fmt.Fprintf(b, "%s--text-%s-tracking: %s;\n", indent, k, formatTracking(ts.Tracking))
		} else {
			fmt.Fprintf(b, "%s--text-%s-tracking: normal;\n", indent, k)
		}
		transform := "none"
		if ts.Caps {
			transform = "uppercase"
		}
		fmt.Fprintf(b, "%s--text-%s-transform: %s;\n", indent, k, transform)
	}

	// Border default
	if t.BorderPx > 0 {
		fmt.Fprintf(b, "%s--border-px: %dpx;\n", indent, t.BorderPx)
	}

	// Elevations (box-shadows assembled from structured tokens).
	// Skipping empty (flat) entries — they'd emit `box-shadow: 0 0
	// 0 0 rgba(0,0,0,0)` which is just noise; the renderer's flat
	// path doesn't reference the var either.
	for _, k := range sortedKeys(t.Elevations) {
		e := t.Elevations[k]
		if (e == theme.Elevation{}) {
			continue
		}
		fmt.Fprintf(b, "%s--elevation-%s: 0px %dpx %dpx %dpx rgba(0,0,0,%g);\n",
			indent, k, e.Y, e.Blur, e.Spread, e.Opacity)
	}

	// Container widths.
	for _, k := range sortedKeys(t.ContainerWidths) {
		fmt.Fprintf(b, "%s--container-width-%s: %dpx;\n", indent, k, t.ContainerWidths[k])
	}

	// Font family — fixed for v0 (system stack with fallbacks)
	fmt.Fprintf(b, "%s--font-family: system-ui, -apple-system, sans-serif;\n", indent)
}

// formatTracking renders a TextStyle.Tracking (1/100 em) as a CSS
// length: 10 → "0.1em", -2 → "-0.02em".
func formatTracking(t int) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", float64(t)/100), "0"), ".") + "em"
}

// sortedKeys returns map keys in sorted order. Generic helper so the same
// code handles `map[string]ColorPair`, `map[string]int`, etc.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
