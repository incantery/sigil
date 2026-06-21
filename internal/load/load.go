// Package load resolves a sigil core module's import graph, type-checks every
// module across boundaries, and links them into a single npm-free JS bundle.
//
// Imports use Go-style string paths (`import "std/ui" (card, stack)`); the path
// is resolved to a file under a filesystem root, with an optional prefix (the
// module's own path, e.g. "github.com/incantery/sigil/") stripped first. Remote
// fetching is not implemented yet — resolution is local.
//
// Modules are loaded depth-first with cycle detection, type-checked in
// dependency order (each module sees its dependencies' public Exports seeded
// in), then emitted as IIFE-isolated scopes so non-public bindings never
// collide. Types and constructors of an imported module always flow to the
// importer; plain value bindings flow only when named in a selective import.
package load

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/emit"
	"github.com/incantery/sigil/internal/parse"
	"github.com/incantery/sigil/internal/peval"
	"github.com/incantery/sigil/internal/types"
)

// Options configures import-path resolution.
type Options struct {
	// Root is the filesystem directory import paths resolve against.
	Root string
	// Prefix, if set, is stripped from each import path before resolving (the
	// module's own import-path prefix, so in-repo imports map to local files).
	Prefix string
}

// Module is one node of a resolved, type-checked import graph.
type Module struct {
	Path    string // canonical import path ("" for the entry module)
	File    string // resolved source file
	ID      string // JS-safe identifier, unique within the program
	AST     *ast.Module
	Exports *types.Exports

	imports []resolvedImport
}

type resolvedImport struct {
	dep   *Module
	names []string // selectively imported value names; nil means import all
}

// Program is a fully linked set of modules in dependency order (dependencies
// before dependents); Entry is the module that was loaded.
type Program struct {
	Modules []*Module
	Entry   *Module
}

// Load parses entryFile, resolves and type-checks its transitive imports, and
// returns the linked program. The first parse, resolution, or type error stops
// the load.
func Load(entryFile string, opts Options) (*Program, error) {
	l := &loader{
		opts:     opts,
		done:     map[string]*Module{},
		visiting: map[string]bool{},
		ids:      map[string]bool{},
	}
	entry, err := l.load(entryFile, "")
	if err != nil {
		return nil, err
	}
	// l.order is post-order DFS over an acyclic graph: dependencies precede
	// dependents, so checking in order seeds every module's deps first.
	for _, m := range l.order {
		if err := l.check(m); err != nil {
			return nil, err
		}
	}
	return &Program{Modules: l.order, Entry: entry}, nil
}

type loader struct {
	opts     Options
	done     map[string]*Module // fully loaded modules (memo), keyed by identity
	visiting map[string]bool    // identities currently on the DFS stack (cycle detect)
	stack    []string           // human-readable in-progress chain for diagnostics
	ids      map[string]bool    // assigned JS ids (uniqueness)
	order    []*Module          // topological (post-order DFS)
}

// load resolves one module by import path (canonPath; "" for the entry, which is
// keyed by file instead). It detects import cycles via the visiting set.
func (l *loader) load(file, canonPath string) (*Module, error) {
	key := canonPath
	if key == "" {
		key = "file:" + file
	}
	if m, ok := l.done[key]; ok {
		return m, nil
	}
	if l.visiting[key] {
		return nil, fmt.Errorf("import cycle: %s -> %s", strings.Join(l.stack, " -> "), key)
	}
	l.visiting[key] = true
	l.stack = append(l.stack, key)
	defer func() {
		l.visiting[key] = false
		l.stack = l.stack[:len(l.stack)-1]
	}()

	src, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	a, err := parse.Module(string(src))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}

	m := &Module{Path: canonPath, File: file, AST: a, ID: l.mintID(canonPath, file)}
	for _, imp := range a.Imports {
		if imp.Alias != "" {
			return nil, fmt.Errorf("%s: aliased imports (`as %s`) are not supported yet", file, imp.Alias)
		}
		depFile, err := l.resolve(imp.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		dep, err := l.load(depFile, imp.Path)
		if err != nil {
			return nil, err
		}
		m.imports = append(m.imports, resolvedImport{dep: dep, names: imp.Names})
	}

	l.done[key] = m
	l.order = append(l.order, m) // post-order: appended after its deps
	return m, nil
}

