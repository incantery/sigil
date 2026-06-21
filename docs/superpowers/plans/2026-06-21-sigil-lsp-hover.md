# `sigil lsp` Slice 3a — Analysis Core + Hover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Capture per-node inferred types and build a position→node index, then ship LSP hover that shows `name : type` (generalized scheme for top-level bindings) for the expression under the cursor.

**Architecture:** Instrument the existing HM checker with an optional node→type recorder (one wrapper around `infer`; zero overhead when off). `load` gains a `Record` option that records the entry module into `Program.EntryInfo`. A new `internal/analysis` package computes structural node extents (since AST nodes have only start positions) to locate the node at a cursor, and formats the hover. `internal/lsp` adds a thin `textDocument/hover` handler.

**Tech Stack:** Go stdlib only. Reuses `internal/types` (HM inference, `String`/`SchemeString` which prune-on-print), `internal/load`, `internal/ast`. No new dependencies.

## Global Constraints

- No new Go module dependencies.
- The ONLY intrusion into the type checker is an optional recorder: a nil-checked `c.rec` populated by a single wrapper around `infer`. Existing `Check`/`CheckModule` output must be unchanged (recorder nil). A golden test enforces this.
- No separate zonk pass: `types.String`/`types.SchemeString` already `prune` at every level, and inference is complete before hover prints — so `TypeInfo` stores the raw recorded `Type` and printing resolves it.
- The identifier node is `*ast.Var` (NOT "Ident"); its name is `.Name`. A declaration name (`LetDecl.Name`) is a string field, not an `Expr` — hovering a declaration name itself is out of scope (hover works on expression occurrences).
- AST positions are 1-based (`ast.Pos{Line, Col}`); LSP positions are 0-based. The `internal/lsp` layer converts; `internal/analysis` stays in 1-based AST positions.
- Node→type map keys are `ast.Expr` pointers. `load` must record the SAME `Entry.AST` instance that `analysis` indexes (it does — one parse, checked in place), so pointer identity lines up.
- Hover degrades gracefully: a position outside every node, a node with no recorded type, or a parse/type failure → reply `null`, never an error.
- Reuse existing `internal/lsp` machinery: `uriToPath`, `docStore.overlay()`, `Position`, the pipe test harness (`frame`/`safeBuffer`/`waitFor`/`send`).

## File structure

- `internal/types/infer.go` — recorder field + `infer` wrapper + `checkIntoRec` + `CheckModuleRecording` + `TypeInfo` (Task 1).
- `internal/load/load.go` — `Options.Record` + `Program.EntryInfo` + record-entry path (Task 2).
- `internal/analysis/index.go` — `children`, extents, `NodeIndex`, `At` (Task 3).
- `internal/analysis/hover.go` — `Hover` over `load.Program` + `TypeInfo` (Task 4).
- `internal/lsp/protocol.go` + `server.go` — hover protocol structs, capability, handler (Task 5).
- `editor/lsp.md` + `CLAUDE.md` — docs (Task 6).

---

### Task 1: type recorder + `CheckModuleRecording` + `TypeInfo`

**Files:**
- Modify: `internal/types/infer.go` (Checker field, `infer` rename+wrap, `checkInto`→`checkIntoRec`, new entry point), and add `TypeInfo`.
- Test: `internal/types/record_test.go` (create)

**Interfaces:**
- Consumes: existing `Checker.infer`, `checkInto`, `String`, `SchemeString`, `Scheme`, `Exports`.
- Produces:
  - `type TypeInfo struct { Nodes map[ast.Expr]Type; Schemes map[string]*Scheme }` with methods `func (*TypeInfo) StringOf(ast.Expr) (string, bool)` and `func (*TypeInfo) SchemeOf(name string) (string, bool)`.
  - `func CheckModuleRecording(m *ast.Module, deps *Exports) (*Exports, *TypeInfo, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/types/record_test.go`:

