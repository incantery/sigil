# `sigil lsp` Slice 3c — Semantic Tokens Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `textDocument/semanticTokens/full` — type-aware coloring that classifies every non-comment token by role (type / constructor / function / parameter / variable / property) plus the lexical categories (keyword / operator / number / string).

**Architecture:** Add name/pattern positions to a few AST nodes (parser). A new `internal/analysis/semantic.go` builds a structural `position → role` map from the parsed AST (no type info) and a function-name set, then lexes the document and maps every token to a legend index, delta-encoding to the LSP `[]uint`. A thin `internal/lsp` handler serves `semanticTokens/full` off the in-memory buffer.

**Tech Stack:** Go stdlib only. Reuses `internal/lex`, `internal/parse`, `internal/ast`, the binder positions from prior slices, and the `internal/lsp` doc store + pipe harness. No new dependencies.

## Global Constraints

- No new Go module dependencies. Semantic tokens use NO type information — a plain `parse.Module(text)` + `lex.Lex(text)` (no `load`, no imports, no checker).
- Parser additions (additive, dump-safe — AST dumps omit positions): `NamePos ast.Pos` on `LetDecl` and `TypeDecl` (the declaration name), `Pos ast.Pos` on `CtorPat` (constructor pattern). Set from the identifier token via `pos(tok)` (parse.go:88).
- `Role` enum order IS the legend index for roles: `RoleType=0, RoleEnumMember=1, RoleFunction=2, RoleParameter=3, RoleVariable=4, RoleProperty=5`. The legend is `["type","enumMember","function","parameter","variable","property","keyword","operator","number","string"]` (keyword=6, operator=7, number=8, string=9).
- Use classification is heuristic: a lowercase use colors `function` if its name is in the file's function-name set (any `let` with params), else `variable`. Declarations are precise.
- Token → semantic type: identifier (`IDENT`/`UIDENT`/`HOLE`) at a role-map position → that role; miss → fallback (`UIDENT`→type, lowercase→function/variable via the function-name set). Keyword kinds (`LET`..`EFFECT`)→keyword; operator kinds (`PIPEFWD`..`BANG`, plus `EQ`/`ARROW`/`PIPE`)→operator; `INT`/`FLOAT`→number; `STRING`→string; everything else (layout/punctuation/`EOF`/`UNDERSCORE`) is skipped.
- Delta encoding: 5 ints per emitted token — `deltaLine` (line−prevLine), `deltaStartChar` (col−prevCol same line, else absolute col), `length`, `tokenType` (legend index), `tokenModifiers` (always 0). Positions are 1-based in lexer/AST → converted to 0-based at encode time.
- Token positions are 1-based `{Line, Col}`; the lexer and parser share positions, so a role-map keyed by `ast.Pos{Line, Col}` matches identifier tokens exactly.
- Comments are dropped by the lexer (out of scope). String interpolation holes are not separately classified (the whole `STRING` is `string`).

## File structure

- `internal/ast/ast.go` — `NamePos`/`CtorPat.Pos` (Task 1).
- `internal/parse/parse.go` — set them (Task 1).
- `internal/analysis/semantic.go` — `Role`, `collectFunctionNames`, `SemanticRoles`, `SemanticTokens` (Tasks 2–3).
- `internal/lsp/protocol.go` + `server.go` — capability + handler (Task 4).
- `editor/lsp.md` + `CLAUDE.md` — docs (Task 5).

---

### Task 1: Parser — name/pattern positions

**Files:**
- Modify: `internal/ast/ast.go` (`LetDecl`, `TypeDecl`, `CtorPat`), `internal/parse/parse.go`
- Test: `internal/parse/namepos_test.go` (create)

**Interfaces:**
- Consumes: `pos(token.Token) ast.Pos`.
- Produces: `ast.LetDecl.NamePos`, `ast.TypeDecl.NamePos`, `ast.CtorPat.Pos`.

- [ ] **Step 1: Write the failing test**

Create `internal/parse/namepos_test.go`:

