# `sigil lsp` Slice 3b — Go-to-Definition Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `textDocument/definition` — jump from a use of a name to its binder: locals, parameters, pattern binders, same-file top-level definitions, and imported names (cross-file, into the dependency source).

**Architecture:** Add source positions to five binder AST nodes (parser change). A new scope-aware resolver in `internal/analysis` walks lexical scope from the cursor's use node (found via the 3a position→node index) and resolves innermost-first → local binder → same-file top-level → imported dep decl. Go-to-def needs no type info, so it runs off a plain `load.Load`. A thin `internal/lsp` handler returns an LSP `Location`.

**Tech Stack:** Go stdlib only. Reuses `internal/analysis` (3a index), `internal/load`, `internal/ast`, `internal/parse`. No new dependencies.

## Global Constraints

- No new Go module dependencies.
- The compiler-side change is positions on binders only: add `Pos ast.Pos` to `VarParam`, `VarPat`, `RecordParamField`, `PatField`, `Variant`, set by the parser. The type checker is NOT touched. AST dumps omit positions, so parse/dump tests stay green.
- Go-to-def is type-free — `internal/lsp` calls `load.Load(path, Options{Root, Overlay})` with NO `Record`.
- The resolver lives in `internal/analysis` (not the checker); it reimplements lexical scope. It degrades gracefully — any unresolved/ambiguous case returns `ok == false` (a `null` reply), never an error.
- Resolution order for a `*ast.Var`: local scope (innermost-first) → same-file top-level `LetDecl` → imported. For a `*ast.Ctor`: same-file `Variant` → imported.
- Positions are 1-based in `internal/ast`/`internal/analysis`; `internal/lsp` converts to 0-based.
- `pos(tok)` (parser helper, `parse.go:88`) returns `ast.Pos{Line, Col}` (1-based) for a token.
- Reuse existing `internal/lsp` machinery: `uriToPath`, `docStore.overlay()`, `Position`/`Range`, the pipe harness (`frame`/`safeBuffer`/`waitFor`/`send`/`writeFile`).

## File structure

- `internal/ast/ast.go` — `Pos` fields on the five binder nodes (Task 1).
- `internal/parse/parse.go` — set those positions (Task 1).
- `internal/load/load.go` — `Import` type + `Module.Imports()` accessor (Task 2).
- `internal/analysis/definition.go` — `Location`, `Definition`, scope walk, binder helpers, same-file resolution (Task 3); imported branch (Task 4).
- `internal/lsp/protocol.go` + `server.go` — definition protocol, capability, handler (Task 5).
- `editor/lsp.md` + `CLAUDE.md` — docs (Task 6).

---

### Task 1: Parser — binder positions

**Files:**
- Modify: `internal/ast/ast.go` (add `Pos` to 5 binder structs)
- Modify: `internal/parse/parse.go` (set the positions at the 5 construction sites)
- Test: `internal/parse/binderpos_test.go` (create)

**Interfaces:**
- Consumes: `pos(token.Token) ast.Pos` (parse.go:88).
- Produces: `ast.VarParam.Pos`, `ast.VarPat.Pos`, `ast.RecordParamField.Pos`, `ast.PatField.Pos`, `ast.Variant.Pos` — all 1-based `ast.Pos` at the binder's defining identifier.

- [ ] **Step 1: Write the failing test**

Create `internal/parse/binderpos_test.go`:

```go
package parse

import (
	"testing"

	"github.com/incantery/sigil/internal/ast"
)

func TestBinderPositionsRecorded(t *testing.T) {
	// "let inc n = n" — the parameter `n` is at line 1, col 9 (1-based):
	// l1 e2 t3 ' '4 i5 n6 c7 ' '8 n9
	m, err := Module("let inc n = n\n")
	if err != nil {
		t.Fatal(err)
	}
	ld := m.Decls[0].(*ast.LetDecl)
	vp, ok := ld.Params[0].(ast.VarParam)
	if !ok {
		t.Fatalf("param 0 = %T, want ast.VarParam", ld.Params[0])
	}
	if vp.Pos.Line != 1 || vp.Pos.Col != 9 {
		t.Errorf("VarParam pos = %d:%d, want 1:9", vp.Pos.Line, vp.Pos.Col)
	}
}

func TestVariantPositionRecorded(t *testing.T) {
	// "type T = Red | Green" — Red at col 10, Green at col 16.
	m, err := Module("type T = Red | Green\n")
	if err != nil {
		t.Fatal(err)
	}
	td := m.Decls[0].(*ast.TypeDecl)
	if td.Variants[0].Pos.Col != 10 {
		t.Errorf("Red pos col = %d, want 10", td.Variants[0].Pos.Col)
	}
	if td.Variants[1].Pos.Col != 16 {
		t.Errorf("Green pos col = %d, want 16", td.Variants[1].Pos.Col)
	}
}

func TestVarPatPositionRecorded(t *testing.T) {
	// Inner block-let destructures a tuple: "  let (a, b) = p" (line 2, indented 2).
	// Columns: 1,2 indent · l3 e4 t5 ' '6 (7 a8 ,9 ' '10 b11 — so `a` is at 2:8.
	m, err := Module("let main =\n  let (a, b) = p\n  a\n")
	if err != nil {
		t.Fatal(err)
	}
	// Drill to the inner Let's destructuring pattern.
	outer := m.Decls[0].(*ast.LetDecl)
	inner := outer.Body.(*ast.Let)
	tup := inner.Pat.(ast.TuplePat)
	a := tup.Elems[0].(ast.VarPat)
	if a.Pos.Line != 2 || a.Pos.Col != 8 {
		t.Errorf("VarPat a pos = %d:%d, want 2:8", a.Pos.Line, a.Pos.Col)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/parse/ -run 'TestBinderPositions|TestVariantPosition|TestVarPatPosition' -v`
Expected: FAIL — `VarParam` has no field `Pos`.

- [ ] **Step 3: Add `Pos` fields in `internal/ast/ast.go`**

Change the five struct definitions to add `Pos Pos`:

```go
	// VarParam binds a name.
	VarParam struct {
		Pos  Pos
		Name string
	}
```

```go
	VarPat struct {
		Pos  Pos
		Name string
	}
```

For `Variant` (currently `type Variant struct { Name string; Arg TypeExpr }`):

```go
type Variant struct {
	Pos  Pos
	Name string
	Arg  TypeExpr // nil for nullary constructors
}
```

For `RecordParamField` (find its struct def near `RecordParam`) add `Pos Pos` as the first field; likewise for `PatField` (currently `type PatField struct { Name string; Pat Pattern }`):

```go
type PatField struct {
	Pos  Pos
	Name string
	Pat  Pattern // Pat==nil means pun (bind Name)
}
```

(Add `Pos Pos` as the first field of `RecordParamField` the same way.)

- [ ] **Step 4: Set the positions in `internal/parse/parse.go`**

At each of the five construction sites, set `Pos` from the binder's identifier token:

- `VarParam` (in `parseParams`, the `token.IDENT` case, currently `params = append(params, ast.VarParam{Name: p.advance().Lit})`):

```go
			vt := p.advance()
			params = append(params, ast.VarParam{Pos: pos(vt), Name: vt.Lit})
```

- `VarPat` (in `parsePatternAtom`, `token.IDENT` case where `t := p.cur()` is already captured, currently `return ast.VarPat{Name: t.Lit}, nil`):

```go
		return ast.VarPat{Pos: pos(t), Name: t.Lit}, nil
```

- `RecordParamField` (in `parseRecordParam`, currently `f := &ast.RecordParamField{Name: name.Lit}`):

```go
		f := &ast.RecordParamField{Pos: pos(name), Name: name.Lit}
```

- `PatField` (in the record-pattern parser, currently `f := &ast.PatField{Name: name.Lit}`):

```go
		f := &ast.PatField{Pos: pos(name), Name: name.Lit}
```

- `Variant` (in the type-decl parser, currently `v := &ast.Variant{Name: ctorTok.Lit}`):

```go
		v := &ast.Variant{Pos: pos(ctorTok), Name: ctorTok.Lit}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/parse/ ./internal/ast/ ./internal/types/ ./internal/emit/`
