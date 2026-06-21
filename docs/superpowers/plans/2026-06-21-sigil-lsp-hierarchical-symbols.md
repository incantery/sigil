# Hierarchical LSP Document Symbols Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade the LSP document-symbol outline so a `type` shows its constructors (ADT) or fields (record) as nested child symbols.

**Architecture:** Add `Pos` to the record field node `ast.FieldType` (the last binder-position gap), add a `Children` field to the `DocumentSymbol` protocol struct, and enrich `documentSymbols` so each `TypeDecl` becomes a parent symbol whose range contains its member children. Everything else (the `textDocument/documentSymbol` handler) is unchanged.

**Tech Stack:** Go stdlib only. Reuses `internal/lsp/symbols.go`, `internal/ast`, `internal/parse`, and `Variant.Pos` (added in slice 3b).

## Global Constraints

- No new Go module dependencies.
- The compiler-side change is one position: `Pos ast.Pos` on `ast.FieldType`, set by the parser from the field-name token. The type checker/emitter are NOT touched. AST dumps omit positions → parse/dump tests stay green.
- LSP containment rule: a `DocumentSymbol`'s child ranges MUST be contained within the parent's `range`. The type symbol's `range` therefore spans from its position to its furthest member; its `selectionRange` stays the name span.
- Symbol kinds (LSP `SymbolKind`): `Field = 8`, `EnumMember = 22` (new); existing `Enum = 10`, `Function = 12`, `Variable = 13`, `Struct = 23`.
- Only `TypeDecl` gains children; `LetDecl` (functions/values) stay leaf symbols.
- ADT constructors → `EnumMember` children; record fields → `Field` children.
- `selectionRange` for a type stays keyword-based (at `TypeDecl.Pos`) — a documented v1 limitation.
- Positions are 1-based in `ast`/`parse`; `documentSymbols` emits 0-based LSP ranges (the existing `symbolAt` already converts).
- `pos(tok token.Token)` (parse.go:88) returns a token's 1-based `ast.Pos`.

## File structure

- `internal/ast/ast.go` — `Pos` on `FieldType` (Task 1).
- `internal/parse/parse.go` — set `FieldType.Pos` (Task 1).
- `internal/lsp/protocol.go` — `DocumentSymbol.Children` + two kind consts (Task 2).
- `internal/lsp/symbols.go` — hierarchical `TypeDecl` symbols (Task 2).
- `editor/lsp.md` + `CLAUDE.md` — docs (Task 3).

---

### Task 1: Parser — `FieldType.Pos`

**Files:**
- Modify: `internal/ast/ast.go` (`FieldType` struct), `internal/parse/parse.go:296`
- Test: `internal/parse/fieldtypepos_test.go` (create)

**Interfaces:**
- Consumes: `pos(token.Token) ast.Pos`.
- Produces: `ast.FieldType.Pos` — the 1-based position of a record field's name.

- [ ] **Step 1: Write the failing test**

Create `internal/parse/fieldtypepos_test.go`:

```go
package parse

import (
	"testing"

	"github.com/incantery/sigil/internal/ast"
)

func TestFieldTypePositionRecorded(t *testing.T) {
	// "type Point = { x: Int, y: Int }" — field `x` is at col 16, `y` at col 24 (1-based):
	// t1 y2 p3 e4 ' '5 P6 o7 i8 n9 t10 ' '11 =12 ' '13 {14 ' '15 x16 ... ,22 ' '23 y24
	m, err := Module("type Point = { x: Int, y: Int }\n")
	if err != nil {
		t.Fatal(err)
	}
	td := m.Decls[0].(*ast.TypeDecl)
	if len(td.Record) != 2 {
		t.Fatalf("got %d record fields, want 2", len(td.Record))
	}
	if td.Record[0].Pos.Line != 1 || td.Record[0].Pos.Col != 16 {
		t.Errorf("field x pos = %d:%d, want 1:16", td.Record[0].Pos.Line, td.Record[0].Pos.Col)
	}
	if td.Record[1].Pos.Col != 24 {
		t.Errorf("field y pos col = %d, want 24", td.Record[1].Pos.Col)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/parse/ -run TestFieldTypePosition -v`
Expected: FAIL — `ast.FieldType` has no field `Pos`.

- [ ] **Step 3: Add `Pos` to `FieldType` in `internal/ast/ast.go`**

Change the `FieldType` struct (currently `type FieldType struct { Name string; Type TypeExpr }`) to:

```go
type FieldType struct {
	Pos  Pos
	Name string
	Type TypeExpr
}
```

- [ ] **Step 4: Set the position in `internal/parse/parse.go`**

In `parseRecordFieldTypes`, change the field construction (currently `fields = append(fields, &ast.FieldType{Name: name.Lit, Type: t})`) to:

```go
		fields = append(fields, &ast.FieldType{Pos: pos(name), Name: name.Lit, Type: t})
```

(`name` is the `p.expect(token.IDENT)` result — the field's own name token.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/parse/ ./internal/ast/ ./internal/types/ ./internal/emit/`
Expected: PASS (new test + existing parse/dump/type/emit suites — `Pos` is additive, dumps omit it).

- [ ] **Step 6: Commit**

```bash
git add internal/ast/ast.go internal/parse/parse.go internal/parse/fieldtypepos_test.go
git commit -m "feat(parse): record source position on record field types"
```

---

### Task 2: Hierarchical type symbols

**Files:**
- Modify: `internal/lsp/protocol.go` (`DocumentSymbol.Children` + kind consts), `internal/lsp/symbols.go`
- Test: `internal/lsp/symbols_test.go` (add hierarchical tests)

**Interfaces:**
- Consumes: `ast.TypeDecl.Variants` (`[]*ast.Variant` with `.Pos`/`.Name`), `ast.TypeDecl.Record` (`[]*ast.FieldType` with `.Pos`/`.Name` from Task 1), the existing `symbolAt(name, kind, pos)`.
- Produces: `DocumentSymbol.Children []DocumentSymbol`; `SymbolKindField = 8`, `SymbolKindEnumMember = 22`; hierarchical `documentSymbols` output.

- [ ] **Step 1: Write the failing test**

Add to `internal/lsp/symbols_test.go`:

```go
func TestDocumentSymbolsHierarchical(t *testing.T) {
	src := `pub type Color = Red | Green | Blue
type Point = { x: Int, y: Int }
let f x = x
`
	syms := documentSymbols(src)
	if len(syms) != 3 {
		t.Fatalf("want 3 top-level symbols, got %d", len(syms))
	}
	byName := map[string]DocumentSymbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}

	// ADT: Color is an Enum with three EnumMember children.
	color := byName["Color"]
	if color.Kind != SymbolKindEnum {
		t.Errorf("Color kind = %d, want Enum(%d)", color.Kind, SymbolKindEnum)
	}
	if len(color.Children) != 3 {
		t.Fatalf("Color has %d children, want 3", len(color.Children))
	}
	wantCtors := []string{"Red", "Green", "Blue"}
	for i, c := range color.Children {
		if c.Name != wantCtors[i] || c.Kind != SymbolKindEnumMember {
			t.Errorf("child %d = %q kind %d, want %q EnumMember(%d)", i, c.Name, c.Kind, wantCtors[i], SymbolKindEnumMember)
		}
	}
	// Containment: each child's range is inside Color's range. Uses the package
	// helper posBeforeSym defined in symbols.go (Step 3/4).
	for _, c := range color.Children {
		if posBeforeSym(c.Range.Start, color.Range.Start) || posBeforeSym(color.Range.End, c.Range.End) {
			t.Errorf("child %q range %+v not contained in parent %+v", c.Name, c.Range, color.Range)
		}
	}

	// Record: Point is a Struct with two Field children.
	point := byName["Point"]
	if point.Kind != SymbolKindStruct {
		t.Errorf("Point kind = %d, want Struct(%d)", point.Kind, SymbolKindStruct)
	}
	if len(point.Children) != 2 || point.Children[0].Name != "x" || point.Children[0].Kind != SymbolKindField {
		t.Errorf("Point children = %+v, want [x,y] of kind Field", point.Children)
	}

	// Function stays a leaf (no children).
	if f := byName["f"]; f.Kind != SymbolKindFunction || len(f.Children) != 0 {
		t.Errorf("f = kind %d, %d children; want Function(%d) leaf", f.Kind, len(f.Children), SymbolKindFunction)
	}
}
```

(The containment check uses `posBeforeSym`, defined in `symbols.go` in Steps 3–4. At the RED step the test won't compile because `Children`/`SymbolKindEnumMember`/`posBeforeSym` are all undefined yet — that is the expected failure.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lsp/ -run TestDocumentSymbolsHierarchical -v`
Expected: FAIL — `Children` / `SymbolKindEnumMember` undefined.

- [ ] **Step 3: Add the protocol fields in `internal/lsp/protocol.go`**

Add the two kind constants alongside the existing ones (`SymbolKindEnum = 10`, etc.):

```go
	SymbolKindField      = 8
	SymbolKindEnumMember = 22
```

Add `Children` to the `DocumentSymbol` struct (it currently has `Name`, `Kind`, `Range`, `SelectionRange`):

```go
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}
```

- [ ] **Step 4: Make `TypeDecl` symbols hierarchical in `internal/lsp/symbols.go`**

Replace the `*ast.TypeDecl` case in `documentSymbols` (currently builds a flat `symbolAt(d.Name, kind, d.Pos)`) with a call to a new `typeSymbol` helper:

```go
		case *ast.TypeDecl:
			syms = append(syms, typeSymbol(d))
```

Add the helper functions (after `symbolAt`):

```go
// typeSymbol builds a hierarchical symbol for a type declaration: an Enum with
// EnumMember children (ADT constructors) or a Struct with Field children (record
// fields). The parent range is expanded to contain its children (LSP requires
// child ranges to lie within the parent range); selectionRange stays the name.
func typeSymbol(d *ast.TypeDecl) DocumentSymbol {
	var children []DocumentSymbol
	kind := SymbolKindEnum
	if d.Record != nil {
		kind = SymbolKindStruct
		for _, f := range d.Record {
			children = append(children, symbolAt(f.Name, SymbolKindField, f.Pos))
		}
	} else {
		for _, v := range d.Variants {
			children = append(children, symbolAt(v.Name, SymbolKindEnumMember, v.Pos))
		}
	}
	sym := symbolAt(d.Name, kind, d.Pos) // range == selectionRange == name span
	if len(children) > 0 {
		sym.Children = children
		end := sym.Range.End
		for _, c := range children {
			if posBeforeSym(end, c.Range.End) {
				end = c.Range.End
			}
		}
		sym.Range = Range{Start: sym.Range.Start, End: end} // contains all children
	}
	return sym
}

// posBeforeSym reports whether a is strictly before b (0-based LSP positions).
func posBeforeSym(a, b Position) bool {
	return a.Line < b.Line || (a.Line == b.Line && a.Character < b.Character)
}
```

(The `*ast.LetDecl` case is unchanged — functions/values stay leaf symbols.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/lsp/`
Expected: PASS — the new hierarchical test AND the existing `TestDocumentSymbolsKinds` (top-level count is still 3-or-4 and kinds unchanged; that test does not inspect `Children`).

- [ ] **Step 6: Commit**

```bash
git add internal/lsp/protocol.go internal/lsp/symbols.go internal/lsp/symbols_test.go
git commit -m "feat(lsp): hierarchical document symbols (type members as children)"
```

---

### Task 3: Docs

**Files:**
- Modify: `editor/lsp.md`, `CLAUDE.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: Update `editor/lsp.md`**

Update the document-symbols bullet in "What it provides (v1)" to note nesting. Replace the existing symbols bullet (which says symbols are flat) with:

```markdown
- **Document symbols** — top-level `let`/`type` declarations for the outline /
  symbol picker, with `type` declarations **nested**: an ADT shows its
  constructors and a record shows its fields as child symbols.
```

- [ ] **Step 2: Update `CLAUDE.md`**

In "What's next", under the editor roadmap, note that the **hierarchical document symbols** follow-up (deferred from #2, unblocked by slice 3b's binder positions) is **DONE** — `type` declarations now nest their constructors/fields as child symbols (`FieldType` gained a position; `Variant` already had one). Remaining type-aware work: **3c semantic tokens**, then **#4 completion**.

Make targeted edits matching the surrounding prose; don't restructure.

- [ ] **Step 3: Full-repo validation**

Run: `go build ./... && go test ./...`
Expected: PASS (browser tests run or skip).

- [ ] **Step 4: Commit**

```bash
git add editor/lsp.md CLAUDE.md
git commit -m "docs: hierarchical LSP document symbols"
```

---

## Self-Review

**Spec coverage:**
- §1 parser `FieldType.Pos` → Task 1. ✓
- §2 protocol `Children` + `Field`/`EnumMember` kinds → Task 2. ✓
- §3 hierarchical `symbols.go` (ADT→Enum+EnumMember, record→Struct+Field, parent containment, leaf functions, no-member fallback) → Task 2. ✓
- §Edge cases (parse failure → empty; destructuring let skipped; keyword-based selectionRange) → unchanged `documentSymbols` head + `symbolAt` (verified by existing tests). ✓
- §Testing (FieldType.Pos column; ADT children; record children; function leaf; containment; existing flat test still passes) → Tasks 1, 2. ✓
- §Docs → Task 3. ✓

**Placeholder scan:** No TBD/TODO. Every code step shows complete code; every run step states the command + expected result.

**Type consistency:** `ast.FieldType.Pos` (Task 1) consumed by `typeSymbol` (Task 2). `SymbolKindField`/`SymbolKindEnumMember` + `DocumentSymbol.Children` (Task 2 protocol) consumed by `typeSymbol`/the test (Task 2). `symbolAt(name, kind, pos)` reused unchanged. The containment helper `posBeforeSym` is defined once in `symbols.go` (Task 2) and reused by the test in the same package — no duplicate helper. `Position`/`Range` reused from existing protocol. ✓
```