```go
package parse

import (
	"testing"

	"github.com/incantery/sigil/internal/ast"
)

func TestLetAndTypeNamePos(t *testing.T) {
	// "let inc n = n"  — name `inc` at 1:5
	// "type Color = Red" — name `Color` at 2:6, ctor `Red` at 2:14
	m, err := Module("let inc n = n\ntype Color = Red\n")
	if err != nil {
		t.Fatal(err)
	}
	ld := m.Decls[0].(*ast.LetDecl)
	if ld.NamePos.Line != 1 || ld.NamePos.Col != 5 {
		t.Errorf("LetDecl NamePos = %d:%d, want 1:5", ld.NamePos.Line, ld.NamePos.Col)
	}
	td := m.Decls[1].(*ast.TypeDecl)
	if td.NamePos.Line != 2 || td.NamePos.Col != 6 {
		t.Errorf("TypeDecl NamePos = %d:%d, want 2:6", td.NamePos.Line, td.NamePos.Col)
	}
}

func TestCtorPatPos(t *testing.T) {
	// "let f c = match c with | Some y -> y" — Some (CtorPat) at 1:26
	// l1 e2 t3 ' '4 f5 ' '6 c7 ' '8 =9 ' '10 m11 a12 t13 c14 h15 ' '16 c17 ' '18 w19 i20 t21 h22 ' '23 |24 ' '25 S26
	m, err := Module("let f c = match c with | Some y -> y\n")
	if err != nil {
		t.Fatal(err)
	}
	ld := m.Decls[0].(*ast.LetDecl)
	mt := ld.Body.(*ast.Match)
	cp := mt.Arms[0].Pat.(ast.CtorPat)
	if cp.Pos.Line != 1 || cp.Pos.Col != 26 {
		t.Errorf("CtorPat Pos = %d:%d, want 1:26", cp.Pos.Line, cp.Pos.Col)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/parse/ -run 'TestLetAndTypeNamePos|TestCtorPatPos' -v`
Expected: FAIL — `NamePos` / `CtorPat.Pos` undefined.

- [ ] **Step 3: Add fields in `internal/ast/ast.go`**

Add `NamePos Pos` to `LetDecl` (after `Pos Pos`):

```go
type LetDecl struct {
	Pos     Pos
	NamePos Pos // position of the bound name (zero if a destructuring let)
	Pub     bool
	Rec     bool
	Name    string
	Params  []Param
	Pat     Pattern
	Body    Expr
}
```

Add `NamePos Pos` to `TypeDecl` (after `Pos Pos`):

```go
type TypeDecl struct {
	Pos      Pos
	NamePos  Pos
	Pub      bool
	Name     string
	Params   []string
	Variants []*Variant
	Record   []*FieldType
}
```

Add `Pos Pos` to `CtorPat` (first field):

```go
	CtorPat struct {
		Pos  Pos
		Name string
		Args []Pattern
	}
```

- [ ] **Step 4: Set the positions in `internal/parse/parse.go`**

In `parseLetDecl`, the named branch currently does `d.Name = p.advance().Lit`. Capture the token:

```go
	if p.at(token.IDENT) {
		nameTok := p.advance()
		d.Name = nameTok.Lit
		d.NamePos = pos(nameTok)
		params, err := p.parseParams()
		if err != nil {
			return nil, err
		}
		d.Params = params
	} else {
```

In `parseTypeDecl`, after `name, err := p.expect(token.UIDENT)`, set `NamePos` on the decl (it is currently `d := &ast.TypeDecl{Pos: pos(start), Pub: pub, Name: name.Lit}` further down — add `NamePos: pos(name)`):

```go
	d := &ast.TypeDecl{Pos: pos(start), NamePos: pos(name), Pub: pub, Name: name.Lit}
```

