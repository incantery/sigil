package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a test helper that writes contents to dir/name.
func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSinglePackage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, root, "main.sigil", `view App =
  text "hi"
`)
	prog, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if prog.Entry != "example.com/proj" {
		t.Fatalf("Entry = %q", prog.Entry)
	}
	if len(prog.Packages) != 1 {
		t.Fatalf("want 1 package, got %d", len(prog.Packages))
	}
	if len(prog.Order) != 1 || prog.Order[0] != "example.com/proj" {
		t.Fatalf("Order = %v", prog.Order)
	}
}

func TestLoadMultiFilePackage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, root, "a.sigil", `type Foo =
  x : Int
`)
	writeFile(t, root, "b.sigil", `type Bar =
  y : Int
`)
	writeFile(t, root, "c.sigil", `view App =
  text "hi"
`)
	prog, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pkg := prog.Packages["example.com/proj"]
	if pkg == nil {
		t.Fatal("missing entry package")
	}
	if len(pkg.Files) != 3 {
		t.Fatalf("want 3 files, got %d", len(pkg.Files))
	}
	// Files sorted alphabetically by path.
	for i, want := range []string{"a.sigil", "b.sigil", "c.sigil"} {
		got := filepath.Base(pkg.Files[i].Path)
		if got != want {
			t.Errorf("file %d: got %s, want %s", i, got, want)
		}
	}
}

func TestLoadCrossPackageImports(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, filepath.Join(root, "types"), "types.sigil", `type Slot =
  id : Int
`)
	writeFile(t, root, "main.sigil", `import example.com/proj/types
view App =
  text "hi"
`)
	prog, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prog.Packages) != 2 {
		t.Fatalf("want 2 packages, got %d", len(prog.Packages))
	}
	if _, ok := prog.Packages["example.com/proj/types"]; !ok {
		t.Fatal("missing types package")
	}
	// Topological order: dependency before dependent.
	if prog.Order[0] != "example.com/proj/types" || prog.Order[1] != "example.com/proj" {
		t.Fatalf("Order = %v", prog.Order)
	}
	// Imports recorded on the main file with the default alias.
	main := prog.Packages["example.com/proj"].Files[0]
	if main.Imports["types"] != "example.com/proj/types" {
		t.Fatalf("Imports = %v", main.Imports)
	}
}

func TestLoadImportAlias(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, filepath.Join(root, "types"), "types.sigil", `type Slot =
  id : Int
`)
	writeFile(t, root, "main.sigil", `import example.com/proj/types as t
view App =
  text "hi"
`)
	prog, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	main := prog.Packages["example.com/proj"].Files[0]
	if main.Imports["t"] != "example.com/proj/types" {
		t.Fatalf("Imports = %v", main.Imports)
	}
}

func TestLoadExternalImportRejected(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, root, "main.sigil", `import github.com/someone/else
view App = text "hi"
`)
	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for external import")
	}
	if !strings.Contains(err.Error(), "external modules not yet supported") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadDetectsCycle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, filepath.Join(root, "a"), "a.sigil", `import example.com/proj/b
type T =
  a : Int
`)
	writeFile(t, filepath.Join(root, "b"), "b.sigil", `import example.com/proj/a
type U =
  b : Int
`)
	writeFile(t, root, "main.sigil", `import example.com/proj/a
view App = text "hi"
`)
	_, err := Load(root)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "import cycle") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadIgnoresUnderscorePrefixedAndNonSigil(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, root, "main.sigil", `view App =
  text "hi"
`)
	// These should all be ignored.
	writeFile(t, root, "_scratch.sigil", "garbage that would fail to parse !@#$%")
	writeFile(t, root, "README.md", "# project notes")
	writeFile(t, root, ".hidden", "do not look")
	prog, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pkg := prog.Packages["example.com/proj"]
	if len(pkg.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(pkg.Files))
	}
}

func TestLoadEmptyPackageErrors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, filepath.Join(root, "empty"), "README.md", "no sigil files here")
	writeFile(t, root, "main.sigil", `import example.com/proj/empty
view App = text "hi"
`)
	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for empty package")
	}
	if !strings.Contains(err.Error(), "no .sigil files") {
		t.Fatalf("err = %v", err)
	}
}

func TestDefaultAlias(t *testing.T) {
	cases := map[string]string{
		"github.com/seth/pokedex/types": "types",
		"example.com/x":                 "x",
		"foo":                           "foo",
	}
	for in, want := range cases {
		if got := defaultAlias(in); got != want {
			t.Errorf("defaultAlias(%q) = %q, want %q", in, got, want)
		}
	}
}
