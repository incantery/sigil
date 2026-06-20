// Package loader resolves a Sigil program from a starting file or
// directory: it locates sigil.mod, discovers the entry package and
// every package it transitively imports, parses each file, and
// returns a Program ready for lowering.
//
// A Package corresponds to one directory of .sigil files. All files
// in a package share top-level scope (Go-style); each file has its
// own import list (also Go-style — different files in the same
// package can import the same module under different aliases).
//
// Packages are addressed by their import path
// (`github.com/seth/pokedex/types`). External imports (paths outside
// the current module's prefix) are a hard error in v1 — `sigil get`
// will lift the restriction later.
//
// Cycle detection runs during package discovery; a cycle errors with
// the full cycle path so authors see where the loop closed.
package loader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/incantery/sigil/pkg/lang/ast"
	"github.com/incantery/sigil/pkg/lang/diag"
	"github.com/incantery/sigil/pkg/lang/parser"
	"github.com/incantery/sigil/pkg/lang/sigilmod"
)

// File is one parsed .sigil file. Imports are extracted from the AST
// so the lowerer can resolve dotted names without re-walking. The
// original AST is preserved for lowering.
type File struct {
	Path    string            // absolute path on disk
	AST     *ast.Node         // root node from parser
	Imports map[string]string // alias -> import path
}

// Package is one directory of .sigil files, addressed by its import
// path under the current module.
type Package struct {
	ImportPath string  // "github.com/seth/pokedex/types"
	Dir        string  // absolute filesystem path
	Files      []*File // parsed contents in stable (filename) order
}

// Program is the closure of packages needed to compile an entry
// point. Packages is keyed by import path; Order lists the import
// paths in topological order (dependencies before dependents) so
// the lowerer / codegen can emit in a single pass.
type Program struct {
	Module   *sigilmod.Module    // resolved manifest
	Entry    string              // import path of the entry package
	Packages map[string]*Package // import path -> package
	Order    []string            // topological order, dependencies first
}

// Load is the top-level entry point. start may be a file or
// directory; in either case the loader finds sigil.mod walking
// upward, then resolves the package containing start (and every
// package it transitively imports).
func Load(start string) (*Program, error) {
	return LoadWithOverlay(start, nil)
}

// LoadWithOverlay is Load with an in-memory overlay: keys are absolute
// file paths, values replace the on-disk contents during this load.
// Editor tooling (the LSP) uses it to compile unsaved buffers; paths
// absent from the overlay read from disk as usual.
func LoadWithOverlay(start string, overlay map[string]string) (*Program, error) {
	mod, err := sigilmod.Find(start)
	if err != nil {
		return nil, err
	}

	abs, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	entryDir := abs
	if !info.IsDir() {
		entryDir = filepath.Dir(abs)
	}
	entryPath, err := mod.PackagePath(entryDir)
	if err != nil {
		return nil, err
	}

	prog := &Program{
		Module:   mod,
		Entry:    entryPath,
		Packages: map[string]*Package{},
	}

	// Cycle detection: visiting is the in-progress set (packages
	// currently on the DFS stack); a hit means cycle. visited is
	// the done set (fully resolved).
	visiting := map[string]bool{}
	visited := map[string]bool{}

	if err := loadPackage(prog, entryPath, overlay, visiting, visited, nil); err != nil {
		return nil, err
	}
	return prog, nil
}

