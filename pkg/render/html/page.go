package html

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/incantery/mako/pkg/codegen"
	"github.com/incantery/mako/pkg/ir"
	"github.com/incantery/mako/pkg/theme"
	"github.com/incantery/mako/pkg/ui"
)

// WriteDoc emits a complete HTML document from an already-lowered IR
// document. This is the path .mako sources flow through (cells come
// from the source's `state` decls, not the Go ui package's global
// registry).
//
// Dispatch: pkg/codegen emits per-app JS containing exactly the
// closures this app needs — no general runtime ships. If the doc uses
// an IR shape codegen doesn't (yet) support, WriteDoc returns a
// compile error. Add the missing shape to the codegen profile +
// emitter; there is no fallback path.
//
// Styling pipeline:
//
//	<style>
//	  {theme vars}        — every --color-*, --space-*, --text-* per theme
//	  {reset.css}         — headless baseline, normalizes the UA
//	  {atomic classes}    — only the (slot,token) pairs used in this render
//	  {structural.css}    — renderer-internal geometry + interactions
//	</style>
//
// Authors never inject CSS; primitives never hand-roll style attrs.
// Every visual decision flows through style.SpecFor → resolver →
// atomic class. See L51 commit for the architectural rationale.
func WriteDoc(w io.Writer, title string, doc ir.Document) error {
	page, err := ComposeDoc(title, doc)
	if err != nil {
		return err
	}
	_, err = w.Write(page.Bytes)
	return err
}

// ComposedPage is one fully rendered page plus the exact inline
// segments it embeds. Serving layers (pkg/serve) hash InlineScript /
// InlineStyle to build a Content-Security-Policy that admits exactly
// this page's inline code and nothing else.
type ComposedPage struct {
	Bytes        []byte
	InlineScript string // the <script> element's exact text content
	InlineStyle  string // the <style> element's exact text content
}

