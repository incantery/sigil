package loader

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/theme"
)

// These tests lock the REQUEST 12 ask-2 contract: a sigil monorepo
// decomposes Go-style (root sigil.mod, packages anywhere below it),
// and *un-referenced* decls — theme, fonts — carry across imports
// into the merged program. The acceptance case is iris's login app
// and chat app sharing one Aura theme package (ui/theme).

// TestMergeCarriesThemeAndFontsAcrossImports: package B declares a
// theme and fonts; package A imports B but never references either
// by name (themes/fonts apply globally). Both must reach the merged
// doc A compiles to.
func TestMergeCarriesThemeAndFontsAcrossImports(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	// Nested two levels deep — the Go-style layout iris wants
	// (ui/theme), not a flat sibling.
	writeFile(t, filepath.Join(root, "ui", "theme"), "theme.sigil", `fonts google = "Spline Sans" "Instrument Serif"

theme Aura =
  primary = "#1d4ed8" on "#ffffff"
`)
	writeFile(t, filepath.Join(root, "app", "chat"), "chat.sigil", `import example.com/proj/ui/theme

view App =
  text "ok"
`)
	prog, err := Load(filepath.Join(root, "app", "chat"))
	if err != nil {
		t.Fatal(err)
	}
	module, err := prog.Merge()
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	doc, err := lower.Lower(module)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	var foundTheme bool
	for _, th := range doc.Themes {
		if t2, ok := th.(*theme.Theme); ok && t2.Name == "Aura" {
			foundTheme = true
		}
	}
	if !foundTheme {
		t.Errorf("theme Aura from imported package missing from doc.Themes (got %d themes)", len(doc.Themes))
	}
	if len(doc.Fonts) != 1 {
		t.Fatalf("want 1 font source from imported package, got %d", len(doc.Fonts))
	}
	if got := strings.Join(doc.Fonts[0].Families, ","); got != "Spline Sans,Instrument Serif" {
		t.Errorf("families = %q", got)
	}
}

// TestMergeUnionsFontFamiliesAcrossPackages: two packages each
// declare `fonts google` with an overlapping family. The lowered doc
// must carry one source with the union, not duplicates — a shared
// family loads once.
func TestMergeUnionsFontFamiliesAcrossPackages(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, filepath.Join(root, "ui", "theme"), "theme.sigil", `fonts google = "Spline Sans" "Spline Sans Mono"

theme Aura =
  primary = "#1d4ed8" on "#ffffff"
`)
	writeFile(t, root, "main.sigil", `import example.com/proj/ui/theme

fonts google = "Spline Sans" "Instrument Serif"

view App =
  text "ok"
`)
	prog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	module, err := prog.Merge()
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	doc, err := lower.Lower(module)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if len(doc.Fonts) != 1 {
		t.Fatalf("want 1 unioned font source, got %d", len(doc.Fonts))
	}
	want := "Spline Sans,Spline Sans Mono,Instrument Serif"
	if got := strings.Join(doc.Fonts[0].Families, ","); got != want {
		t.Errorf("families = %q, want %q (deduped union, decl order)", got, want)
	}
}

// TestMergeCrossPackageComponent: a component declared in an
// imported package is usable via its dotted name; the merge rewrite
// plus lower-time inlining must compose.
func TestMergeCrossPackageComponent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, filepath.Join(root, "ui", "components"), "pill.sigil", `component Pill -> label =
  card
    text label
`)
	writeFile(t, filepath.Join(root, "app", "chat"), "chat.sigil", `import example.com/proj/ui/components

view App =
  components.Pill "hello"
`)
	prog, err := Load(filepath.Join(root, "app", "chat"))
	if err != nil {
		t.Fatal(err)
	}
	module, err := prog.Merge()
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if _, err := lower.Lower(module); err != nil {
		t.Fatalf("Lower (cross-package component should inline): %v", err)
	}
}

// TestMergeRejectsCrossPackageStreamCollision: ops in different
// packages share one global namespace — two `stream Chat` decls must
// collide at merge time, same as query/command.
func TestMergeRejectsCrossPackageStreamCollision(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, filepath.Join(root, "a"), "a.sigil", `stream Chat -> prompt : String = String
`)
	writeFile(t, filepath.Join(root, "b"), "b.sigil", `stream Chat -> prompt : String = String
`)
	writeFile(t, root, "main.sigil", `import example.com/proj/a
import example.com/proj/b

view App =
  text "ok"
`)
	prog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prog.Merge(); err == nil {
		t.Fatal("expected stream name collision error")
	} else if !strings.Contains(err.Error(), "declared in both") {
		t.Fatalf("err = %v", err)
	}
}

// TestMergeRejectsCrossPackageBackendCollision: same rule for
// backend decls.
func TestMergeRejectsCrossPackageBackendCollision(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	backend := `backend Api =
  url same-origin
  auth none
`
	writeFile(t, filepath.Join(root, "a"), "a.sigil", backend)
	writeFile(t, filepath.Join(root, "b"), "b.sigil", backend)
	writeFile(t, root, "main.sigil", `import example.com/proj/a
import example.com/proj/b

view App =
  text "ok"
`)
	prog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prog.Merge(); err == nil {
		t.Fatal("expected backend name collision error")
	} else if !strings.Contains(err.Error(), "declared in both") {
		t.Fatalf("err = %v", err)
	}
}