In `parsePattern` (the `CtorPat` with args, currently `cp := ast.CtorPat{Name: name}` where `name` is the UIDENT's `.Lit` — capture the token instead) and `parsePatternAtom` (the nullary `return ast.CtorPat{Name: t.Lit}, nil`):

For the args branch (around parse.go:855-857) — the constructor token is captured as a token before `.Lit` is taken; set `Pos` from it. The current code is:
```go
		name := p.advance().Lit
		cp := ast.CtorPat{Name: name}
```
Change to:
```go
		nameTok := p.advance()
		cp := ast.CtorPat{Pos: pos(nameTok), Name: nameTok.Lit}
```

For the nullary branch in `parsePatternAtom` (`case token.UIDENT:` → `return ast.CtorPat{Name: t.Lit}, nil`), `t` is already the token:
```go
		return ast.CtorPat{Pos: pos(t), Name: t.Lit}, nil
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/parse/ ./internal/ast/ ./internal/types/ ./internal/emit/ ./internal/analysis/`
Expected: PASS (new tests + existing suites — positions are additive, dumps omit them).

- [ ] **Step 6: Commit**

```bash
git add internal/ast/ast.go internal/parse/parse.go internal/parse/namepos_test.go
git commit -m "feat(parse): name positions on let/type decls + ctor pattern position"
```

---

### Task 2: `internal/analysis` — role classifier

**Files:**
- Create: `internal/analysis/semantic.go`
- Test: `internal/analysis/semantic_test.go` (create)

**Interfaces:**
- Consumes: `ast` nodes with positions (Task 1 + prior slices).
- Produces:
  - `type Role int` with `RoleType=0`, `RoleEnumMember=1`, `RoleFunction=2`, `RoleParameter=3`, `RoleVariable=4`, `RoleProperty=5`.
  - `func collectFunctionNames(m *ast.Module) map[string]bool` — names of every `let`/`Let` with params.
  - `func SemanticRoles(m *ast.Module) map[ast.Pos]Role` — position → role for classified identifier occurrences.

- [ ] **Step 1: Write the failing test**

Create `internal/analysis/semantic_test.go`:

```go
package analysis

import (
	"testing"

	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/parse"
)

func roleAt(t *testing.T, roles map[ast.Pos]Role, line, col int) Role {
	t.Helper()
	r, ok := roles[ast.Pos{Line: line, Col: col}]
	if !ok {
		t.Fatalf("no role recorded at %d:%d", line, col)
	}
	return r
}

func TestSemanticRoles(t *testing.T) {
	src := "type Color = Red\n" + // Color@1:6 type, Red@1:14 enumMember
		"let inc n = n\n" + //   inc@2:5 function, n@2:9 parameter, n@2:13 variable(use)
		"let r = inc\n" + //     r@3:5 variable, inc@3:9 function(use, in funcNames)
		"let g x = x.field\n" + // g@4:5 function, x@4:7 parameter, x@4:11 variable, field@4:13 property
		"let h c = match c with | Some y -> y\n" // Some@5:26 enumMember, y@5:31 variable
	m, err := parse.Module(src)
	if err != nil {
		t.Fatal(err)
	}
	roles := SemanticRoles(m)

	cases := []struct {
		line, col int
		want      Role
		what      string
	}{
		{1, 6, RoleType, "Color (type decl)"},
		{1, 14, RoleEnumMember, "Red (variant)"},
		{2, 5, RoleFunction, "inc (fn decl)"},
		{2, 9, RoleParameter, "n (param)"},
		{2, 13, RoleVariable, "n (use)"},
		{3, 5, RoleVariable, "r (value decl)"},
		{3, 9, RoleFunction, "inc (fn use)"},
		{4, 13, RoleProperty, "field (access)"},
		{5, 26, RoleEnumMember, "Some (ctor pattern)"},
	}
	for _, c := range cases {
		if got := roleAt(t, roles, c.line, c.col); got != c.want {
			t.Errorf("%s @ %d:%d = role %d, want %d", c.what, c.line, c.col, got, c.want)
		}
	}
}

func TestCollectFunctionNames(t *testing.T) {
	m, _ := parse.Module("let inc n = n\nlet v = 1\n")
	fns := collectFunctionNames(m)
	if !fns["inc"] {
		t.Error("inc (has params) should be a function name")
	}
	if fns["v"] {
		t.Error("v (no params) should not be a function name")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/analysis/ -run 'TestSemanticRoles|TestCollectFunctionNames' -v`
Expected: FAIL — `Role` / `SemanticRoles` undefined.

- [ ] **Step 3: Implement `internal/analysis/semantic.go`**

```go
package analysis

import "github.com/incantery/sigil/internal/ast"

// Role is a semantic-token classification for an identifier. The numeric values
// are also the legend indices for those roles (see SemanticTokens).
type Role int

const (
	RoleType       Role = iota // 0
	RoleEnumMember             // 1
	RoleFunction               // 2
	RoleParameter              // 3
	RoleVariable               // 4
	RoleProperty               // 5
)

// collectFunctionNames returns the set of names bound to a function (a let with
// parameters) anywhere in the module — used to color lowercase *uses*.
func collectFunctionNames(m *ast.Module) map[string]bool {
	fns := map[string]bool{}
	var visit func(e ast.Expr)
	visit = func(e ast.Expr) {
		switch e := e.(type) {
		case *ast.Let:
			if e.Name != "" && len(e.Params) > 0 {
				fns[e.Name] = true
			}
			visit(e.Body)
			visit(e.In)
		case *ast.Lambda:
			visit(e.Body)
		default:
			for _, ch := range children(e) {
				visit(ch)
			}
		}
	}
	for _, d := range m.Decls {
		if ld, ok := d.(*ast.LetDecl); ok {
			if ld.Name != "" && len(ld.Params) > 0 {
				fns[ld.Name] = true
			}
			if ld.Body != nil {
				visit(ld.Body)
			}
		}
	}
	return fns
}

// SemanticRoles returns position → role for every identifier occurrence the
// structure classifies. Identifiers not present here fall back to the lexical
// default in SemanticTokens. No type information is used.
func SemanticRoles(m *ast.Module) map[ast.Pos]Role {
	fns := collectFunctionNames(m)
	roles := map[ast.Pos]Role{}
	put := func(p ast.Pos, r Role) {
		if p.Line != 0 { // skip zero positions (unset)
			roles[p] = r
		}
	}

	var visitExpr func(e ast.Expr)
	var visitPat func(p ast.Pattern)

	visitParams := func(params []ast.Param) {
		for _, p := range params {
			switch p := p.(type) {
			case ast.VarParam:
				put(p.Pos, RoleParameter)
			case ast.PatParam:
				visitPat(p.Pat)
			case ast.RecordParam:
				for _, f := range p.Fields {
					put(f.Pos, RoleParameter)
				}
			}
		}
	}

	visitPat = func(p ast.Pattern) {
		switch p := p.(type) {
		case ast.VarPat:
			put(p.Pos, RoleVariable)
		case ast.CtorPat:
			put(p.Pos, RoleEnumMember)
			for _, a := range p.Args {
				visitPat(a)
			}
		case ast.TuplePat:
			for _, e := range p.Elems {
				visitPat(e)
			}
		case ast.ListPat:
			for _, e := range p.Elems {
				visitPat(e)
			}
		case ast.RecordPat:
			for _, f := range p.Fields {
				if f.Pat == nil {
					put(f.Pos, RoleVariable)
				} else {
					visitPat(f.Pat)
				}
			}
		}
	}

	visitExpr = func(e ast.Expr) {
		switch e := e.(type) {
		case *ast.Var:
			if fns[e.Name] {
				put(e.Pos, RoleFunction)
			} else {
				put(e.Pos, RoleVariable)
			}
		case *ast.Ctor:
			put(e.Pos, RoleEnumMember)
		case *ast.Field:
			put(e.Pos, RoleProperty) // Field.Pos is the field name
			visitExpr(e.Recv)
		case *ast.Lambda:
			visitParams(e.Params)
			visitExpr(e.Body)
		case *ast.Let:
			// block-let name uses the lexical fallback (no NamePos); bind params.
			visitParams(e.Params)
			if e.Name == "" {
				visitPat(e.Pat)
			}
			visitExpr(e.Body)
			visitExpr(e.In)
		case *ast.Match:
			visitExpr(e.Scrut)
			for _, arm := range e.Arms {
				visitPat(arm.Pat)
				if arm.Guard != nil {
					visitExpr(arm.Guard)
				}
				visitExpr(arm.Body)
			}
		default:
			for _, ch := range children(e) {
				visitExpr(ch)
			}
		}
	}

	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			if d.Name != "" {
				if len(d.Params) > 0 {
					put(d.NamePos, RoleFunction)
				} else {
					put(d.NamePos, RoleVariable)
				}
			} else {
				visitPat(d.Pat)
			}
			visitParams(d.Params)
			if d.Body != nil {
				visitExpr(d.Body)
			}
		case *ast.TypeDecl:
			put(d.NamePos, RoleType)
			for _, v := range d.Variants {
				put(v.Pos, RoleEnumMember)
			}
			for _, f := range d.Record {
				put(f.Pos, RoleProperty)
			}
		}
	}
	return roles
}
```

Note: `children(e)` is the shared walker from `index.go` (same package). The `default` cases recurse into all sub-expressions (App, Binop, If, Tuple, ListLit, RecordLit, Interp, Effect, Unop, literals) with no special handling — those carry no role-bearing identifiers beyond what their children expose.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/analysis/ -run 'TestSemanticRoles|TestCollectFunctionNames' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/semantic.go internal/analysis/semantic_test.go
git commit -m "feat(analysis): structural semantic-token role classifier"
```

---

### Task 3: `internal/analysis` — token encoder

**Files:**
- Modify: `internal/analysis/semantic.go` (add `SemanticTokens` + helpers)
- Test: `internal/analysis/semantic_test.go` (add encoder tests)

**Interfaces:**
- Consumes: `SemanticRoles`/`collectFunctionNames`/`Role` (Task 2), `lex.Lex`, `parse.Module`, `token` kinds.
- Produces: `func SemanticTokens(text string) []uint` — the LSP semantic-tokens `data` array (5 ints per token).

- [ ] **Step 1: Write the failing test**

Add to `internal/analysis/semantic_test.go`:

```go
func TestSemanticTokensEncoding(t *testing.T) {
	// Two lines exercise single-line deltas and the cross-line reset.
	//   line 1: "let x = 1"   LET kw@0:0 len3, x var@0:4 len1, = op@0:6 len1, 1 num@0:8 len1
	//   line 2: "let y = x"   LET kw@1:0 len3, y var@1:4 len1, = op@1:6 len1, x var@1:8 len1
	data := SemanticTokens("let x = 1\nlet y = x\n")
	want := []uint{
		0, 0, 3, 6, 0, // let     (keyword=6)
		0, 4, 1, 4, 0, // x        (variable=4)
		0, 2, 1, 7, 0, // =        (operator=7)
		0, 2, 1, 8, 0, // 1        (number=8)
		1, 0, 3, 6, 0, // let      (deltaLine=1, absolute col 0)
		0, 4, 1, 4, 0, // y        (variable=4)
		0, 2, 1, 7, 0, // =        (operator=7)
		0, 2, 1, 4, 0, // x (use)  (variable=4)
	}
	if len(data) != len(want) {
		t.Fatalf("len(data) = %d, want %d\n got: %v", len(data), len(want), data)
	}
	for i := range want {
		if data[i] != want[i] {
			t.Fatalf("data[%d] = %d, want %d\n got:  %v\n want: %v", i, data[i], want[i], data, want)
		}
	}
}

func TestSemanticTokensRolesAndKinds(t *testing.T) {
	// type/enumMember/function/string coverage in one line each; just check the
	// tokenType (4th of each 5-tuple) for the identifiers/strings of interest.
	data := SemanticTokens("type C = Red\nlet greet n = \"hi\"\n")
	// Collect (tokenType) values; we only assert presence of the right indices.
	types := map[uint]bool{}
	for i := 3; i < len(data); i += 5 {
		types[data[i]] = true
	}
	for _, idx := range []uint{0 /*type C*/, 1 /*enumMember Red*/, 2 /*function greet*/, 9 /*string "hi"*/} {
		if !types[idx] {
			t.Errorf("expected a token of legend type %d in %v", idx, data)
		}
	}
}

func TestSemanticTokensParseErrorEmpty(t *testing.T) {
	if data := SemanticTokens("let x = ("); len(data) != 0 {
		t.Errorf("parse error should yield empty data, got %v", data)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/analysis/ -run TestSemanticTokens -v`
Expected: FAIL — `SemanticTokens` undefined.

- [ ] **Step 3: Implement the encoder in `internal/analysis/semantic.go`**

Add imports `"strings"`, `"github.com/incantery/sigil/internal/lex"`, `"github.com/incantery/sigil/internal/parse"`, `"github.com/incantery/sigil/internal/token"` to the file, and:

```go
// Legend indices for non-role token types (roles 0..5 are their own indices).
const (
	legendKeyword  = 6
	legendOperator = 7
	legendNumber   = 8
	legendString   = 9
)

// SemanticTokens returns the LSP semanticTokens/full `data` array for the source:
// five ints per emitted token (deltaLine, deltaStartChar, length, tokenType,
// modifiers=0). A parse or lex error yields an empty slice.
func SemanticTokens(text string) []uint {
	m, err := parse.Module(text)
	if err != nil {
		return []uint{}
	}
	toks, err := lex.Lex(text)
	if err != nil {
		return []uint{}
	}
	roles := SemanticRoles(m)
	fns := collectFunctionNames(m)
	lines := strings.Split(text, "\n")

	data := []uint{}
	prevLine, prevCol := 0, 0
	for _, t := range toks {
		typ, ok := semanticType(t, roles, fns)
		if !ok {
			continue
		}
		line := t.Line - 1 // 0-based
		col := t.Col - 1
		length := tokenLength(t, lines)
		dLine := line - prevLine
		dCol := col
		if dLine == 0 {
			dCol = col - prevCol
		}
		data = append(data, uint(dLine), uint(dCol), uint(length), uint(typ), 0)
		prevLine, prevCol = line, col
	}
	return data
}

// semanticType maps a token to its legend index, or ok=false to skip it.
func semanticType(t token.Token, roles map[ast.Pos]Role, fns map[string]bool) (int, bool) {
	switch t.Kind {
	case token.IDENT, token.UIDENT, token.HOLE:
		if r, ok := roles[ast.Pos{Line: t.Line, Col: t.Col}]; ok {
			return int(r), true
		}
		if t.Kind == token.UIDENT {
			return int(RoleType), true
		}
		if fns[t.Lit] {
			return int(RoleFunction), true
		}
		return int(RoleVariable), true
	case token.INT, token.FLOAT:
		return legendNumber, true
	case token.STRING:
		return legendString, true
	}
	if t.Kind >= token.LET && t.Kind <= token.EFFECT {
		return legendKeyword, true
	}
	if (t.Kind >= token.PIPEFWD && t.Kind <= token.BANG) ||
		t.Kind == token.EQ || t.Kind == token.ARROW || t.Kind == token.PIPE {
		return legendOperator, true
	}
	return 0, false // layout, punctuation, EOF, UNDERSCORE
}

// tokenLength returns a token's source length in characters.
func tokenLength(t token.Token, lines []string) int {
	if t.Kind == token.STRING {
		return stringLength(lines, t.Line, t.Col)
	}
	if t.Lit != "" {
		return len([]rune(t.Lit))
	}
	return len([]rune(t.Kind.String())) // keywords/operators: canonical spelling
}

// stringLength measures a single-line string literal (sigil forbids raw newlines
// in strings) from its opening quote at 1-based (line,col), counting both quotes
// and honoring backslash escapes.
func stringLength(lines []string, line, col int) int {
	if line-1 < 0 || line-1 >= len(lines) {
		return 2
	}
	rs := []rune(lines[line-1])
	i := col - 1 // index of the opening quote
	if i < 0 || i >= len(rs) || rs[i] != '"' {
		return 2
	}
	n := 1 // opening quote
	for j := i + 1; j < len(rs); j++ {
		n++
		if rs[j] == '\\' && j+1 < len(rs) {
			j++
			n++
			continue
		}
		if rs[j] == '"' {
			break // closing quote counted
		}
	}
	return n
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/analysis/`
Expected: PASS (encoding golden + roles/kinds + parse-error-empty + the Task 2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/semantic.go internal/analysis/semantic_test.go
git commit -m "feat(analysis): semantic-token encoder (lex + delta-encode)"
```

---

### Task 4: `internal/lsp` — capability + handler + integration test

**Files:**
- Modify: `internal/lsp/protocol.go` (capability + params/result), `internal/lsp/server.go` (capability value + dispatch + handler)
- Test: `internal/lsp/semantic_test.go` (create)

**Interfaces:**
- Consumes: `analysis.SemanticTokens` (Task 3), the doc store, the pipe harness.
- Produces:
  - `ServerCapabilities.SemanticTokensProvider` (advertises the legend + `full`).
  - `SemanticTokensParams`, `SemanticTokens` (result) structs.
  - `textDocument/semanticTokens/full` dispatch case + `handleSemanticTokens`.

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/semantic_test.go`:

```go
package lsp

import (
	"io"
	"path/filepath"
	"testing"
)

func TestSemanticTokensFull(t *testing.T) {
	root := t.TempDir()
	src := "let inc n = n + 1\n"
	writeFile(t, filepath.Join(root, "app.sigil"), src)
	uri := "file://" + filepath.Join(root, "app.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"let inc n = n + 1\n"}}}`)
	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/semanticTokens/full","params":{"textDocument":{"uri":"`+uri+`"}}}`)
	// The reply carries a non-empty "data" array; the first token is `let`
	// (keyword=6) at delta 0,0 length 3 → starts with [0,0,3,6,0,...
	waitFor(t, &out, `"data":[0,0,3,6,0`)
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func TestSemanticTokensCapabilityAdvertised(t *testing.T) {
	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()
	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///tmp"}}`)
	waitFor(t, &out, "semanticTokensProvider")
	waitFor(t, &out, "enumMember") // the legend is present
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lsp/ -run TestSemanticTokens -v`
Expected: FAIL — no handler / capability.

- [ ] **Step 3: Add protocol structs + capability in `internal/lsp/protocol.go`**

Add the legend value and capability struct:

```go
// SemanticTokenTypes is the legend; an index into it is a token's type. Roles
// 0..5 (analysis.Role) map to the first six entries.
var SemanticTokenTypes = []string{
	"type", "enumMember", "function", "parameter", "variable", "property",
	"keyword", "operator", "number", "string",
}

type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

type SemanticTokensOptions struct {
	Legend SemanticTokensLegend `json:"legend"`
	Full   bool                 `json:"full"`
}

type SemanticTokensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type SemanticTokens struct {
	Data []uint `json:"data"`
}
```

Add the field to `ServerCapabilities`:

```go
	SemanticTokensProvider *SemanticTokensOptions `json:"semanticTokensProvider,omitempty"`
```

- [ ] **Step 4: Advertise + handle in `internal/lsp/server.go`**

In the `initialize` reply capabilities, add the provider (a pointer so `omitempty` works):

```go
			SemanticTokensProvider: &SemanticTokensOptions{
				Legend: SemanticTokensLegend{TokenTypes: SemanticTokenTypes, TokenModifiers: []string{}},
				Full:   true,
			},
```

Add a dispatch case (before `default`):

```go
	case "textDocument/semanticTokens/full":
		s.handleSemanticTokens(msg)
```

Add the handler (alongside the others — `analysis` is already imported by server.go from hover/definition):

```go
func (s *Server) handleSemanticTokens(msg *Message) {
	var p SemanticTokensParams
	_ = json.Unmarshal(msg.Params, &p)
	text, _ := s.docs.get(p.TextDocument.URI)
	_ = s.conn.Reply(msg.ID, SemanticTokens{Data: analysis.SemanticTokens(text)})
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/lsp/`
Expected: PASS (semantic-tokens full + capability + all prior lsp tests).

- [ ] **Step 6: Commit**

```bash
git add internal/lsp/protocol.go internal/lsp/server.go internal/lsp/semantic_test.go
git commit -m "feat(lsp): textDocument/semanticTokens/full"
```

---

### Task 5: Docs

**Files:**
- Modify: `editor/lsp.md`, `CLAUDE.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: Update `editor/lsp.md`**

Add a semantic-tokens bullet to "What it provides (v1)":

```markdown
- **Semantic tokens** — type-aware coloring: every identifier is classified by
  role (`type` / constructor / `function` / `parameter` / `variable` / record
  `property`), so the editor can distinguish a type from a constructor and a
  function from a local — disambiguation the grammar alone can't do. Keywords,
  operators, numbers, and strings are covered too (`semanticTokens/full`).
```

In "Not yet", remove "semantic tokens" if listed; keep completion / find-references / range-and-delta token requests as remaining.

- [ ] **Step 2: Update `CLAUDE.md`**

In "What's next", mark **#3 type-aware: slice 3c (semantic tokens) — DONE**, and note the **type-aware trio (hover, go-to-def, semantic tokens) is now complete**. The structural role classifier + delta encoder live in `internal/analysis/semantic.go`; the parser gained name positions on `let`/`type` decls and a constructor-pattern position. The remaining editor work is **#4 completion**. Make targeted edits matching the surrounding prose; don't restructure.

- [ ] **Step 3: Full-repo validation**

Run: `go build ./... && go test ./...`
Expected: PASS (browser tests run or skip).

- [ ] **Step 4: Commit**

```bash
git add editor/lsp.md CLAUDE.md
git commit -m "docs: sigil lsp semantic tokens (type-aware slice 3c)"
```

---

## Self-Review

**Spec coverage:**
- §1 parser positions (LetDecl/TypeDecl NamePos, CtorPat.Pos) → Task 1. ✓
- §2 role classifier (Role enum, collectFunctionNames, SemanticRoles walk over decls/exprs/patterns; type-expr UIDENTs left to fallback) → Task 2. ✓
- §3 token encoder (lex, semanticType map incl. keyword/operator/number/string + identifier fallback, tokenLength incl. STRING scan, delta-encode) → Task 3. ✓
- §4 lsp (capability + legend, params/result, handler off the buffer) → Task 4. ✓
- §Edge cases (parse error → empty; unclassified identifier → fallback; STRING whole-token; comment-only → empty) → Tasks 2/3 (empty-on-error test, fallback in semanticType). ✓
- §Testing (parser columns; roles per fixture; encoder golden incl. multi-line reset; lsp integration + capability) → Tasks 1–4. ✓
- §Docs → Task 5. ✓

**Placeholder scan:** No TBD/TODO. Every code step is complete; every run step has a command + expected result.

**Type consistency:** `ast.LetDecl.NamePos`/`TypeDecl.NamePos`/`CtorPat.Pos` (Task 1) consumed by `SemanticRoles` (Task 2). `Role` (0..5) / `SemanticRoles(m) map[ast.Pos]Role` / `collectFunctionNames` (Task 2) consumed by `SemanticTokens` (Task 3). `SemanticTokens(text) []uint` (Task 3) consumed by `handleSemanticTokens` (Task 4). The legend order in `SemanticTokenTypes` (Task 4) matches the role/index mapping in Task 2/3 (`type=0`…`property=5`, `keyword=6`…`string=9`). `children` reused from `index.go` (same package). `Position`/`Range` not used here (raw `[]uint`). ✓
```