func loadPackage(prog *Program, importPath string, overlay map[string]string, visiting, visited map[string]bool, stack []string) error {
	if visited[importPath] {
		return nil
	}
	if visiting[importPath] {
		// Cycle: the current stack starts at the first occurrence of
		// importPath and ends with us trying to enter it again.
		cycle := append(slices.Clone(stack), importPath)
		return fmt.Errorf("import cycle:\n  %s", strings.Join(cycle, "\n  → "))
	}
	visiting[importPath] = true
	defer func() {
		delete(visiting, importPath)
		visited[importPath] = true
	}()

	dir, err := prog.Module.PackageDir(importPath)
	if err != nil {
		return err
	}
	pkg, err := readPackage(importPath, dir, overlay)
	if err != nil {
		return err
	}

	// Recurse into every imported package. Imports are per-file; the
	// union across files is the package's external dependency set.
	deps := map[string]bool{}
	for _, f := range pkg.Files {
		for _, p := range f.Imports {
			deps[p] = true
		}
	}
	depList := make([]string, 0, len(deps))
	for p := range deps {
		depList = append(depList, p)
	}
	sort.Strings(depList) // deterministic order for testing + diag messages

	for _, p := range depList {
		nextStack := append(stack, importPath)
		if err := loadPackage(prog, p, overlay, visiting, visited, nextStack); err != nil {
			return err
		}
	}

	prog.Packages[importPath] = pkg
	prog.Order = append(prog.Order, importPath)
	return nil
}

// readPackage walks dir for .sigil files, parses each, and extracts
// imports from the AST. Ignored: files starting with `_` (working
// copies / scratch); anything not ending in `.sigil`. The package's
// existence requires at least one .sigil file in the directory.
func readPackage(importPath, dir string, overlay map[string]string) (*Package, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("package %s: %w", importPath, err)
	}
	pkg := &Package{ImportPath: importPath, Dir: dir}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			continue
		}
		if !strings.HasSuffix(name, ".sigil") {
			continue
		}
		path := filepath.Join(dir, name)
		var source string
		if src, ok := overlay[path]; ok {
			source = src
		} else {
			src, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			source = string(src)
		}
		root, err := parser.Parse(source)
		if err != nil {
			stampDiagFiles(err, path)
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		imports, err := extractImports(root, importPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		pkg.Files = append(pkg.Files, &File{
			Path:    path,
			AST:     root,
			Imports: imports,
		})
	}
	if len(pkg.Files) == 0 {
		return nil, fmt.Errorf("package %s: no .sigil files in %s", importPath, dir)
	}
	// Stable order — filename ascending.
	sort.Slice(pkg.Files, func(i, j int) bool {
		return pkg.Files[i].Path < pkg.Files[j].Path
	})
	return pkg, nil
}

// stampDiagFiles fills the File field of every *diag.Diagnostic in err
// so downstream consumers (sigil check --json, the LSP) know which file
// of a multi-file package each finding came from. Diagnostics that
// already carry a File keep it.
func stampDiagFiles(err error, path string) {
	var multi *diag.MultiError
	if errors.As(err, &multi) {
		for _, d := range multi.Items {
			if d.File == "" {
				d.File = path
			}
		}
		return
	}
	var d *diag.Diagnostic
	if errors.As(err, &d) && d.File == "" {
		d.File = path
	}
}

// extractImports walks a parsed file's top-level children, pulls out
// every `import …` node, and returns alias → import-path. The alias
// defaults to the last path segment when `as` was not given. Duplicate
// aliases within one file are an error.
func extractImports(root *ast.Node, selfPath string) (map[string]string, error) {
	out := map[string]string{}
	children := topLevel(root)
	for _, c := range children {
		if c.Kind != "import" {
			continue
		}
		if len(c.Args) == 0 {
			return nil, errors.New("import node missing path")
		}
		path := c.Args[0].String
		if path == selfPath {
			return nil, fmt.Errorf("package %q cannot import itself", path)
		}
		alias := defaultAlias(path)
		if len(c.Args) >= 2 {
			alias = c.Args[1].String
		}
		if _, dup := out[alias]; dup {
			return nil, fmt.Errorf("duplicate import alias %q in file", alias)
		}
		out[alias] = path
	}
	return out, nil
}

// topLevel returns the top-level decls of a parsed file. parser.Parse
// returns either the single root node (when only one top-level decl
// is present) or a `module` wrapper. Normalize that here so callers
// always see a flat list.
func topLevel(root *ast.Node) []*ast.Node {
	if root == nil {
		return nil
	}
	if root.Kind == "module" || root.Kind == "__root__" {
		return root.Children
	}
	return []*ast.Node{root}
}

// defaultAlias is the bare-import alias: the last path segment.
// `github.com/seth/pokedex/types` → `types`.
func defaultAlias(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
