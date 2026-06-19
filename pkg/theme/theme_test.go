package theme

import (
	"math"
	"strings"
	"testing"
)

// TestContrastRatioReferences cross-checks our luminance/contrast math
// against WCAG's published reference pairs. If any of these drift, every
// theme audit downstream is wrong.
func TestContrastRatioReferences(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"#000000", "#ffffff", 21.0},  // max contrast
		{"#ffffff", "#ffffff", 1.0},   // identical
		{"#777777", "#ffffff", 4.48},  // borderline AA
		{"#18181b", "#ffffff", 17.72}, // our default primary
		{"#dc2626", "#ffffff", 4.83},  // our default danger
	}
	for _, c := range cases {
		got, err := ContrastRatio(c.a, c.b)
		if err != nil {
			t.Fatalf("contrast(%s,%s): %v", c.a, c.b, err)
		}
		if math.Abs(got-c.want) > 0.05 {
			t.Errorf("contrast(%s,%s) = %.2f, want %.2f", c.a, c.b, got, c.want)
		}
	}
}

// TestDefaultThemesValidate is the canary that catches a future maintainer
// shipping a default theme with an inaccessible pair. CI fails before the
// theme can be merged.
func TestDefaultThemesValidate(t *testing.T) {
	cases := []struct {
		name  string
		theme Theme
	}{
		{"light", Light},
		{"dark", Dark},
		{"light+high-contrast", HighContrast.Apply(Light)},
		{"dark+dark-high-contrast", DarkHighContrast.Apply(Dark)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.theme.Validate(); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
		})
	}
}

// TestValidateRejectsLowContrast confirms the validator actually fails
// on a bad pair (otherwise the canary above is meaningless).
func TestValidateRejectsLowContrast(t *testing.T) {
	bad := Theme{
		Name: "bad",
		Tones: map[string]ColorPair{
			"primary": {BG: "#888888", FG: "#aaaaaa"}, // ~1.5:1 ratio
		},
	}
	err := bad.Validate()
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !strings.Contains(err.Error(), "WCAG AA") {
		t.Fatalf("want WCAG AA error, got %q", err)
	}
}

// TestModifierComposition checks that Apply produces a theme whose set
// fields come from the modifier and unset fields come from the base.
func TestModifierComposition(t *testing.T) {
	out := HighContrast.Apply(Light)

	// HighContrast overrides "primary"
	if out.Tones["primary"].BG != "#000000" {
		t.Errorf("primary BG: want #000000, got %s", out.Tones["primary"].BG)
	}
	// Light defines spacing.md; HighContrast doesn't touch it; result preserves.
	if out.Spacing["md"] != 16 {
		t.Errorf("spacing.md: want 16, got %d", out.Spacing["md"])
	}
	// HighContrast bumps BorderPx to 2.
	if out.BorderPx != 2 {
		t.Errorf("BorderPx: want 2, got %d", out.BorderPx)
	}
	// Modifier doesn't mutate base.
	if Light.BorderPx != 1 {
		t.Errorf("base mutated: Light.BorderPx = %d", Light.BorderPx)
	}
}

// TestExtendsDelta confirms a derived theme inherits unset values and
// replaces set values, including a nested map merge.
func TestExtendsDelta(t *testing.T) {
	// Dark extends Light. Dark redefines tones but doesn't touch spacing,
	// so spacing must come through unchanged.
	if Dark.Spacing["md"] != Light.Spacing["md"] {
		t.Errorf("spacing.md should be inherited; light=%d dark=%d",
			Light.Spacing["md"], Dark.Spacing["md"])
	}
	if Dark.Tones["surface"].BG != "#18181b" {
		t.Errorf("dark surface BG: want #18181b, got %s", Dark.Tones["surface"].BG)
	}
	// Light is unchanged after Dark is computed.
	if Light.Tones["surface"].BG != "#ffffff" {
		t.Errorf("light mutated: surface BG = %s", Light.Tones["surface"].BG)
	}
}