Expected: PASS (new binder-position tests + existing parse/dump/type/emit suites — positions are additive, dumps omit them).

- [ ] **Step 6: Commit**

```bash
git add internal/ast/ast.go internal/parse/parse.go internal/parse/binderpos_test.go
git commit -m "feat(parse): record source positions on binder nodes"
```

---

### Task 2: `load` — `Module.Imports()` accessor

**Files:**
- Modify: `internal/load/load.go` (add `Import` type + accessor)
- Test: `internal/load/imports_test.go` (create)

**Interfaces:**
- Consumes: the unexported `resolvedImport{dep *Module, names []string}`.
- Produces: `type Import struct { Dep *Module; Names []string }` and `func (m *Module) Imports() []Import` (Names nil ⇒ bare import).

- [ ] **Step 1: Write the failing test**

Create `internal/load/imports_test.go`:

```go
package load

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModuleImportsExposesDeps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.sigil"), []byte("pub let answer = 42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte("import \"lib\" (answer)\nlet main = answer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := Load(entry, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	imps := prog.Entry.Imports()
	if len(imps) != 1 {
		t.Fatalf("got %d imports, want 1", len(imps))
	}
	if imps[0].Dep == nil || len(imps[0].Names) != 1 || imps[0].Names[0] != "answer" {
		t.Errorf("import = %+v, want Dep set + Names [answer]", imps[0])
	}
	if imps[0].Dep.File == "" {
		t.Error("dep File should be set for cross-file resolution")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/load/ -run TestModuleImports -v`
Expected: FAIL — `Imports` undefined.

- [ ] **Step 3: Implement in `internal/load/load.go`**

Add near the `Module` type:

```go
// Import is a resolved dependency of a module: the dependency module and the
// value names selectively imported (Names == nil for a bare import). Exposed so
// LSP analysis can resolve a name to its defining module/file.
type Import struct {
	Dep   *Module
	Names []string
}

// Imports returns m's resolved imports.
func (m *Module) Imports() []Import {
	out := make([]Import, 0, len(m.imports))
	for _, ri := range m.imports {
		out = append(out, Import{Dep: ri.dep, Names: ri.names})
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/load/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/load/load.go internal/load/imports_test.go
git commit -m "feat(load): expose resolved imports via Module.Imports()"
```

---

### Task 3: `internal/analysis` — resolver + scope walk (in-file)

**Files:**
- Create: `internal/analysis/definition.go`
- Test: `internal/analysis/definition_test.go` (create)

**Interfaces:**
- Consumes: `Index`/`At` (3a), `children` (index.go, same package), `load.Program`, `ast` binder nodes with `Pos` (Task 1).
- Produces:
  - `type Location struct { File string; Range Range }`.
  - `func Definition(prog *load.Program, line, col int) (Location, bool)` — in-file resolution (local + same-file top-level + same-file constructor). Imported names return `ok == false` here (Task 4 adds them).

- [ ] **Step 1: Write the failing test**

Create `internal/analysis/definition_test.go`:

```go
package analysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/incantery/sigil/internal/load"
)

func loadProg(t *testing.T, src string) *load.Program {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(entry, load.Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

func TestDefinitionParam(t *testing.T) {
	// "let inc n = n + 1" — param binder `n` at 1:9; its use at 1:13.
	prog := loadProg(t, "let inc n = n + 1\n")
	loc, ok := Definition(prog, 1, 13)
	if !ok {
		t.Fatal("expected to resolve the parameter use")
	}
	if loc.Range.Start.Line != 1 || loc.Range.Start.Col != 9 {
		t.Errorf("param def = %d:%d, want 1:9", loc.Range.Start.Line, loc.Range.Start.Col)
	}
	if loc.File != prog.Entry.File {
		t.Errorf("File = %q, want entry file", loc.File)
	}
}

func TestDefinitionTopLevel(t *testing.T) {
	// `inc` use on line 2 resolves to its top-level decl on line 1 (col 1).
	prog := loadProg(t, "let inc n = n + 1\nlet main = inc 41\n")
	loc, ok := Definition(prog, 2, 12) // the `inc` in `inc 41`
	if !ok {
		t.Fatal("expected to resolve the top-level use")
	}
	if loc.Range.Start.Line != 1 || loc.Range.Start.Col != 1 {
		t.Errorf("top-level def = %d:%d, want 1:1", loc.Range.Start.Line, loc.Range.Start.Col)
	}
}

func TestDefinitionShadowing(t *testing.T) {
	// A parameter `x` shadows a top-level `x`; the use inside resolves to the param.
	// "let x = 1\nlet f x = x\n" — inside f, `x` (use at 2:11) is the param at 2:7.
	prog := loadProg(t, "let x = 1\nlet f x = x\n")
	loc, ok := Definition(prog, 2, 11)
	if !ok {
		t.Fatal("expected resolution")
	}
	if loc.Range.Start.Line != 2 || loc.Range.Start.Col != 7 {
		t.Errorf("shadowed def = %d:%d, want 2:7 (the param, not top-level x)", loc.Range.Start.Line, loc.Range.Start.Col)
	}
}

func TestDefinitionConstructor(t *testing.T) {
	// "type C = Red | Green\nlet main = Red" — `Red` use at 2:12 -> variant at 1:10.
	prog := loadProg(t, "type C = Red | Green\nlet main = Red\n")
	loc, ok := Definition(prog, 2, 12)
	if !ok {
		t.Fatal("expected to resolve the constructor use")
	}
	if loc.Range.Start.Line != 1 || loc.Range.Start.Col != 10 {
		t.Errorf("ctor def = %d:%d, want 1:10", loc.Range.Start.Line, loc.Range.Start.Col)
	}
}

func TestDefinitionWhitespaceNone(t *testing.T) {
	prog := loadProg(t, "let main = 1\n")
	if _, ok := Definition(prog, 5, 1); ok {
		t.Error("expected no definition off-source")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/analysis/ -run TestDefinition -v`
Expected: FAIL — `Definition` undefined.

- [ ] **Step 3: Implement `internal/analysis/definition.go`**