// ComposeDoc renders the full page for a compiled document and
// returns it with its inline segments. WriteDoc is the plain-writer
// wrapper.
func ComposeDoc(title string, doc ir.Document) (*ComposedPage, error) {
	ok, reason := codegen.Profile(doc)
	if !ok {
		return nil, fmt.Errorf("sigil: doc uses IR shape outside the codegen profile: %s", reason)
	}

	light := theme.Light
	dark := theme.Dark
	for _, t := range doc.Themes {
		th, ok := t.(*theme.Theme)
		if !ok {
			continue
		}
		switch th.ExtendsName {
		case "dark":
			dark = *th
		default:
			light = *th
		}
	}
	// Text-scale tokens are palette-independent — a family/size/weight
	// has no light/dark variant — so a custom theme's type vocabulary
	// propagates to the other realization when only one side declares
	// it. Without this, `size=wordmark` would resolve to undefined vars
	// in whichever variant the author didn't restate.
	syncTextScale(&light, &dark)
	hcLight := theme.HighContrast.Apply(light)
	hcDark := theme.DarkHighContrast.Apply(dark)

	// Pre-pass: walk the IR tree to resolve CSS classes and track icon
	// usage. The SPA JS applies these classes during createElement;
	// the resolver tracks which atomic rules + icon symbols are needed.
	r := newResolver(doc.IconSets)
	classMap := BuildClassMap(doc.Root, r)

	var css strings.Builder
	writeThemeCSS(&css, light, dark, hcLight, hcDark)
	css.WriteString(resetCSS)
	css.WriteString(r.stylesheet())
	css.WriteString(structuralCSS)

	js := codegen.EmitSPA(doc, classMap)

	// Body is a styleless host: the mounted root element owns the
	// viewport unconditionally (codegen tags it .s-root + a mode
	// class — see structural.css). There is no per-page body state
	// left to decide here.
	bodyAttrs := ""

	// The inline segments are kept byte-identical between the page
	// bytes and the ComposedPage fields — a CSP hash over
	// InlineScript must match what the browser extracts from the
	// <script> element. Note the emitted page wraps both segments in
	// a leading/trailing newline; those newlines are part of the
	// element text, so they're part of the hashed segments too.
	inlineStyle := "\n" + css.String() + "\n  "
	inlineScript := "\n" + js + "\n"
	var buf bytes.Buffer
	fmt.Fprintf(&buf, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
  <title>%s</title>
%s  <style>%s</style>
</head>
<body%s>
%s
<script>%s</script>
</body>
</html>
`, title, fontLinks(doc, light, dark), inlineStyle, bodyAttrs, r.iconDefs(), inlineScript)
	return &ComposedPage{
		Bytes:        buf.Bytes(),
		InlineScript: inlineScript,
		InlineStyle:  inlineStyle,
	}, nil
}

// fontLinks renders the <link> tags that load declared web fonts. For
// the google provider this is the standard preconnect pair plus one
// css2 stylesheet URL. The weights/styles requested per family are
// computed from the text-scale entries (across the light and dark
// realizations) that reference that family — the fonts decl never
// repeats them. Returns "" when the doc declares no fonts.
func fontLinks(doc ir.Document, themes ...theme.Theme) string {
	var families []string
	for _, f := range doc.Fonts {
		if f.Provider != "google" {
			continue
		}
		families = append(families, f.Families...)
	}
	if len(families) == 0 {
		return ""
	}

	type variant struct {
		italic bool
		weight int
	}
	params := make([]string, 0, len(families))
	for _, fam := range families {
		seen := map[variant]bool{}
		var variants []variant
		for _, t := range themes {
			for _, ts := range t.TextScale {
				if ts.Family != fam {
					continue
				}
				v := variant{italic: ts.Italic, weight: ts.Weight}
				if v.weight == 0 {
					v.weight = 400
				}
				if !seen[v] {
					seen[v] = true
					variants = append(variants, v)
				}
			}
		}
		sort.Slice(variants, func(i, j int) bool {
			if variants[i].italic != variants[j].italic {
				return !variants[i].italic
			}
			return variants[i].weight < variants[j].weight
		})
		name := strings.ReplaceAll(fam, " ", "+")
		switch {
		case len(variants) == 0:
			// Declared but unreferenced — load the regular cut.
			params = append(params, "family="+name)
		default:
			hasItalic := false
			for _, v := range variants {
				if v.italic {
					hasItalic = true
					break
				}
			}
			var spec strings.Builder
			if hasItalic {
				spec.WriteString(":ital,wght@")
				for i, v := range variants {
					if i > 0 {
						spec.WriteString(";")
					}
					ital := "0"
					if v.italic {
						ital = "1"
					}
					fmt.Fprintf(&spec, "%s,%d", ital, v.weight)
				}
			} else {
				spec.WriteString(":wght@")
				for i, v := range variants {
					if i > 0 {
						spec.WriteString(";")
					}
					fmt.Fprintf(&spec, "%d", v.weight)
				}
			}
			params = append(params, "family="+name+spec.String())
		}
	}

	return "  <link rel=\"preconnect\" href=\"https://fonts.googleapis.com\">\n" +
		"  <link rel=\"preconnect\" href=\"https://fonts.gstatic.com\" crossorigin>\n" +
		"  <link href=\"https://fonts.googleapis.com/css2?" + strings.Join(params, "&amp;") +
		"&amp;display=swap\" rel=\"stylesheet\">\n"
}

// syncTextScale copies text-scale entries that exist in one variant
// but not the other, both directions. Maps are re-allocated before
// mutation so the process-wide default themes are never written to.
func syncTextScale(a, b *theme.Theme) {
	copyMissing := func(dst, src *theme.Theme) {
		var fresh map[string]theme.TextStyle
		for k, v := range src.TextScale {
			if _, ok := dst.TextScale[k]; ok {
				continue
			}
			if fresh == nil {
				fresh = make(map[string]theme.TextStyle, len(dst.TextScale)+1)
				for dk, dv := range dst.TextScale {
					fresh[dk] = dv
				}
			}
			fresh[k] = v
		}
		if fresh != nil {
			dst.TextScale = fresh
		}
	}
	copyMissing(a, b)
	copyMissing(b, a)
}

// WritePage is the Go-side authoring path: takes a ui.Component, runs Compile
// (which reads the package-level cell registry), and writes the page. Kept
// for the existing examples under examples/*/main.go.
func WritePage(w io.Writer, title string, root ui.Component) error {
	return WriteDoc(w, title, Compile(root))
}