```go
package types

import (
	"testing"

	"github.com/incantery/sigil/internal/parse"
)

func TestCheckModuleRecordingCapturesNodeTypes(t *testing.T) {
	m, err := parse.Module("let main = 1 + 2\n")
	if err != nil {
		t.Fatal(err)
	}
	_, info, err := CheckModuleRecording(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Every expression node got a recorded type: 1, 2, and (1 + 2).
	if len(info.Nodes) < 3 {
		t.Errorf("recorded %d node types, want >= 3 (1, 2, 1+2)", len(info.Nodes))
	}
	// The top-level binding scheme is available.
	if sc, ok := info.SchemeOf("main"); !ok || sc != "Int" {
		t.Errorf("SchemeOf(main) = %q,%v want Int,true", sc, ok)
	}
}

// A node whose type is only fixed by later unification still prints concretely
// (String prunes at print time; recording stores the live type by reference).
func TestRecordingZonksThroughUnification(t *testing.T) {
	m, err := parse.Module("let f x = x + 1\nlet g = f 3\n")
	if err != nil {
		t.Fatal(err)
	}
	_, info, err := CheckModuleRecording(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	// f generalizes to Int -> Int (x is constrained by + 1).
	if sc, ok := info.SchemeOf("f"); !ok || sc != "Int -> Int" {
		t.Errorf("SchemeOf(f) = %q,%v want Int -> Int,true", sc, ok)
	}
}

// Recorder off (Check) is unchanged.
func TestCheckStillWorks(t *testing.T) {
	m, _ := parse.Module("let main = 1 + 2\n")
	got, err := Check(m)
	if err != nil {
		t.Fatal(err)
	}
	if got["main"] != "Int" {
		t.Errorf("Check main = %q want Int", got["main"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/types/ -run 'TestCheckModuleRecording|TestRecording|TestCheckStill' -v`
Expected: FAIL — `CheckModuleRecording` undefined.

- [ ] **Step 3: Add the recorder field + wrap `infer`**

In `internal/types/infer.go`, add a field to the `Checker` struct (after `ctorSchemes`):

```go
	// rec, when non-nil, records the inferred type of every expression node
	// (LSP analysis). nil in normal checking — a single nil-check on the hot path.
	rec map[ast.Expr]Type
```

Rename the existing method `func (c *Checker) infer(e ast.Expr, env *env) (Type, error)` to `inferExpr` (rename ONLY the receiver method declaration line; leave its body and all internal `c.infer(...)` recursive calls untouched). Then add the recording wrapper:

```go
// infer types an expression and, when recording is on, captures node -> type.
// All recursive inference goes through here, so one hook covers every node.
func (c *Checker) infer(e ast.Expr, env *env) (Type, error) {
	t, err := c.inferExpr(e, env)
	if err == nil && c.rec != nil {
		c.rec[e] = t
	}
	return t, err
}
```

- [ ] **Step 4: Add `checkIntoRec`, `CheckModuleRecording`, `TypeInfo`, and extract `moduleExports`**

Change `checkInto` to delegate (keeps Check/CheckModule callers unchanged):

```go
func checkInto(m *ast.Module, deps *Exports) (*Checker, *env, error) {
	return checkIntoRec(m, deps, nil)
}

func checkIntoRec(m *ast.Module, deps *Exports, rec map[ast.Expr]Type) (*Checker, *env, error) {
	c := newChecker()
	c.rec = rec
	root := newEnv(nil)
	c.installBuiltins(root)
	if deps != nil {
		c.seed(root, deps)
	}
	for _, d := range m.Decls {
		if td, ok := d.(*ast.TypeDecl); ok {
			if err := c.registerType(td, root); err != nil {
				return nil, nil, err
			}
		}
	}
	for _, d := range m.Decls {
		if ld, ok := d.(*ast.LetDecl); ok {
			if err := c.inferDecl(ld, root); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := checkEffects(m); err != nil {
		return nil, nil, err
	}
	return c, root, nil
}
```

(Delete the old `checkInto` body that this replaces — there must be exactly one `checkInto` and one `checkIntoRec`.)

Extract the export-building from `CheckModule` into a helper, and have `CheckModule` call it (behavior identical):