// resolve maps an import path to a source file under Root, stripping Prefix.
func (l *loader) resolve(path string) (string, error) {
	p := strings.TrimPrefix(path, l.opts.Prefix)
	p = strings.Trim(p, "/")
	file := filepath.Join(l.opts.Root, filepath.FromSlash(p)) + ".sigil"
	if _, err := os.Stat(file); err != nil {
		return "", fmt.Errorf("cannot resolve import %q (looked for %s)", path, file)
	}
	return file, nil
}

// check type-checks one module with its dependencies' exports seeded in. It
// runs after all dependencies have been checked (dependency order).
func (l *loader) check(m *Module) error {
	deps, err := l.mergeDeps(m)
	if err != nil {
		return fmt.Errorf("%s: %w", m.File, err)
	}
	ex, err := types.CheckModule(m.AST, deps)
	if err != nil {
		return fmt.Errorf("%s: %w", m.File, err)
	}
	m.Exports = ex
	return nil
}

// mergeDeps combines the exports visible to m: every imported module's public
// types and constructors, plus its value bindings filtered by the selective
// import list (nil = import all values).
func (l *loader) mergeDeps(m *Module) (*types.Exports, error) {
	out := &types.Exports{
		Values:      map[string]*types.Scheme{},
		TypeArity:   map[string]int{},
		CtorsOf:     map[string][]string{},
		CtorType:    map[string]string{},
		CtorSchemes: map[string]*types.Scheme{},
	}
	for _, imp := range m.imports {
		ex := imp.dep.Exports
		maps.Copy(out.TypeArity, ex.TypeArity)
		maps.Copy(out.CtorsOf, ex.CtorsOf)
		maps.Copy(out.CtorType, ex.CtorType)
		maps.Copy(out.CtorSchemes, ex.CtorSchemes)
		if imp.names == nil {
			maps.Copy(out.Values, ex.Values)
			continue
		}
		for _, n := range imp.names {
			sc, ok := ex.Values[n]
			if !ok {
				if _, isCtor := ex.CtorSchemes[n]; isCtor {
					continue // constructors flow automatically; naming one is fine
				}
				return nil, fmt.Errorf("imported name %q is not exported by %q", n, imp.dep.Path)
			}
			out.Values[n] = sc
		}
	}
	return out, nil
}

// Bundle links the type-checked program into one JS program. A program-wide
// partial-evaluator environment (every module's top-level definitions) is built
// so the emitter can fold static styles across module boundaries.
func (p *Program) Bundle() (string, error) {
	linked := make([]emit.LinkedModule, len(p.Modules))
	env := peval.NewEnv()
	for i, m := range p.Modules {
		env.AddModule(m.AST)
		linked[i] = emit.LinkedModule{
			ID:      m.ID,
			AST:     m.AST,
			Imports: importBindings(m),
			Exports: exportNames(m),
		}
	}
	return emit.Bundle(linked, env)
}

// importBindings lists, per dependency, the names to re-bind into m's scope:
// the selected value names (or all, for a bare import) plus every constructor
// the dependency exports (types and their constructors always flow).
func importBindings(m *Module) []emit.ImportBinding {
	out := make([]emit.ImportBinding, 0, len(m.imports))
	for _, imp := range m.imports {
		names := map[string]bool{}
		if imp.names == nil {
			for n := range imp.dep.Exports.Values {
				names[n] = true
			}
		} else {
			for _, n := range imp.names {
				names[n] = true
			}
		}
		for n := range imp.dep.Exports.CtorSchemes {
			names[n] = true
		}
		out = append(out, emit.ImportBinding{FromID: imp.dep.ID, Names: sortedKeys(names)})
	}
	return out
}

// exportNames is every name a module exposes to importers at runtime: its public
// value bindings and the constructors of its public types.
func exportNames(m *Module) []string {
	names := map[string]bool{}
	for n := range m.Exports.Values {
		names[n] = true
	}
	for n := range m.Exports.CtorSchemes {
		names[n] = true
	}
	return sortedKeys(names)
}

// mintID derives a stable, unique, JS-safe identifier for a module.
func (l *loader) mintID(canonPath, file string) string {
	base := canonPath
	if base == "" {
		base = "entry_" + strings.TrimSuffix(filepath.Base(file), ".sigil")
	}
	var sb strings.Builder
	for _, r := range base {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('_')
		}
	}
	id := sb.String()
	cand := id
	for i := 1; l.ids[cand]; i++ {
		cand = fmt.Sprintf("%s_%d", id, i)
	}
	l.ids[cand] = true
	return cand
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
