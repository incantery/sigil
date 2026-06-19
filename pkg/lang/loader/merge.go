package loader

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/incantery/mako/pkg/lang/ast"
	"github.com/incantery/mako/pkg/lang/diag"
)

// Merge returns the program flattened into a single ast.Node{Kind:
// "module"} that the existing lowerer can consume.
//
// v1 semantics:
//
//   - Every package's top-level declarations are concatenated into one
//     module in topological order (dependencies first), with the entry
//     package's `view` / `app` / `test` decls last so user intent
//     dominates the merged tree.
//   - Names must be globally unique across the merged program (same
//     short name in two packages = error). The import-set mechanism
//     plans to relax this with explicit namespacing (planned for icon
//     sets, generalizable to other decl kinds), but the v1 baseline
//     is "split for organization, names stay globally unique."
//   - Dotted type references (`types.Slot`) are rewritten to their
//     bare name (`Slot`) after the loader verifies the qualifier
//     resolves to an imported package that actually declares the name.
//   - `import` nodes are stripped from the merged tree — they've done
//     their job by the time we get here.
func (p *Program) Merge() (*ast.Node, error) {
	// Pass 1: collect every declared name and its source-of-truth
	// package. This lets us catch duplicates early with a clear
	// "declared in both X and Y" error, and lets dotted-ref
	// rewriting verify that `alias.Name` actually names a decl in
	// the aliased package.
	//
	// Uniqueness keys on (decl-kind, name): a view and an app may
	// share a name (the conventional pattern is `view Foo` paired
	// with `app Foo`), but two views named `Foo` is a collision.
	// declsByPackage stores names per package so dotted-ref
	// resolution can verify the aliased package actually declares
	// the referenced name.
	type declKey struct{ kind, name string }
	declsByPackage := map[string]map[string]bool{} // pkgPath -> set of names declared
	declOwners := map[declKey]string{}             // (kind, name) -> pkgPath of declarer

	for _, pkgPath := range p.Order {
		pkg := p.Packages[pkgPath]
		declsByPackage[pkgPath] = map[string]bool{}
		seenInPkg := map[declKey]bool{}
		for _, f := range pkg.Files {
			for _, c := range topLevel(f.AST) {
				name, ok := declName(c)
				if !ok {
					continue
				}
				key := declKey{kind: c.Kind, name: name}
				if other, dup := declOwners[key]; dup && other != pkgPath {
					return nil, fmt.Errorf(
						"%s %q declared in both %s and %s — v1 requires globally-unique names within a decl kind across the program",
						c.Kind, name, other, pkgPath)
				}
				if seenInPkg[key] {
					return nil, fmt.Errorf(
						"%s %q declared multiple times in package %s",
						c.Kind, name, pkgPath)
				}
				seenInPkg[key] = true
				declsByPackage[pkgPath][name] = true
				declOwners[key] = pkgPath
			}
		}
	}

	// Pass 2: rewrite dotted refs in each file's AST, resolve icon-set
	// folder paths to absolute filesystem paths (so the lowerer can
	// load them without knowing where the source lives), and
	// accumulate top-level decls into the merged module.
	module := &ast.Node{Kind: "module"}
	for _, pkgPath := range p.Order {
		pkg := p.Packages[pkgPath]
		for _, f := range pkg.Files {
			if err := rewriteDottedRefs(f.AST, f.Imports, declsByPackage); err != nil {
				return nil, fmt.Errorf("%s: %w", f.Path, err)
			}
			absolutizeIconPaths(f.AST, pkg.Dir)
			for _, c := range topLevel(f.AST) {
				if c.Kind == "import" {
					continue
				}
				module.Children = append(module.Children, c)
			}
		}
	}
	return module, nil
}

// absolutizeIconPaths rewrites every `icon-target` node's path arg
// to an absolute filesystem path, resolved relative to the package
// directory the source file lives in. Required so the lowerer can
// call into icons.Load() without re-acquiring "which file did this
// decl come from" context.
func absolutizeIconPaths(root *ast.Node, pkgDir string) {
	if root == nil {
		return
	}
	for _, c := range topLevel(root) {
		if c.Kind != "icons" {
			continue
		}
		for _, target := range c.Children {
			if target.Kind != "icon-target" || len(target.Args) < 2 {
				continue
			}
			rel := target.Args[1].String
			if rel == "" || filepath.IsAbs(rel) {
				continue
			}
			target.Args[1].String = filepath.Join(pkgDir, rel)
		}
	}
}

