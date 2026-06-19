// Package icons discovers and validates SVG assets for a Sigil icon
// set. Sigil ships zero curated icons — every project brings its own
// vocabulary by declaring an `icons` set that points at a folder of
// `.svg` files. The compiler reads the folder at compile time,
// validates each asset against the headless-color contract, and
// hands the renderer a typed registry.
//
// Validation enforced (compile-time errors):
//
//   - root element is <svg>
//   - <svg> has a viewBox attribute
//   - no hardcoded fill/stroke colors (must be one of: unset, "none",
//     "currentColor", "transparent") — the headless contract is
//     "icon inherits color via currentColor cascade"
//   - no <script>, <foreignObject>, or <style> elements (security +
//     portability)
//   - no external <use href="https://..."> or <image href="https://...">
//     references (must be local or inline)
//
// The headless-color rule catches the most common Figma-export bug:
// designers leave fill="#000000" or fill="#212121" baked into the
// path, which silently breaks tone overrides because the SVG ignores
// the parent's color.
//
// Folder walking:
//
//   - Only `.svg` files are read; everything else (README.md,
//     screenshots, JSON manifests) is ignored — designers can drop
//     auxiliary files in the same directory without breaking the
//     walk.
//   - Files starting with `_` or `.` are skipped (working copies /
//     hidden files).
//   - Subdirectories produce dotted icon names: `foo/bar.svg`
//     registers as `foo.bar` in the set, giving authors a way to
//     group related icons without flat-naming everything.
package icons

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Asset is one icon's per-target payload. v1 carries only the web
// (SVG) realization; future targets add fields here (e.g.
// `IOSSymbol string` for SF Symbols).
type Asset struct {
	// ViewBox is the value of the <svg viewBox="..."> attribute.
	// Preserved separately so the HTML renderer can emit a
	// <symbol viewBox="..."> + <use> pair (the dedupe pattern).
	ViewBox string

	// Inner is the inner XML of the <svg> root — child elements
	// only, with the outer <svg ...></svg> stripped. The renderer
	// re-wraps this in its target-appropriate envelope.
	Inner string
}

// Load discovers every icon under dir, validates each, and returns
// a name → Asset map. Subdirectories produce dotted names
// (`foo/bar.svg` → `foo.bar`). Files starting with `_` or `.` are
// skipped; non-`.svg` files are silently ignored so screenshots and
// READMEs can sit alongside source assets.
//
// Returns the union of all errors encountered (one per icon),
// formatted as a single multi-line message — the compiler prints
// every failed icon, not just the first, so authors can fix a
// folder in one pass.
func Load(dir string) (map[string]Asset, error) {
	out := map[string]Asset{}
	var problems []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip dot- and underscore-prefixed dirs entirely —
			// matches Go's convention and prevents `_drafts/` or
			// `.git/` from polluting the icon set.
			name := d.Name()
			if path != dir && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			return nil
		}
		if !strings.HasSuffix(name, ".svg") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		asset, err := Validate(raw)
		iconName := iconNameFor(dir, path)
		if err != nil {
			problems = append(problems,
				fmt.Sprintf("  %s: %s", iconName, err.Error()))
			return nil
		}
		if existing, dup := out[iconName]; dup {
			_ = existing
			problems = append(problems,
				fmt.Sprintf("  %s: declared more than once in %s", iconName, dir))
			return nil
		}
		out[iconName] = asset
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("icon set %s has %d problem(s):\n%s",
			dir, len(problems), strings.Join(problems, "\n"))
	}
	return out, nil
}

// iconNameFor turns an absolute file path into its registered icon
// name, relative to the icon-set root. `dir/foo/bar.svg` → `foo.bar`.
// Subfolders join via `.` (we don't allow nested-subfolder names
// past one level in v1; the loader doesn't enforce this, but the
// convention is to keep namespaces shallow).
func iconNameFor(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	rel = strings.TrimSuffix(rel, ".svg")
	rel = filepath.ToSlash(rel)
	return strings.ReplaceAll(rel, "/", ".")
}