```go
package analysis

import (
	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/load"
)

// Location is a definition site: an absolute source File plus a 1-based Range
// spanning the bound name.
type Location struct {
	File  string
	Range Range
}

// Definition resolves the use of a name under (line, col) in prog's entry module
// to its binder. Resolution order for a variable: local scope (innermost-first),
// then same-file top-level, then imported (see resolveImported). For a
// constructor: same-file variant, then imported. ok == false when the cursor is
// not on a Var/Ctor use or the name cannot be resolved.
func Definition(prog *load.Program, line, col int) (Location, bool) {
	if prog == nil || prog.Entry == nil {
		return Location{}, false
	}
	m := prog.Entry.AST
	node, _, ok := Index(m).At(line, col)
	if !ok {
		return Location{}, false
	}
	switch n := node.(type) {
	case *ast.Var:
		if p, ok := resolveLocal(m, n, n.Name); ok {
			return loc(prog.Entry.File, p, n.Name), true
		}
		if p, ok := topLevelLet(m, n.Name); ok {
			return loc(prog.Entry.File, p, n.Name), true
		}
		if l, ok := resolveImported(prog, n.Name, false); ok {
			return l, true
		}
	case *ast.Ctor:
		if p, ok := variantInModule(m, n.Name); ok {
			return loc(prog.Entry.File, p, n.Name), true
		}
		if l, ok := resolveImported(prog, n.Name, true); ok {
			return l, true
		}
	}
	return Location{}, false
}

type binder struct {
	name string
	pos  ast.Pos
}

func loc(file string, p ast.Pos, name string) Location {
	return Location{File: file, Range: Range{
		Start: p,
		End:   ast.Pos{Line: p.Line, Col: p.Col + len(name)},
	}}
}

// resolveLocal finds the innermost binder of name that is in scope at the target
// node, walking lexical scope from each top-level decl body.
func resolveLocal(m *ast.Module, target ast.Expr, name string) (ast.Pos, bool) {
	var result ast.Pos
	var found bool
	var search func(e ast.Expr, scope []binder) bool // true once target is located
	search = func(e ast.Expr, scope []binder) bool {
		if e == nil {
			return false
		}
		if e == target {
			for i := len(scope) - 1; i >= 0; i-- {
				if scope[i].name == name {
					result, found = scope[i].pos, true
					return true
				}
			}
			return true // target located; name not bound locally
		}
		switch e := e.(type) {
		case *ast.Lambda:
			return search(e.Body, extend(scope, paramBinders(e.Params)...))
		case *ast.Let:
			body := extend(scope, paramBinders(e.Params)...)
			if e.Rec {
				body = extend(body, letSelf(e)...)
			}
			if search(e.Body, body) {
				return true
			}
			return search(e.In, extend(scope, letSelf(e)...))
		case *ast.Match:
			if search(e.Scrut, scope) {
				return true
			}
			for _, arm := range e.Arms {
				armScope := extend(scope, patBinders(arm.Pat)...)
				if arm.Guard != nil && search(arm.Guard, armScope) {
					return true
				}
				if search(arm.Body, armScope) {
					return true
				}
			}
			return false
		default:
			for _, ch := range children(e) {
				if search(ch, scope) {
					return true
				}
			}
			return false
		}
	}
	for _, d := range m.Decls {
		ld, ok := d.(*ast.LetDecl)
		if !ok || ld.Body == nil {
			continue
		}
		scope := paramBinders(ld.Params)
		if ld.Rec && ld.Name != "" {
			scope = extend(scope, binder{ld.Name, ld.Pos})
		}
		if search(ld.Body, scope) {
			return result, found
		}
	}
	return ast.Pos{}, false
}

// extend returns a fresh scope = base ++ more (never aliases base's backing array).
func extend(base []binder, more ...binder) []binder {
	out := make([]binder, 0, len(base)+len(more))
	out = append(out, base...)
	out = append(out, more...)
	return out
}

// letSelf is the binder(s) a Let introduces for its `in` body.
func letSelf(e *ast.Let) []binder {
	if e.Name != "" {
		return []binder{{e.Name, e.Pos}}
	}
	return patBinders(e.Pat)
}

func paramBinders(params []ast.Param) []binder {
	var bs []binder
	for _, p := range params {
		switch p := p.(type) {
		case ast.VarParam:
			bs = append(bs, binder{p.Name, p.Pos})
		case ast.PatParam:
			bs = append(bs, patBinders(p.Pat)...)
		case ast.RecordParam:
			for _, f := range p.Fields {
				bs = append(bs, binder{f.Name, f.Pos})
			}
		}
	}
	return bs
}

func patBinders(pat ast.Pattern) []binder {
	switch p := pat.(type) {
	case ast.VarPat:
		return []binder{{p.Name, p.Pos}}
	case ast.CtorPat:
		var bs []binder
		for _, a := range p.Args {
			bs = append(bs, patBinders(a)...)
		}
		return bs
	case ast.TuplePat:
		var bs []binder
		for _, e := range p.Elems {
			bs = append(bs, patBinders(e)...)
		}
		return bs
	case ast.ListPat:
		var bs []binder
		for _, e := range p.Elems {
			bs = append(bs, patBinders(e)...)
		}
		return bs
	case ast.RecordPat:
		var bs []binder
		for _, f := range p.Fields {
			if f.Pat == nil {
				bs = append(bs, binder{f.Name, f.Pos}) // pun: binds the field name
			} else {
				bs = append(bs, patBinders(f.Pat)...)
			}
		}
		return bs
	}
	return nil
}

// topLevelLet finds a top-level value binding named name.
func topLevelLet(m *ast.Module, name string) (ast.Pos, bool) {
	for _, d := range m.Decls {
		if ld, ok := d.(*ast.LetDecl); ok && ld.Name == name {
			return ld.Pos, true
		}
	}
	return ast.Pos{}, false
}

// variantInModule finds a constructor named name among the module's type decls.
func variantInModule(m *ast.Module, name string) (ast.Pos, bool) {
	for _, d := range m.Decls {
		if td, ok := d.(*ast.TypeDecl); ok {
			for _, v := range td.Variants {
				if v.Name == name {
					return v.Pos, true
				}
			}
		}
	}
	return ast.Pos{}, false
}

// resolveImported is implemented in Task 4; in-file resolution returns false here.
func resolveImported(prog *load.Program, name string, isCtor bool) (Location, bool) {
	return Location{}, false
}
```