// declName returns the short name a top-level decl declares. For
// decls that don't carry a name (e.g. anonymous structures), returns
// ok=false.
func declName(n *ast.Node) (string, bool) {
	switch n.Kind {
	case "type", "view", "component", "app", "query", "command", "stream",
		"theme", "icons", "backend", "session":
		if len(n.Args) > 0 && n.Args[0].Kind == ast.ValueIdent {
			return n.Args[0].String, true
		}
	case "fonts":
		// `fonts google = "Family" …` carries a provider, not a name —
		// multiple packages may each declare font sources; the lowerer
		// unions families per provider. Skip the uniqueness check.
		return "", false
	case "test":
		// Tests carry a string name; duplicates between tests are
		// allowed (you can have two "scenario X" tests in different
		// packages). Returning ok=false skips the uniqueness check.
		return "", false
	}
	return "", false
}

// rewriteDottedRefs walks a file's AST and rewrites every dotted
// identifier reference (`alias.Name`) into its bare form (`Name`),
// using the file's import map to resolve the alias. The verification
// is light at v1: confirm that `alias` is a known import and that
// `Name` is declared in the aliased package; the rewrite to bare
// form means the lowerer never sees the dot.
func rewriteDottedRefs(n *ast.Node, imports map[string]string, declsByPackage map[string]map[string]bool) error {
	if n == nil {
		return nil
	}
	// A dotted invocation head (`components.Pill …`) qualifies a
	// component from an imported package. Same verification as value
	// refs, then the Kind drops to its bare name so the lowerer's
	// global component table resolves it.
	if alias, suffix, found := strings.Cut(n.Kind, "."); found {
		if looksLikePackageRef(alias, suffix) {
			targetPkg, ok := imports[alias]
			if !ok {
				return diag.New("loader", n.Pos.Line, n.Pos.Col,
					fmt.Sprintf("%s.%s: %q is not an imported package", alias, suffix, alias))
			}
			decls := declsByPackage[targetPkg]
			if decls == nil || !decls[suffix] {
				return diag.New("loader", n.Pos.Line, n.Pos.Col,
					fmt.Sprintf("%s.%s: %q is not declared in package %s", alias, suffix, suffix, targetPkg))
			}
			n.Kind = suffix
		}
	}
	// Walk Args. Any Value with `.` in its String is a dotted ref.
	for i := range n.Args {
		if err := rewriteDottedValue(&n.Args[i], imports, declsByPackage); err != nil {
			return err
		}
	}
	// Walk Kwargs.
	for k, v := range n.Kwargs {
		if err := rewriteDottedValue(&v, imports, declsByPackage); err != nil {
			return err
		}
		n.Kwargs[k] = v
	}
	// Recurse children + handlers.
	for _, c := range n.Children {
		if err := rewriteDottedRefs(c, imports, declsByPackage); err != nil {
			return err
		}
	}
	for _, h := range n.Handlers {
		if err := rewriteDottedRefs(h, imports, declsByPackage); err != nil {
			return err
		}
	}
	return nil
}

// rewriteDottedValue rewrites one AST Value if it contains a dotted
// package-qualified reference. Recurses into GenericArgs so
// `List<types.Agent>` rewrites the inner `types.Agent` → `Agent`.
func rewriteDottedValue(v *ast.Value, imports map[string]string, declsByPackage map[string]map[string]bool) error {
	if v.Kind == ast.ValueIdent && strings.Contains(v.String, ".") {
		parts := strings.SplitN(v.String, ".", 2)
		alias, suffix := parts[0], parts[1]
		if looksLikePackageRef(alias, suffix) {
			if targetPkg, ok := imports[alias]; ok {
				decls := declsByPackage[targetPkg]
				if decls == nil || !decls[suffix] {
					return diag.New("loader", v.Pos.Line, v.Pos.Col,
						fmt.Sprintf("%s.%s: %q is not declared in package %s", alias, suffix, suffix, targetPkg))
				}
				v.String = suffix
			}
		}
	}
	for i := range v.GenericArgs {
		if err := rewriteDottedValue(&v.GenericArgs[i], imports, declsByPackage); err != nil {
			return err
		}
	}
	return nil
}

// looksLikePackageRef heuristically distinguishes a package-qualified
// reference (`types.Slot`) from a cell-field reference
// (`featured.id`). At v1 the call is: if the suffix starts with an
// uppercase letter, it's a type/decl name; otherwise it's a field.
// (Sigil's other decl kinds — components, views, ops — also use
// CamelCase by convention, so this catches them uniformly.) Both
// halves must be non-empty.
//
// The check is best-effort. False negatives produce a "not declared
// in package" error from the rewriter; false positives let the
// reference pass through unchanged to be resolved as a cell field at
// lower time.
func looksLikePackageRef(alias, suffix string) bool {
	if alias == "" || suffix == "" {
		return false
	}
	c := suffix[0]
	return c >= 'A' && c <= 'Z'
}
