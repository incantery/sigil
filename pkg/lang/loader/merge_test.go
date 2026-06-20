package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeSinglePackage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, root, "main.sigil", `view App =
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
	if module.Kind != "module" {
		t.Fatalf("Kind = %s", module.Kind)
	}
	if len(module.Children) != 1 {
		t.Fatalf("want 1 child, got %d", len(module.Children))
	}
	if module.Children[0].Kind != "view" {
		t.Fatalf("child Kind = %s", module.Children[0].Kind)
	}
}

func TestMergeImportRewritesDottedTypeRef(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, filepath.Join(root, "types"), "types.sigil", `type Slot =
  id : Int
`)
	writeFile(t, root, "main.sigil", `import example.com/proj/types

view App =
  state s : types.Slot
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
	// Find the state decl in the merged tree, verify it now
	// references `Slot` (bare), not `types.Slot`.
	var found bool
	for _, c := range module.Children {
		if c.Kind != "view" {
			continue
		}
		for _, body := range c.Children {
			if body.Kind == "state" && len(body.Args) >= 2 {
				if body.Args[1].String == "Slot" {
					found = true
				}
				if body.Args[1].String == "types.Slot" {
					t.Errorf("state ref still dotted: %q", body.Args[1].String)
				}
			}
		}
	}
	if !found {
		t.Fatal("did not find expected state decl in merged tree")
	}
	// import nodes are gone
	for _, c := range module.Children {
		if c.Kind == "import" {
			t.Errorf("import node leaked into merged tree: %v", c)
		}
	}
}

func TestMergeRejectsGlobalNameCollision(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, filepath.Join(root, "a"), "a.sigil", `type Slot =
  id : Int
`)
	writeFile(t, filepath.Join(root, "b"), "b.sigil", `type Slot =
  id : Int
`)
	writeFile(t, root, "main.sigil", `import example.com/proj/a
import example.com/proj/b
view App = text "ok"
`)
	prog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prog.Merge()
	if err == nil {
		t.Fatal("expected name collision error")
	}
	if !strings.Contains(err.Error(), "declared in both") {
		t.Fatalf("err = %v", err)
	}
}

func TestMergeRejectsUnknownDottedRef(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	writeFile(t, filepath.Join(root, "types"), "types.sigil", `type Slot =
  id : Int
`)
	writeFile(t, root, "main.sigil", `import example.com/proj/types

view App =
  state s : types.NotARealType
  text "ok"
`)
	prog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prog.Merge()
	if err == nil {
		t.Fatal("expected error for unknown dotted ref")
	}
	if !strings.Contains(err.Error(), "NotARealType") {
		t.Fatalf("err = %v", err)
	}
}

func TestMergePreservesCellFieldRefs(t *testing.T) {
	// `featured.id` is a cell-field ref, not a package ref —
	// suffix starts lowercase so the rewriter must leave it alone.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sigil.mod"),
		[]byte("module example.com/proj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "main.sigil", `type Slot =
  id : Int

view App =
  state featured : Slot
  text featured.id
`)
	prog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prog.Merge()
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
}