// Validate parses one SVG and enforces the rules described at the
// package level. Returns the extracted Asset on success or a single
// concrete error on any failure. The error message points at the
// specific problem (which attribute, which element) so authors can
// fix the file directly.
func Validate(svg []byte) (Asset, error) {
	dec := xml.NewDecoder(bytes.NewReader(svg))
	dec.Strict = false // tolerate `<svg ... xmlns:xlink="..."/>` and other Figma exports
	var (
		root       xml.StartElement
		seenRoot   bool
		viewBox    string
		innerBuf   bytes.Buffer
		innerEnc   = xml.NewEncoder(&innerBuf)
		innerDepth int
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			// io.EOF check via constant comparison without importing io
			// would cleaner; xml's Decoder.Token wraps io.EOF, just exit.
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if !seenRoot {
				if t.Name.Local != "svg" {
					return Asset{}, fmt.Errorf("root element is <%s>, expected <svg>", t.Name.Local)
				}
				root = t
				seenRoot = true
				for _, a := range t.Attr {
					if a.Name.Local == "viewBox" {
						viewBox = a.Value
					}
				}
				if viewBox == "" {
					return Asset{}, fmt.Errorf("<svg> is missing viewBox attribute")
				}
				continue
			}
			if err := validateElement(t); err != nil {
				return Asset{}, err
			}
			if err := innerEnc.EncodeToken(t); err != nil {
				return Asset{}, fmt.Errorf("re-encode element: %w", err)
			}
			innerDepth++
		case xml.EndElement:
			if t.Name.Local == "svg" && innerDepth == 0 {
				continue
			}
			if err := innerEnc.EncodeToken(t); err != nil {
				return Asset{}, fmt.Errorf("re-encode close: %w", err)
			}
			innerDepth--
		case xml.CharData:
			if seenRoot && innerDepth >= 0 {
				if err := innerEnc.EncodeToken(t.Copy()); err != nil {
					return Asset{}, fmt.Errorf("re-encode chardata: %w", err)
				}
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			// Drop comments / processing instructions / doctype on the
			// inner content; they're cosmetic and not needed by the
			// renderer.
		}
		_ = root
	}
	if !seenRoot {
		return Asset{}, fmt.Errorf("no <svg> root element found")
	}
	if err := innerEnc.Flush(); err != nil {
		return Asset{}, err
	}
	return Asset{
		ViewBox: viewBox,
		Inner:   strings.TrimSpace(innerBuf.String()),
	}, nil
}

// validateElement enforces the per-element rules: rejected element
// names + the hardcoded-color check on fill/stroke attributes.
// Other attributes pass through untouched so authors can use stroke
// widths, opacities, transforms, etc.
func validateElement(t xml.StartElement) error {
	switch t.Name.Local {
	case "script":
		return fmt.Errorf("<script> elements are rejected (security)")
	case "foreignObject":
		return fmt.Errorf("<foreignObject> is rejected (portability)")
	case "style":
		return fmt.Errorf("<style> elements are rejected; use currentColor + Spec slots for theming")
	case "image":
		for _, a := range t.Attr {
			if a.Name.Local == "href" || a.Name.Local == "xlink:href" {
				if isExternalHref(a.Value) {
					return fmt.Errorf("<image> with external href %q is rejected", a.Value)
				}
			}
		}
	case "use":
		for _, a := range t.Attr {
			if a.Name.Local == "href" || a.Name.Local == "xlink:href" {
				if isExternalHref(a.Value) {
					return fmt.Errorf("<use> with external href %q is rejected", a.Value)
				}
			}
		}
	}
	for _, a := range t.Attr {
		if a.Name.Local == "fill" || a.Name.Local == "stroke" {
			if !isAllowedPaintValue(a.Value) {
				return fmt.Errorf(
					"<%s> has hardcoded %s=%q — icons must use currentColor (or none/transparent) so tone overrides apply",
					t.Name.Local, a.Name.Local, a.Value)
			}
		}
	}
	return nil
}

// isAllowedPaintValue checks one fill/stroke value against the
// headless-color allow-list. Only currentColor / none / transparent
// are permitted; any concrete color (named, hex, rgb(), …) is a
// rejection.
func isAllowedPaintValue(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	switch v {
	case "", "none", "currentcolor", "transparent", "inherit":
		return true
	}
	return false
}

// isExternalHref returns true when a `<use href>` or `<image href>`
// value points outside the local document. Local `#fragment`
// references are allowed (you can have multi-symbol files); URL or
// path references are rejected.
func isExternalHref(v string) bool {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "#") {
		return false
	}
	if strings.HasPrefix(v, "data:") {
		return false
	}
	return true
}