```go
// moduleExports builds m's public Exports from a checked module's root scope.
func moduleExports(c *Checker, root *env, m *ast.Module) *Exports {
	ex := newExports()
	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			if d.Pub && d.Name != "" {
				if sc, ok := root.vars[d.Name]; ok {
					ex.Values[d.Name] = sc
				}
			}
		case *ast.TypeDecl:
			if !d.Pub {
				continue
			}
			ex.TypeArity[d.Name] = c.data.typeArity[d.Name]
			if cs, ok := c.data.ctorsOf[d.Name]; ok {
				ex.CtorsOf[d.Name] = cs
			}
			for _, v := range d.Variants {
				ex.CtorType[v.Name] = d.Name
				if sc, ok := c.ctorSchemes[v.Name]; ok {
					ex.CtorSchemes[v.Name] = sc
				}
			}
		}
	}
	return ex
}

func CheckModule(m *ast.Module, deps *Exports) (*Exports, error) {
	c, root, err := checkInto(m, deps)
	if err != nil {
		return nil, err
	}
	return moduleExports(c, root, m), nil
}
```

Add `TypeInfo` and the recording entry point:

```go
// TypeInfo is the per-node type record of one checked module, for LSP analysis.
type TypeInfo struct {
	Nodes   map[ast.Expr]Type  // expression node -> its inferred type
	Schemes map[string]*Scheme // top-level binding name -> generalized scheme
}

// StringOf renders the recorded type of a node (prune happens in String).
func (ti *TypeInfo) StringOf(e ast.Expr) (string, bool) {
	t, ok := ti.Nodes[e]
	if !ok {
		return "", false
	}
	return String(t), true
}

// SchemeOf renders the generalized scheme of a top-level binding name.
func (ti *TypeInfo) SchemeOf(name string) (string, bool) {
	sc, ok := ti.Schemes[name]
	if !ok {
		return "", false
	}
	return SchemeString(sc), true
}

// topSchemes mirrors Check's filtering: user value bindings only (no builtins,
// constructors, or __intrinsics).
func topSchemes(c *Checker, root *env) map[string]*Scheme {
	out := map[string]*Scheme{}
	for name, sc := range root.vars {
		if builtinNames[name] || strings.HasPrefix(name, "__") {
			continue
		}
		if _, isCtor := c.ctorSchemes[name]; isCtor {
			continue
		}
		out[name] = sc
	}
	return out
}

// CheckModuleRecording is CheckModule plus a captured per-node TypeInfo.
func CheckModuleRecording(m *ast.Module, deps *Exports) (*Exports, *TypeInfo, error) {
	rec := map[ast.Expr]Type{}
	c, root, err := checkIntoRec(m, deps, rec)
	if err != nil {
		return nil, nil, err
	}
	return moduleExports(c, root, m), &TypeInfo{Nodes: rec, Schemes: topSchemes(c, root)}, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/types/`