Note: `Variant` is `*ast.Variant` in `TypeDecl.Variants` (a slice of pointers), so `v.Pos`/`v.Name` dereference cleanly in the range loops.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/analysis/ -run TestDefinition -v`
Expected: PASS (param, top-level, shadowing, constructor, whitespace-none).

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/definition.go internal/analysis/definition_test.go
git commit -m "feat(analysis): scope-aware go-to-definition (in-file)"
```

---

### Task 4: `internal/analysis` — cross-file (imported) resolution

**Files:**
- Modify: `internal/analysis/definition.go` (implement `resolveImported`)
- Test: `internal/analysis/definition_test.go` (add a cross-file test)

**Interfaces:**
- Consumes: `load.Program.Entry.Imports()` (Task 2), `Import{Dep, Names}`, `Dep.AST`/`Dep.File`, the `loc` helper (Task 3).
- Produces: `resolveImported(prog, name, isCtor)` resolving to the dependency's public decl/variant position.

- [ ] **Step 1: Write the failing test**

Add to `internal/analysis/definition_test.go`:

```go
func TestDefinitionImported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.sigil"), []byte("pub let answer = 42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte("import \"lib\" (answer)\nlet main = answer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(entry, load.Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	// `answer` use on line 2 (col 12) resolves into lib.sigil, on line 1.
	// (The target is the `pub let answer` declaration, whose Pos is at the decl
	// keyword — assert the File and line, not an exact column, since the column
	// depends on the pub/let prefix.)
	loc, ok := Definition(prog, 2, 12)
	if !ok {
		t.Fatal("expected to resolve the imported name")
	}
	if loc.File != filepath.Join(dir, "lib.sigil") {
		t.Errorf("File = %q, want lib.sigil", loc.File)
	}
	if loc.Range.Start.Line != 1 {
		t.Errorf("imported def line = %d, want 1", loc.Range.Start.Line)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/analysis/ -run TestDefinitionImported -v`
Expected: FAIL — `resolveImported` is a stub returning false.

- [ ] **Step 3: Implement `resolveImported`**

Replace the stub `resolveImported` in `definition.go` with:

```go
// resolveImported resolves name against the entry module's imports, returning the
// dependency's defining position. A selective value import must name it;
// constructors always flow. Only public decls/variants are valid targets.
func resolveImported(prog *load.Program, name string, isCtor bool) (Location, bool) {
	for _, imp := range prog.Entry.Imports() {
		if !isCtor && imp.Names != nil && !contains(imp.Names, name) {
			continue // selective value import that doesn't bring in this name
		}
		if p, ok := declInModule(imp.Dep.AST, name, isCtor); ok {
			return loc(imp.Dep.File, p, name), true
		}
	}
	return Location{}, false
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// declInModule finds a public value binding (isCtor == false) or constructor
// (isCtor == true) named name in a dependency module.
func declInModule(m *ast.Module, name string, isCtor bool) (ast.Pos, bool) {
	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			if !isCtor && d.Pub && d.Name == name {
				return d.Pos, true
			}
		case *ast.TypeDecl:
			if isCtor && d.Pub {
				for _, v := range d.Variants {
					if v.Name == name {
						return v.Pos, true
					}
				}
			}
		}
	}
	return ast.Pos{}, false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/analysis/`
Expected: PASS (all definition tests incl. the imported case).

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/definition.go internal/analysis/definition_test.go
git commit -m "feat(analysis): cross-file go-to-definition for imported names"
```

---

### Task 5: `internal/lsp` — definition handler + protocol + integration test

**Files:**
- Modify: `internal/lsp/protocol.go` (capability field + `DefinitionParams`/`Location`), `internal/lsp/server.go` (capability value + dispatch case + handler)
- Test: `internal/lsp/definition_test.go` (create)

**Interfaces:**
- Consumes: `analysis.Definition` (Tasks 3–4), `load.Load` (no Record), existing `uriToPath`/`docStore.overlay()`/`Position`/`Range`, the pipe harness.
- Produces:
  - `ServerCapabilities.DefinitionProvider bool` (`definitionProvider`).
  - `DefinitionParams{TextDocument, Position}`, `Location{URI string, Range Range}` structs.
  - `textDocument/definition` dispatch case + `handleDefinition`.

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/definition_test.go`:

```go
package lsp

import (
	"io"
	"path/filepath"
	"testing"
)

func TestDefinitionParamSameFile(t *testing.T) {
	root := t.TempDir()
	src := "let inc n = n + 1\nlet main = inc 41\n"
	writeFile(t, filepath.Join(root, "app.sigil"), src)
	uri := "file://" + filepath.Join(root, "app.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"let inc n = n + 1\nlet main = inc 41\n"}}}`)
	// go-to-def on the use of `n` at line 1 (0-based 0), col 13 (0-based 12).
	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":0,"character":12}}}`)
	// Expect a Location pointing back into the same file at the param binder
	// (line 0, character 8 — 0-based of 1:9).
	waitFor(t, &out, `"character":8`)

	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func TestDefinitionImportedCrossFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib.sigil"), "pub let answer = 42\n")
	appPath := filepath.Join(root, "app.sigil")
	writeFile(t, appPath, "import \"lib\" (answer)\nlet main = answer\n")
	uri := "file://" + appPath
	libURI := "file://" + filepath.Join(root, "lib.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"import \"lib\" (answer)\nlet main = answer\n"}}}`)
	// def on `answer` use at line 2 (0-based 1), col 12 (0-based 11).
	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":1,"character":11}}}`)
	waitFor(t, &out, libURI) // the reply Location points into lib.sigil

	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func TestDefinitionCapabilityAdvertised(t *testing.T) {
	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()
	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///tmp"}}`)
	waitFor(t, &out, "definitionProvider")
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lsp/ -run TestDefinition -v`
Expected: FAIL — no definition handler / capability.

- [ ] **Step 3: Add protocol structs + capability field**

In `internal/lsp/protocol.go`, add `DefinitionProvider` to `ServerCapabilities`:

```go
type ServerCapabilities struct {
	TextDocumentSync       int  `json:"textDocumentSync"`
	DocumentSymbolProvider bool `json:"documentSymbolProvider"`
	HoverProvider          bool `json:"hoverProvider"`
	DefinitionProvider     bool `json:"definitionProvider"`
}
```

Add:

```go
type DefinitionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// Location is an LSP location (a 0-based range in a document).
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}
```

- [ ] **Step 4: Advertise the capability + handle the request in `server.go`**

In the initialize reply capabilities, add `DefinitionProvider: true`:

```go
		_ = s.conn.Reply(msg.ID, InitializeResult{Capabilities: ServerCapabilities{
			TextDocumentSync:       TextDocumentSyncFull,
			DocumentSymbolProvider: true,
			HoverProvider:          true,
			DefinitionProvider:     true,
		}})
```

Add a dispatch case (before `default`):

```go
	case "textDocument/definition":
		s.handleDefinition(msg)
```

Add the handler (alongside `handleHover`); it mirrors the hover handler but calls `analysis.Definition` and loads WITHOUT `Record`:

```go
func (s *Server) handleDefinition(msg *Message) {
	var p DefinitionParams
	_ = json.Unmarshal(msg.Params, &p)
	path := uriToPath(p.TextDocument.URI)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	root := s.root
	if root == "" {
		root = filepath.Dir(path)
	}
	prog, err := load.Load(path, load.Options{Root: root, Overlay: s.docs.overlay()})
	if err != nil {
		_ = s.conn.Reply(msg.ID, nil) // broken file: null
		return
	}
	loc, ok := analysis.Definition(prog, p.Position.Line+1, p.Position.Character+1)
	if !ok {
		_ = s.conn.Reply(msg.ID, nil)
		return
	}
	_ = s.conn.Reply(msg.ID, Location{
		URI: "file://" + loc.File,
		Range: Range{
			Start: Position{Line: loc.Range.Start.Line - 1, Character: loc.Range.Start.Col - 1},
			End:   Position{Line: loc.Range.End.Line - 1, Character: loc.Range.End.Col - 1},
		},
	})
}
```

(`analysis` and `load` are imported by `server.go` from the hover task; if `analysis` is not yet imported, add `"github.com/incantery/sigil/internal/analysis"`.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/lsp/`
Expected: PASS (definition same-file, cross-file, capability + all prior lsp tests). The cross-file test proves end-to-end resolution into a dependency file.

