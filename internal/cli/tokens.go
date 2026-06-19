package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/incantery/mako/pkg/theme"
)

var tokensTheme string

var tokensCmd = &cobra.Command{
	Use:   "tokens",
	Short: "List the design tokens available in a Sigil theme",
	Long: `Prints every spacing, color, text, radius, and border token in the
named theme. Use this to remind yourself what scale to compose
from before editing a .mako source — Sigil intentionally has a
small closed set of tokens; this is how you see all of them at
once.

  sigil tokens             # the default light theme
  sigil tokens --theme dark
  sigil tokens --theme high-contrast
  sigil tokens --theme dark-high-contrast

Output is plain text, two columns, grouped by category.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		th, ok := resolveTheme(tokensTheme)
		if !ok {
			return fmt.Errorf("unknown theme %q (try: light, dark, high-contrast, dark-high-contrast)", tokensTheme)
		}
		writeTokens(cmd.OutOrStdout(), th)
		return nil
	},
}

// resolveTheme maps a flag value to one of the four named themes
// the theme package ships with. New themes added there should be
// reflected here too; the error message above lists the closed
// set on a typo.
func resolveTheme(name string) (theme.Theme, bool) {
	switch name {
	case "light", "":
		return theme.Light, true
	case "dark":
		return theme.Dark, true
	case "high-contrast":
		return theme.HighContrast.Apply(theme.Light), true
	case "dark-high-contrast":
		return theme.DarkHighContrast.Apply(theme.Dark), true
	}
	return theme.Theme{}, false
}

// writeTokens dumps the theme's resolved values in a stable order
// per category. Categories print in the order an author thinks
// about them (spacing → colors → text → shapes) so the output
// reads top-to-bottom like a cheat sheet.
func writeTokens(w io.Writer, th theme.Theme) {
	fmt.Fprintf(w, "theme: %s\n\n", th.Name)

	fmt.Fprintln(w, "spacing:")
	writeIntMap(w, th.Spacing, []string{"xs", "sm", "md", "lg", "xl"}, "px")

	fmt.Fprintln(w)
	fmt.Fprintln(w, "radii:")
	writeIntMap(w, th.Radii, []string{"none", "sm", "md", "lg", "full"}, "px")

	fmt.Fprintln(w)
	fmt.Fprintln(w, "border:")
	fmt.Fprintf(w, "  border-px = %dpx\n", th.BorderPx)
	fmt.Fprintf(w, "  outline   = %s\n", th.Outline)
	fmt.Fprintf(w, "  muted     = %s\n", th.Muted)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "text:")
	textOrder := []string{"caption", "body", "body-strong", "heading-sm", "heading-md", "heading-lg"}
	for _, k := range textOrder {
		if ts, ok := th.TextScale[k]; ok {
			fmt.Fprintf(w, "  %-14s = %dpx / %d\n", k, ts.Size, ts.Weight)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "tones (bg on fg):")
	// Print in the IntentTones order if it covers everything, then
	// alphabetically for any extras (custom themes can add tones).
	known := map[string]bool{}
	for _, t := range theme.IntentTones {
		known[t] = true
		if pair, ok := th.Tones[t]; ok {
			fmt.Fprintf(w, "  %-14s = %s on %s\n", t, pair.BG, pair.FG)
		}
	}
	extras := []string{}
	for k := range th.Tones {
		if !known[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)
	for _, k := range extras {
		pair := th.Tones[k]
		fmt.Fprintf(w, "  %-14s = %s on %s\n", k, pair.BG, pair.FG)
	}
}

// writeIntMap prints `key = value<suffix>` rows in `order`, then
// any keys not in `order` alphabetically. Keeps a small map's
// output stable + scan-friendly.
func writeIntMap(w io.Writer, m map[string]int, order []string, suffix string) {
	seen := map[string]bool{}
	for _, k := range order {
		seen[k] = true
		if v, ok := m[k]; ok {
			fmt.Fprintf(w, "  %-14s = %d%s\n", k, v, suffix)
		}
	}
	extras := []string{}
	for k := range m {
		if !seen[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)
	for _, k := range extras {
		fmt.Fprintf(w, "  %-14s = %d%s\n", k, m[k], suffix)
	}
}

// stderrTokensHelp surfaces the closed theme set the same way
// resolveTheme's error does; kept tiny so the binary stays lean.
var _ = strings.ToLower
var _ = os.Stdout

func init() {
	tokensCmd.Flags().StringVar(&tokensTheme, "theme", "light",
		"theme to inspect (light, dark, high-contrast, dark-high-contrast)")
}