Expected: PASS (new record tests + the full existing type suite — proves Check/CheckModule unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/types/infer.go internal/types/record_test.go
git commit -m "feat(types): optional per-node type recorder + CheckModuleRecording"
```

---

### Task 2: `load.Options.Record` + `Program.EntryInfo`

**Files:**
- Modify: `internal/load/load.go` (`Options`, `Program`, `Load`)
- Test: `internal/load/record_test.go` (create)

**Interfaces:**
- Consumes: `types.CheckModuleRecording` (Task 1), the loader's `mergeDeps`.
- Produces: `Options.Record bool`; `Program.EntryInfo *types.TypeInfo` (nil unless `Record`).

- [ ] **Step 1: Write the failing test**

Create `internal/load/record_test.go`:

```go
package load

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRecordPopulatesEntryInfo(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte("let twice x = x + x\nlet main = twice 21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := Load(entry, Options{Root: dir, Record: true})
	if err != nil {
		t.Fatal(err)
	}
	if prog.EntryInfo == nil {
		t.Fatal("EntryInfo is nil with Record: true")
	}
	if sc, ok := prog.EntryInfo.SchemeOf("twice"); !ok || sc != "Int -> Int" {
		t.Errorf("SchemeOf(twice) = %q,%v want Int -> Int,true", sc, ok)
	}
	if len(prog.EntryInfo.Nodes) == 0 {
		t.Error("EntryInfo.Nodes is empty")
	}
}

func TestLoadWithoutRecordHasNilEntryInfo(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte("let main = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := Load(entry, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	if prog.EntryInfo != nil {
		t.Error("EntryInfo should be nil without Record")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/load/ -run TestLoad -v`
Expected: FAIL — `Record` / `EntryInfo` undefined.

- [ ] **Step 3: Implement**

In `internal/load/load.go`, add to `Options`:

```go
	// Record, when set, captures the entry module's per-node TypeInfo into
	// Program.EntryInfo (LSP analysis). Off by default — no effect on bundling.
	Record bool
```

Add to `Program`:

```go
	// EntryInfo is the entry module's per-node type record, populated only when
	// Options.Record is set. nil otherwise.
	EntryInfo *types.TypeInfo
```

In `Load`, after the existing check loop and before `return &Program{...}`, replace the final return:

```go
	prog := &Program{Modules: l.order, Entry: entry}
	if opts.Record {
		deps, err := l.mergeDeps(entry)
		if err != nil {
			return nil, err
		}
		_, info, err := types.CheckModuleRecording(entry.AST, deps)
		if err != nil {
			return nil, err
		}
		prog.EntryInfo = info
	}
	return prog, nil
```

(`types` is already imported in load.go.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/load/`
Expected: PASS (record tests + existing load suite).

- [ ] **Step 5: Commit**

```bash
git add internal/load/load.go internal/load/record_test.go
git commit -m "feat(load): Options.Record captures entry TypeInfo into Program.EntryInfo"
```

---

### Task 3: `internal/analysis` — position→node index

**Files:**
- Create: `internal/analysis/index.go`
- Test: `internal/analysis/index_test.go` (create)

**Interfaces:**
- Consumes: `internal/ast` node types.
- Produces:
  - `type Range struct { Start, End ast.Pos }` (1-based).
  - `func Index(m *ast.Module) *NodeIndex`.
  - `func (*NodeIndex) At(line, col int) (ast.Expr, Range, bool)` — smallest expression node whose extent contains the position.

- [ ] **Step 1: Write the failing test**

Create `internal/analysis/index_test.go`:

```go
package analysis

import (
	"testing"

	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/parse"
)

func TestAtFindsSmallestNode(t *testing.T) {
	// "let main = count + 1" on line 1 (1-based). Columns (1-based):
	// l=1 e=2 t=3 ' '=4 m=5..n=8 ' '=9 '='=10 ' '=11 c=12 o=13 u=14 n=15 t=16 ' '=17 +=18 ' '=19 1=20
	m, err := parse.Module("let main = count + 1\n")
	if err != nil {
		t.Fatal(err)
	}
	ix := Index(m)

	// Cursor on "count" (col 14) -> the Var node "count".
	node, _, ok := ix.At(1, 14)
	if !ok {
		t.Fatal("expected a node at the identifier")
	}
	v, isVar := node.(*ast.Var)
	if !isVar || v.Name != "count" {
		t.Errorf("At(1,14) = %T, want *ast.Var{Name:count}", node)
	}

	// Cursor on the literal "1" (col 20) -> the IntLit node.
	if _, _, ok := ix.At(1, 20); !ok {
		t.Error("expected a node at the literal")
	}

	// Cursor past end of line (col 40) -> nothing.
	if _, _, ok := ix.At(1, 40); ok {
		t.Error("expected no node past end of line")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/analysis/ -run TestAt -v`
Expected: FAIL — package/`Index` undefined.

- [ ] **Step 3: Implement `internal/analysis/index.go`**

```go
// Package analysis provides position-based queries over a parsed sigil module
// (used by the LSP for hover, and later go-to-definition / semantic tokens).
// AST nodes carry only a start position, so node extents are computed
// structurally: a node spans from its start to the furthest end of its
// descendants; a leaf ends at start + len(its source text).
package analysis

import "github.com/incantery/sigil/internal/ast"

// Range is a 1-based source span.
type Range struct{ Start, End ast.Pos }

// NodeIndex maps source positions to the expression nodes that contain them.
type NodeIndex struct {
	entries []entry
}

type entry struct {
	node  ast.Expr
	start ast.Pos
	end   ast.Pos
}

// Index builds the position→node index for a module's expression trees.
func Index(m *ast.Module) *NodeIndex {
	ix := &NodeIndex{}
	for _, d := range m.Decls {
		if ld, ok := d.(*ast.LetDecl); ok && ld.Body != nil {
			ix.build(ld.Body)
		}
	}
	return ix
}

// build records e and its descendants, returning e's end position.
func (ix *NodeIndex) build(e ast.Expr) ast.Pos {
	start := posOf(e)
	end := leafEnd(e, start)
	for _, ch := range children(e) {
		if ch == nil {
			continue
		}
		ce := ix.build(ch)
		if enc(ce) > enc(end) {
			end = ce
		}
	}
	ix.entries = append(ix.entries, entry{node: e, start: start, end: end})
	return end
}

// At returns the smallest expression node whose extent contains (line, col).
func (ix *NodeIndex) At(line, col int) (ast.Expr, Range, bool) {
	p := ast.Pos{Line: line, Col: col}
	var best *entry
	for i := range ix.entries {
		en := &ix.entries[i]
		if enc(en.start) <= enc(p) && enc(p) <= enc(en.end) {
			if best == nil || span(en) < span(best) {
				best = en
			}
		}
	}
	if best == nil {
		return nil, Range{}, false
	}
	return best.node, Range{Start: best.start, End: best.end}, true
}

const colBits = 1 << 20

func enc(p ast.Pos) int   { return p.Line*colBits + p.Col }
func span(e *entry) int   { return enc(e.end) - enc(e.start) }

// posOf returns a node's start position.
func posOf(e ast.Expr) ast.Pos {
	switch e := e.(type) {
	case *ast.IntLit:
		return e.Pos
	case *ast.FloatLit:
		return e.Pos
	case *ast.StrLit:
		return e.Pos
	case *ast.Interp:
		return e.Pos
	case *ast.Var:
		return e.Pos
	case *ast.Ctor:
		return e.Pos
	case *ast.Unit:
		return e.Pos
	case *ast.Tuple:
		return e.Pos
	case *ast.ListLit:
		return e.Pos
	case *ast.RecordLit:
		return e.Pos
	case *ast.Lambda:
		return e.Pos
	case *ast.App:
		return e.Pos
	case *ast.Field:
		return e.Pos
	case *ast.Binop:
		return e.Pos
	case *ast.Unop:
		return e.Pos
	case *ast.If:
		return e.Pos
	case *ast.Match:
		return e.Pos
	case *ast.Let:
		return e.Pos
	case *ast.Effect:
		return e.Pos
	}
	return ast.Pos{}
}

// leafEnd returns the end position of a leaf node (start advanced by its source
// length); for composite nodes it returns start (the real end comes from
// children).
func leafEnd(e ast.Expr, start ast.Pos) ast.Pos {
	adv := func(n int) ast.Pos { return ast.Pos{Line: start.Line, Col: start.Col + n} }
	switch e := e.(type) {
	case *ast.Var:
		return adv(len(e.Name))
	case *ast.Ctor:
		return adv(len(e.Name))
	case *ast.IntLit:
		return adv(len(e.Raw))
	case *ast.FloatLit:
		return adv(len(e.Raw))
	case *ast.StrLit:
		return adv(len(e.Value) + 2) // approximate: include the quotes
	case *ast.Unit:
		return adv(2) // "()"
	}
	return start
}

// children returns the sub-expressions of a node (skips patterns and names).
func children(e ast.Expr) []ast.Expr {
	switch e := e.(type) {
	case *ast.Interp:
		return e.Parts
	case *ast.Tuple:
		return e.Elems
	case *ast.ListLit:
		return e.Elems
	case *ast.RecordLit:
		out := make([]ast.Expr, 0, len(e.Fields))
		for _, f := range e.Fields {
			out = append(out, f.Value)
		}
		return out
	case *ast.Lambda:
		return []ast.Expr{e.Body}
	case *ast.App:
		return []ast.Expr{e.Fn, e.Arg}
	case *ast.Field:
		return []ast.Expr{e.Recv}
	case *ast.Binop:
		return []ast.Expr{e.L, e.R}
	case *ast.Unop:
		return []ast.Expr{e.X}
	case *ast.If:
		return []ast.Expr{e.Cond, e.Then, e.Else}
	case *ast.Match:
		out := []ast.Expr{e.Scrut}
		for _, a := range e.Arms {
			if a.Guard != nil {
				out = append(out, a.Guard)
			}
			out = append(out, a.Body)
		}
		return out
	case *ast.Let:
		return []ast.Expr{e.Body, e.In}
	case *ast.Effect:
		return e.Stmts
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/analysis/ -run TestAt -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/index.go internal/analysis/index_test.go
git commit -m "feat(analysis): structural position→node index"
```

---

### Task 4: `internal/analysis` — hover

**Files:**
- Create: `internal/analysis/hover.go`
- Test: `internal/analysis/hover_test.go` (create)

**Interfaces:**
- Consumes: `Index`/`At` (Task 3), `load.Program`/`Program.EntryInfo` (Task 2), `types.TypeInfo` (Task 1), `*ast.Var`.
- Produces:
  - `type Result struct { Markdown string; Range Range }`.
  - `func Hover(prog *load.Program, line, col int) (Result, bool)` — hover for the program's entry module (the open file). `ok == false` when no node, no type, or no `EntryInfo`.

- [ ] **Step 1: Write the failing test**

Create `internal/analysis/hover_test.go`:

```go
package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/sigil/internal/load"
)

func loadRec(t *testing.T, src string) *load.Program {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(entry, load.Options{Root: dir, Record: true})
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

func TestHoverLocalIdentifier(t *testing.T) {
	// line 2 col 16 is the use of `n` inside `n + 1`.
	prog := loadRec(t, "let inc n = n + 1\nlet main = inc 41\n")
	// Hover the use of `inc` (line 2, col 12) -> generalized scheme.
	res, ok := Hover(prog, 2, 12)
	if !ok {
		t.Fatal("expected hover on inc")
	}
	if !strings.Contains(res.Markdown, "inc :") || !strings.Contains(res.Markdown, "Int -> Int") {
		t.Errorf("hover markdown = %q, want inc : Int -> Int", res.Markdown)
	}
}

func TestHoverWhitespaceIsNull(t *testing.T) {
	prog := loadRec(t, "let main = 1\n")
	if _, ok := Hover(prog, 5, 1); ok { // a line past the source
		t.Error("expected no hover on empty region")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/analysis/ -run TestHover -v`
Expected: FAIL — `Hover` undefined.

- [ ] **Step 3: Implement `internal/analysis/hover.go`**

````go
package analysis

import (
	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/load"
	"github.com/incantery/sigil/internal/types"
)

// Result is a hover answer: rendered markdown plus the highlighted range.
type Result struct {
	Markdown string
	Range    Range
}

// Hover answers a hover request at (line, col) over prog's entry module. It
// returns ok == false (a null hover) when there is no node, no recorded type,
// or the program was not loaded with Record.
func Hover(prog *load.Program, line, col int) (Result, bool) {
	if prog == nil || prog.EntryInfo == nil || prog.Entry == nil {
		return Result{}, false
	}
	ix := Index(prog.Entry.AST)
	node, rng, ok := ix.At(line, col)
	if !ok {
		return Result{}, false
	}
	text, ok := render(node, prog.EntryInfo)
	if !ok {
		return Result{}, false
	}
	return Result{Markdown: codeBlock(text), Range: rng}, true
}

// render formats the hover line per level B: an identifier shows `name : type`
// (generalized scheme for a top-level binding); any other node shows its type.
func render(node ast.Expr, info *types.TypeInfo) (string, bool) {
	if v, isVar := node.(*ast.Var); isVar {
		if sc, ok := info.SchemeOf(v.Name); ok {
			return v.Name + " : " + sc, true // top-level binding -> generalized scheme
		}
		if ty, ok := info.StringOf(node); ok {
			return v.Name + " : " + ty, true // local / param -> monomorphic type
		}
		return "", false
	}
	if ty, ok := info.StringOf(node); ok {
		return ty, true
	}
	return "", false
}

func codeBlock(s string) string { return "```sigil\n" + s + "\n```" }
````

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/analysis/`
Expected: PASS (hover + index).

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/hover.go internal/analysis/hover_test.go
git commit -m "feat(analysis): hover — name : type / generalized scheme per cursor node"
```

---

### Task 5: `internal/lsp` — hover handler + protocol + integration test

**Files:**
- Modify: `internal/lsp/protocol.go` (hover structs + capability field), `internal/lsp/server.go` (capability value + dispatch case)
- Test: `internal/lsp/hover_test.go` (create)

**Interfaces:**
- Consumes: `analysis.Hover` (Task 4), `load.Load` with `Record` (Task 2), existing `uriToPath`, `docStore.overlay()`, `Position`, `Range`/`Position` LSP structs, pipe harness.
- Produces:
  - `ServerCapabilities` gains `HoverProvider bool` (json `hoverProvider`).
  - `HoverParams`, `Hover` (`{ contents, range }`), `MarkupContent` structs.
  - dispatch case `textDocument/hover`.

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/hover_test.go`:

```go
package lsp

import (
	"io"
	"path/filepath"
	"testing"
)

func TestHoverReturnsType(t *testing.T) {
	root := t.TempDir()
	// `inc` is a top-level binding; hovering its use shows its scheme.
	writeFile(t, filepath.Join(root, "app.sigil"), "let inc n = n + 1\nlet main = inc 41\n")
	uri := "file://" + filepath.Join(root, "app.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"let inc n = n + 1\nlet main = inc 41\n"}}}`)
	// hover the use of `inc` at line 2 (0-based line 1), col 12 (0-based char 11).
	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":1,"character":11}}}`)
	waitFor(t, &out, "Int -> Int")

	// hover an empty region -> null result (no panic, no type).
	send(cw, `{"jsonrpc":"2.0","id":3,"method":"textDocument/hover","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":40,"character":0}}}`)
	waitFor(t, &out, `"id":3`)

	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func TestHoverCapabilityAdvertised(t *testing.T) {
	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()
	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///tmp"}}`)
	waitFor(t, &out, "hoverProvider")
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}
```

(`writeFile`, `send`, `safeBuffer`, `waitFor` exist in the package's other test files.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lsp/ -run TestHover -v`
Expected: FAIL — no hover handler / `hoverProvider` not advertised.

- [ ] **Step 3: Add protocol structs + capability field**

In `internal/lsp/protocol.go`, add `HoverProvider` to `ServerCapabilities`:

```go
type ServerCapabilities struct {
	TextDocumentSync       int  `json:"textDocumentSync"`
	DocumentSymbolProvider bool `json:"documentSymbolProvider"`
	HoverProvider          bool `json:"hoverProvider"`
}
```

Add hover structs:

```go
type HoverParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type MarkupContent struct {
	Kind  string `json:"kind"`  // "markdown"
	Value string `json:"value"`
}

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    Range         `json:"range"`
}
```

- [ ] **Step 4: Advertise the capability + handle the request in `server.go`**

In the `initialize` reply capabilities, set `HoverProvider: true`:

```go
		_ = s.conn.Reply(msg.ID, InitializeResult{Capabilities: ServerCapabilities{
			TextDocumentSync:       TextDocumentSyncFull,
			DocumentSymbolProvider: true,
			HoverProvider:          true,
		}})
```

Add a dispatch case (before `default`):

```go
	case "textDocument/hover":
		s.handleHover(msg)
```

Add the handler method (alongside `publishDiagnostics`). It mirrors the diagnostics path's path/overlay handling and converts 0-based LSP ↔ 1-based AST:

```go
import (
	"path/filepath" // already imported by server.go
	"github.com/incantery/sigil/internal/analysis"
	"github.com/incantery/sigil/internal/load"
)

func (s *Server) handleHover(msg *Message) {
	var p HoverParams
	_ = json.Unmarshal(msg.Params, &p)
	path := uriToPath(p.TextDocument.URI)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	root := s.root
	if root == "" {
		root = filepath.Dir(path)
	}
	prog, err := load.Load(path, load.Options{Root: root, Overlay: s.docs.overlay(), Record: true})
	if err != nil {
		_ = s.conn.Reply(msg.ID, nil) // broken file: null hover (diagnostics report the error)
		return
	}
	// LSP positions are 0-based; AST is 1-based.
	res, ok := analysis.Hover(prog, p.Position.Line+1, p.Position.Character+1)
	if !ok {
		_ = s.conn.Reply(msg.ID, nil)
		return
	}
	_ = s.conn.Reply(msg.ID, Hover{
		Contents: MarkupContent{Kind: "markdown", Value: res.Markdown},
		Range: Range{
			Start: Position{Line: res.Range.Start.Line - 1, Character: res.Range.Start.Col - 1},
			End:   Position{Line: res.Range.End.Line - 1, Character: res.Range.End.Col - 1},
		},
	})
}
```

(Add the `analysis` and `load` imports to `server.go`'s import block. `load` may already be unimported there — add it. `encoding/json`, `path/filepath` are already imported.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/lsp/`
Expected: PASS (hover + all prior lsp tests).

- [ ] **Step 6: Commit**

```bash
git add internal/lsp/protocol.go internal/lsp/server.go internal/lsp/hover_test.go
git commit -m "feat(lsp): textDocument/hover — type under the cursor"
```

---

### Task 6: Docs

**Files:**
- Modify: `editor/lsp.md`, `CLAUDE.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: Update `editor/lsp.md`**

In the "What it provides (v1)" list, add a hover bullet after the diagnostics/symbols bullets:

```markdown
- **Hover** — the inferred type of the expression under the cursor
  (`name : type`; a top-level binding shows its generalized scheme, e.g.
  `map : (a -> b) -> List a -> List b`). Powered by a per-node type record
  captured from the checker.
```

In the "Not yet" section, remove "hover" if listed, and keep go-to-definition / semantic tokens / completion as the remaining type-aware work.

- [ ] **Step 2: Update `CLAUDE.md`**

- In "What's next", under the editor roadmap, mark **#3 type-aware: slice 3a (analysis core + hover) — DONE** — a per-node type recorder in `internal/types` (`CheckModuleRecording`/`TypeInfo`), a `load.Options.Record` → `Program.EntryInfo`, and a new `internal/analysis` package (position→node index via structural extents + hover). Note the remaining slices: **3b go-to-definition** (needs the definition resolver), **3c semantic tokens** (role classification), then **#4 completion**.
- Mention `internal/analysis` in the package list in the `internal/` description.

Make targeted edits matching the surrounding prose; don't restructure.

- [ ] **Step 3: Full-repo validation**

Run: `go build ./... && go test ./...`
Expected: PASS (browser tests run or skip).

- [ ] **Step 4: Commit**

```bash
git add editor/lsp.md CLAUDE.md
git commit -m "docs: sigil lsp hover (type-aware slice 3a)"
```

---

## Self-Review

**Spec coverage:**
- §1 recording sink (`Recorder`/`c.rec`, `CheckModuleRecording`, `TypeInfo`, existing entry points unchanged, zonk-on-print) → Task 1. ✓
- §2 `internal/analysis` position→node (structural extents) + hover → Tasks 3, 4. ✓
- §3 `load.Options.Record` + `Program.EntryInfo` → Task 2. ✓
- §4 lsp wiring (hoverProvider, Hover/MarkupContent/HoverParams, handler, overlay reuse) → Task 5. ✓
- §Edge cases (whitespace/no-type/parse-fail → null) → Task 4 (`Hover` ok==false) + Task 5 (load error → null). ✓
- §Testing (recording captures per-node, zonk resolves, Check unchanged; At smallest node; Hover name:type + scheme + null; lsp integration) → Tasks 1, 3, 4, 5. ✓
- §Docs → Task 6. ✓

**Placeholder scan:** The Task 3 test and Task 4 code contain explicit "stand-in / use the real type" notes (`astVar`→`*ast.Var`, `*typeInfo`→`*types.TypeInfo`) with the concrete replacement given inline — these are disambiguation notes, not unfilled placeholders. No TBD/TODO. Every code step is complete.

**Type consistency:** `CheckModuleRecording(m, deps) (*Exports, *TypeInfo, error)` and `TypeInfo{Nodes, Schemes}` with `StringOf`/`SchemeOf` (Task 1) are consumed unchanged by `load` (Task 2) and `analysis` (Task 4). `load.Options.Record` / `Program.EntryInfo` (Task 2) consumed by Task 5. `analysis.Index`/`At` returning `(ast.Expr, Range, bool)` (Task 3) consumed by `analysis.Hover` (Task 4). `analysis.Hover(prog, line, col) (Result, bool)` consumed by the lsp handler (Task 5). `Result{Markdown, Range}` and `Range{Start, End ast.Pos}` consistent. 1-based↔0-based conversion isolated to Task 5. ✓
```