- [ ] **Step 6: Commit**

```bash
git add internal/lsp/protocol.go internal/lsp/server.go internal/lsp/definition_test.go
git commit -m "feat(lsp): textDocument/definition — jump to a name's binder"
```

---

### Task 6: Docs

**Files:**
- Modify: `editor/lsp.md`, `CLAUDE.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: Update `editor/lsp.md`**

Add a go-to-definition bullet to the "What it provides (v1)" list:

```markdown
- **Go to definition** — jump from a use of a name to its binder: a parameter,
  a local `let`, a pattern binder, a same-file top-level definition, or an
  imported name (jumps into the dependency's source file, e.g. `std/ui`).
```

In the "Not yet" section, remove "go-to-definition" if listed; keep semantic tokens / completion / find-references as remaining.

- [ ] **Step 2: Update `CLAUDE.md`**

- In "What's next", mark **#3 type-aware: slice 3b (go-to-definition) — DONE** — binder positions added to the AST (`VarParam`/`VarPat`/`RecordParamField`/`PatField`/`Variant`), a scope-aware resolver in `internal/analysis` (local → same-file top-level → imported/cross-file), `load.Module.Imports()`, and a `textDocument/definition` handler. Note that **binder positions now exist**, so hierarchical document symbols (the #2 follow-up) are unblocked. Remaining: **3c semantic tokens**, then **#4 completion**.

Make targeted edits matching the surrounding prose; don't restructure.

- [ ] **Step 3: Full-repo validation**

Run: `go build ./... && go test ./...`
Expected: PASS (browser tests run or skip).

- [ ] **Step 4: Commit**

```bash
git add editor/lsp.md CLAUDE.md
git commit -m "docs: sigil lsp go-to-definition (type-aware slice 3b)"
```

---

## Self-Review

**Spec coverage:**
- §1 parser binder positions (5 nodes) → Task 1. ✓
- §2 analysis resolver: Location, Definition order, scope walk, paramBinders/patBinders → Task 3; imported branch → Task 4. ✓
- §3 load `Import` + `Module.Imports()` → Task 2. ✓
- §4 lsp definition handler (capability, structs, no Record, 0↔1 conversion, file:// URI) → Task 5. ✓
- §Edge cases (not-on-Var/Ctor → null; shadowing → local; unresolved → null; load error → null) → Task 3 (shadowing/whitespace tests) + Task 5 (load error → null). ✓
- §Testing (parser positions; analysis param/local/top-level/ctor/imported/shadowing/none; lsp same-file + cross-file integration) → Tasks 1, 3, 4, 5. ✓
- §Docs → Task 6. ✓

**Placeholder scan:** No TBD/TODO. Task 3 ships a deliberate `resolveImported` stub (returns false) that Task 4 replaces — this is sequenced, not a placeholder (Task 3's in-file tests pass with it; the stub is explicitly called out). Every code step is complete.

**Type consistency:** `ast.*.Pos` (Task 1) consumed by `paramBinders`/`patBinders`/`variantInModule`/`declInModule` (Tasks 3-4). `load.Import{Dep, Names}` + `Module.Imports()` (Task 2) consumed by `resolveImported` (Task 4). `analysis.Location{File, Range}` + `Definition(prog, line, col) (Location, bool)` (Tasks 3-4) consumed by `handleDefinition` (Task 5). `loc`/`binder`/`extend`/`letSelf`/`topLevelLet`/`variantInModule`/`resolveImported`/`contains`/`declInModule` are all defined in Task 3/4 and used consistently. `children` is reused from `index.go` (same package, 3a). 1-based↔0-based conversion isolated to Task 5. LSP `Location{URI, Range}` (Task 5) distinct from `analysis.Location{File, Range}` (Task 3) — intentional, converted in the handler. ✓
```
