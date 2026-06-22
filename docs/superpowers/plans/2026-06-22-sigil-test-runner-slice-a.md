# Sigil test framework — Slice A (goja tier) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `test "name" { expect (eq a b) }` syntax, a `std/test` matcher library, and a `sigil test` CLI subcommand that compiles `*_test.sigil` files and runs them in goja, reporting pass/fail.

**Architecture:** `test`/`expect` are new keyword forms that lower to **test-only runtime hooks** (`__test`, `__expect`) injected by a dedicated *test prelude* — the same swap/concat pattern the dev prelude uses, so the ~24 kernel intrinsics are untouched and normal `build`/`serve`/`dev` never ship test code. Matchers are pure Sigil returning a structural record `{ pass, label, got, expected }`. A new `internal/testrun` package loads each `*_test.sigil`, bundles it with the test prelude, runs it in goja, and reads results back as JSON.

**Tech Stack:** Go, cobra (CLI), `github.com/dop251/goja` (JS engine, already a dependency), the existing `internal/{token,lex,parse,ast,types,emit,load,cli}` packages.

## Global Constraints

- `go build ./...` and `go test ./...` must stay green after every task.
- **No new Go dependencies.** goja and cobra are already vendored.
- **Do not add kernel intrinsics.** `__test`/`__expect`/`__runTests` exist only in the test prelude (JS), not in `internal/types` intrinsic registration; the ~24-intrinsic count stays put.
- **Keep `test`/`expect` syntax out of `std/` and `examples/`.** `make tree-sitter-verify` parses every file under `std/` and `examples/` and fails on ERROR nodes; the tree-sitter grammar does not know the new keywords. Therefore `std/test.sigil` (the matcher library) uses only existing syntax, and all `*_test.sigil` files live under a top-level `tests/` directory.
- Records in Sigil are **structural** (`TRecord`), so `Match` needs no nominal type registration — `expect`'s checker rule unifies its argument against a hardcoded `TRecord{pass,label,got,expected}`.
- Lambdas use `fun x -> body` (there is **no** `\` syntax). Matchers use the `let name params = body` form.

---

### Task 1: Front-end — `test`/`expect` tokens, AST, and parser

**Files:**
- Modify: `internal/token/token.go` (keyword block, `keywords` map, kind-name table)
- Modify: `internal/analysis/semantic.go:250` (keyword range bound)
- Modify: `internal/ast/ast.go` (new `TestDecl`, `TestStmt` nodes + markers)
- Modify: `internal/parse/parse.go` (`parseDecl` dispatch + `parseTestDecl`/`parseTestStmt`)
- Test: `internal/parse/parse_test.go` (add a test; create the file if absent)

**Interfaces:**
- Produces:
  - `token.TEST`, `token.EXPECT` (Kind values inside the contiguous keyword block).
  - `ast.TestDecl{ Pos, NamePos ast.Pos; Name string; Body []ast.TestStmt }` implementing `ast.Decl`.
  - `ast.TestStmt` interface with `*ast.TestLet{Pos; Name string; Value ast.Expr}`, `*ast.TestExpect{Pos; X ast.Expr}`, `*ast.TestRun{X ast.Expr}`.
  - `parse.Module(src)` parses `test "n" { ... }` into a `*ast.TestDecl`.

- [ ] **Step 1: Write the failing test**

Add to `internal/parse/parse_test.go` (create the file with `package parse` + imports if it does not exist):

```go
func TestParseTestDecl(t *testing.T) {
	src := `test "reverse swaps ends" {
  let xs = [1, 2];
  __set c 1;
  expect (eq xs [2, 1])
}`
	m, err := Module(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(m.Decls) != 1 {
		t.Fatalf("got %d decls, want 1", len(m.Decls))
	}
	td, ok := m.Decls[0].(*ast.TestDecl)
	if !ok {
		t.Fatalf("decl is %T, want *ast.TestDecl", m.Decls[0])
	}
	if td.Name != "reverse swaps ends" {
		t.Errorf("name = %q, want %q", td.Name, "reverse swaps ends")
	}
	if len(td.Body) != 3 {
		t.Fatalf("got %d stmts, want 3", len(td.Body))
	}
	if _, ok := td.Body[0].(*ast.TestLet); !ok {
		t.Errorf("stmt 0 is %T, want *ast.TestLet", td.Body[0])
	}
	if _, ok := td.Body[1].(*ast.TestRun); !ok {
		t.Errorf("stmt 1 is %T, want *ast.TestRun", td.Body[1])
	}
	if _, ok := td.Body[2].(*ast.TestExpect); !ok {
		t.Errorf("stmt 2 is %T, want *ast.TestExpect", td.Body[2])
	}
}
```

Make sure the file imports `"github.com/incantery/sigil/internal/ast"` and `"testing"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/parse/ -run TestParseTestDecl`
Expected: FAIL — compile error (`token.TEST` / `ast.TestDecl` undefined) or `expected declaration, got test`.

- [ ] **Step 3a: Add tokens**

In `internal/token/token.go`, the keyword block ends at `EFFECT`. Add the two new keywords immediately after `EFFECT` (still inside the contiguous keyword block, before the `// Punctuation.` comment), and update the block's doc comment:

```go
	// Keywords. NOTE: this block must stay contiguous (LET..EXPECT) —
	// internal/analysis/semantic.go classifies keywords by range.
	LET
	REC
	PUB
	IMPORT
	AS
	TYPE
	FUN
	IF
	THEN
	ELSE
	MATCH
	WITH
	OF
	EFFECT
	TEST
	EXPECT
```

Add to the `keywords` map:

```go
	"effect": EFFECT,
	"test":   TEST,
	"expect": EXPECT,
```

Find the kind-name table (the `String()` method or `kindNames` map that maps `EFFECT` to `"effect"`) and add `TEST -> "test"` and `EXPECT -> "expect"` entries mirroring the existing `EFFECT` entry.

- [ ] **Step 3b: Fix the semantic-tokens keyword range**

In `internal/analysis/semantic.go` (~line 250), change the upper bound from `token.EFFECT` to `token.EXPECT`:

```go
	if t.Kind >= token.LET && t.Kind <= token.EXPECT {
		return legendKeyword, true
	}
```

- [ ] **Step 3c: Add AST nodes**

In `internal/ast/ast.go`, in the declaration section add the `TestDecl` struct and its marker, and add a new `TestStmt` section:

```go
// TestDecl is a top-level `test "name" { stmts }` declaration. Its body is an
// effect context; statements are TestLet / TestExpect / TestRun. Dropped from
// non-test builds.
type TestDecl struct {
	Pos     Pos
	NamePos Pos
	Name    string
	Body    []TestStmt
}

// TestStmt is one statement inside a test block.
type TestStmt interface{ testStmtNode() }

// TestLet binds a name for subsequent statements: `let n = expr`.
type TestLet struct {
	Pos   Pos
	Name  string
	Value Expr
}

// TestExpect asserts a Match value: `expect expr`.
type TestExpect struct {
	Pos Pos
	X   Expr
}

// TestRun is a bare effectful statement inside a test block.
type TestRun struct{ X Expr }

func (*TestLet) testStmtNode()    {}
func (*TestExpect) testStmtNode() {}
func (*TestRun) testStmtNode()    {}
```

Add the `Decl` marker alongside the existing `func (*LetDecl) declNode() {}` lines:

```go
func (*TestDecl) declNode() {}
```

- [ ] **Step 3d: Add parser support**

In `internal/parse/parse.go`, add a case to `parseDecl`'s switch (after the `token.TYPE` case):

```go
	case token.TEST:
		return p.parseTestDecl()
```

Then add the two functions (place them near `parseEffect`):

```go
// parseTestDecl parses `test "name" { stmt; stmt; ... }`. Like effect { },
// layout is suspended inside the braces, so statements are ';'-separated with an
// optional trailing ';'.
func (p *parser) parseTestDecl() (ast.Decl, error) {
	start := p.advance() // test
	nameTok, err := p.expect(token.STRING)
	if err != nil {
		return nil, err
	}
	td := &ast.TestDecl{Pos: pos(start), NamePos: pos(nameTok), Name: nameTok.Lit}
	if _, err := p.expect(token.LBRACE); err != nil {
		return nil, err
	}
	for !p.at(token.RBRACE) {
		s, err := p.parseTestStmt()
		if err != nil {
			return nil, err
		}
		td.Body = append(td.Body, s)
		if !p.accept(token.SEMI) {
			break
		}
	}
	if _, err := p.expect(token.RBRACE); err != nil {
		return nil, err
	}
	return td, nil
}

func (p *parser) parseTestStmt() (ast.TestStmt, error) {
	switch {
	case p.at(token.EXPECT):
		start := p.advance() // expect
		x, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.TestExpect{Pos: pos(start), X: x}, nil
	case p.at(token.LET):
		start := p.advance() // let
		nameTok, err := p.expect(token.IDENT)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.EQ); err != nil {
			return nil, err
		}
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.TestLet{Pos: pos(start), Name: nameTok.Lit, Value: v}, nil
	default:
		x, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.TestRun{X: x}, nil
	}
}
```

Note: `nameTok.Lit` for a `STRING` token must be the **decoded** string (no surrounding quotes) — confirm against how the `STRING` case in `parsePrimary` builds `ast.StrLit.Value`. If the lexer stores quotes in `.Lit`, decode with `strconv.Unquote` here. The Step-1 test asserts `td.Name == "reverse swaps ends"` and will catch it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/parse/ -run TestParseTestDecl && go build ./...`
Expected: PASS, build green.

- [ ] **Step 5: Guard against keyword collisions, then commit**

Run: `grep -rnE '\b(test|expect)\b' std/ examples/ | grep -v '//'`
Expected: no identifier uses of `test`/`expect` as variable/function names (matches inside comments/strings are fine). If any real binding uses these names, rename it in the same commit.

```bash
go test ./internal/parse/ ./internal/analysis/ && go build ./...
git add internal/token/token.go internal/analysis/semantic.go internal/ast/ast.go internal/parse/
git commit -m "feat(parse): test/expect syntax — tokens, AST, parser"
```

---

### Task 2: Type-check `test` declarations

**Files:**
- Create: `internal/types/test_check.go`
- Modify: `internal/types/infer.go` (`checkIntoRec` second pass — dispatch `*ast.TestDecl`)
- Modify: `internal/types/effects.go` (`checkEffects` — walk `*ast.TestDecl` bodies)
- Test: `internal/types/test_check_test.go`

**Interfaces:**
- Consumes: `ast.TestDecl`, `ast.TestLet/TestExpect/TestRun` (Task 1); `newEnv`, `monoScheme`, `c.infer`, `c.unify`, `c.generalize`, `c.enterLevel/exitLevel`, `tBool`, `tString`, `TRecord` (existing in `internal/types`).
- Produces: `(*Checker).checkTest(td *ast.TestDecl, parent *env) error`; `walkTestStmt(s ast.TestStmt) error`.

- [ ] **Step 1: Write the failing test**

Create `internal/types/test_check_test.go`:

```go
package types

import (
	"strings"
	"testing"

	"github.com/incantery/sigil/internal/parse"
)

func checkSrc(t *testing.T, src string) error {
	t.Helper()
	m, err := parse.Module(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Check(m)
	return err
}

func TestTestDeclAcceptsMatchRecord(t *testing.T) {
	src := `test "ok" {
  let c = __cell 0;
  __set c 1;
  expect { pass = __get c == 1, label = "eq", got = "1", expected = "1" }
}`
	if err := checkSrc(t, src); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestTestDeclRejectsNonMatch(t *testing.T) {
	src := `test "bad" {
  expect 5
}`
	err := checkSrc(t, src)
	if err == nil {
		t.Fatal("expected a type error for non-Match expect argument")
	}
	if !strings.Contains(err.Error(), "Int") && !strings.Contains(err.Error(), "record") {
		t.Errorf("error %q should mention the mismatch", err.Error())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/types/ -run TestTestDecl`
Expected: FAIL — `TestTestDeclAcceptsMatchRecord` may pass vacuously (TestDecl currently ignored), but `TestTestDeclRejectsNonMatch` FAILS because `expect 5` is not checked yet (no error returned).

- [ ] **Step 3a: Implement `checkTest`**

Create `internal/types/test_check.go`:

```go
package types

import "github.com/incantery/sigil/internal/ast"

// matchType is the structural record every `expect` argument must produce.
func matchType() *TRecord {
	return &TRecord{Fields: map[string]Type{
		"pass":     tBool,
		"label":    tString,
		"got":      tString,
		"expected": tString,
	}}
}

// checkTest type-checks a `test "name" { ... }` declaration. Its body is an
// effect context: each statement is inferred in a child scope, `let` bindings
// extend the scope for subsequent statements, and every `expect` argument must
// unify with the Match record shape.
func (c *Checker) checkTest(td *ast.TestDecl, parent *env) error {
	scope := newEnv(parent)
	for _, s := range td.Body {
		switch s := s.(type) {
		case *ast.TestLet:
			c.enterLevel()
			t, err := c.infer(s.Value, scope)
			if err != nil {
				return err
			}
			c.exitLevel()
			scope.set(s.Name, c.generalize(t))
		case *ast.TestExpect:
			t, err := c.infer(s.X, scope)
			if err != nil {
				return err
			}
			if err := c.unify(t, matchType(), s.Pos); err != nil {
				return err
			}
		case *ast.TestRun:
			if _, err := c.infer(s.X, scope); err != nil {
				return err
			}
		}
	}
	return nil
}
```

- [ ] **Step 3b: Dispatch TestDecl in the inference pass**

In `internal/types/infer.go`, in `checkIntoRec`'s **second pass** (the loop that does `if ld, ok := d.(*ast.LetDecl)`), add TestDecl handling so it becomes:

```go
	// Second pass: infer value bindings in order.
	for _, d := range m.Decls {
		if ld, ok := d.(*ast.LetDecl); ok {
			if err := c.inferDecl(ld, root); err != nil {
				return nil, nil, err
			}
		}
		if td, ok := d.(*ast.TestDecl); ok {
			if err := c.checkTest(td, root); err != nil {
				return nil, nil, err
			}
		}
	}
```

- [ ] **Step 3c: Walk TestDecl bodies for the effect-context check**

In `internal/types/effects.go`, replace the body of `checkEffects` to also handle `*ast.TestDecl`, and add `walkTestStmt`:

```go
func checkEffects(m *ast.Module) error {
	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			if err := walkEffect(d.Body, 0); err != nil {
				return err
			}
			if err := walkParamDefaults(d.Params); err != nil {
				return err
			}
		case *ast.TestDecl:
			for _, s := range d.Body {
				if err := walkTestStmt(s); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// walkTestStmt checks a test statement at effect-depth 1: a test body is an
// effect context, so effect ops (__set, __effect, ...) are legal inside it.
func walkTestStmt(s ast.TestStmt) error {
	switch s := s.(type) {
	case *ast.TestLet:
		return walkEffect(s.Value, 1)
	case *ast.TestExpect:
		return walkEffect(s.X, 1)
	case *ast.TestRun:
		return walkEffect(s.X, 1)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/types/ && go build ./...`
Expected: PASS (both new tests), build green.

- [ ] **Step 5: Commit**

```bash
git add internal/types/
git commit -m "feat(types): type-check test/expect (Match record + effect context)"
```

---

### Task 3: Emit lowering + test prelude

**Files:**
- Modify: `internal/emit/emit.go` (`emitter` struct, `bundle` signature, `testRuntime`/`testPrelude`, `BundleTest`, `decl` dispatch, `testDecl`/`testStmt`)
- Test: `internal/emit/test_emit_test.go`

**Interfaces:**
- Consumes: `ast.TestDecl`, `ast.TestLet/TestExpect/TestRun` (Task 1); `mangle`, `e.expr`, `prelude`, `bundle`, `LinkedModule`, `peval.NewEnv` (existing).
- Produces: `emit.BundleTest(mods []LinkedModule, env *peval.Env) (string, error)`; a `testPrelude` defining JS globals `__tests`, `__test(name, thunk)`, `__expect(match)`, `__runTests()`.

- [ ] **Step 1: Write the failing test**

Create `internal/emit/test_emit_test.go`:

```go
package emit

import (
	"encoding/json"
	"testing"

	"github.com/dop251/goja"
	"github.com/incantery/sigil/internal/parse"
	"github.com/incantery/sigil/internal/peval"
)

type expectJSON struct {
	Pass     bool   `json:"pass"`
	Label    string `json:"label"`
	Got      string `json:"got"`
	Expected string `json:"expected"`
}
type testJSON struct {
	Name    string       `json:"name"`
	Expects []expectJSON `json:"expects"`
	Error   string       `json:"error"`
}

func TestBundleTestRunsExpects(t *testing.T) {
	src := `test "demo" {
  expect { pass = true, label = "eqx", got = "1", expected = "1" };
  expect { pass = false, label = "eqx", got = "2", expected = "1" }
}`
	m, err := parse.Module(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	js, err := BundleTest([]LinkedModule{{ID: "entry", AST: m}}, peval.NewEnv())
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	vm := goja.New()
	v, err := vm.RunString(js + "\n;JSON.stringify(__runTests())")
	if err != nil {
		t.Fatalf("run: %v\n--- emitted ---\n%s", err, js)
	}
	var got []testJSON
	if err := json.Unmarshal([]byte(v.Export().(string)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Name != "demo" {
		t.Fatalf("got %+v, want one test named demo", got)
	}
	if len(got[0].Expects) != 2 || !got[0].Expects[0].Pass || got[0].Expects[1].Pass {
		t.Errorf("expects = %+v, want [pass, fail]", got[0].Expects)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/emit/ -run TestBundleTestRunsExpects`
Expected: FAIL — `BundleTest` undefined.

- [ ] **Step 3a: Add the test field + thread it through `bundle`**

In `internal/emit/emit.go`, add a `test` field to the emitter struct:

```go
type emitter struct {
	tmp   int
	sheet *cssSheet
	penv  *peval.Env
	test  bool // emit test declarations (testDecl) instead of dropping them
}
```

Change the unexported `bundle` signature to take a `test` flag and set it on each module emitter. The function header and emitter construction become:

```go
func bundle(mods []LinkedModule, env *peval.Env, pre string, test bool) (string, error) {
	// ... unchanged setup ...
		e := &emitter{sheet: sheet, penv: env, test: test}
	// ... unchanged ...
}
```

Update the two existing callers and add a third:

```go
func Bundle(mods []LinkedModule, env *peval.Env) (string, error) {
	return bundle(mods, env, prelude, false)
}

// BundleDev is Bundle with the HMR-instrumented dev prelude.
func BundleDev(mods []LinkedModule, env *peval.Env) (string, error) {
	return bundle(mods, env, devPrelude, false)
}

// BundleTest is Bundle with the test prelude; test declarations are emitted as
// __test registrations and run by __runTests().
func BundleTest(mods []LinkedModule, env *peval.Env) (string, error) {
	return bundle(mods, env, testPrelude, true)
}
```

- [ ] **Step 3b: Define the test prelude**

In `internal/emit/emit.go`, after the `devPrelude` definitions, add:

```go
// testRuntime defines the globals that `test`/`expect` lower into. It is
// appended to the production prelude to form testPrelude. These are test-only
// JS helpers — not kernel intrinsics.
const testRuntime = `
const __tests = [];
let __cur = null;
const __test = (name, thunk) => { __tests.push({ name: name, thunk: thunk }); };
const __expect = (m) => { __cur.push({ pass: m.pass, label: m.label, got: m.got, expected: m.expected }); };
const __runTests = () => __tests.map((t) => {
  __cur = [];
  let error = null;
  try { t.thunk(); } catch (e) { error = String(e); }
  return { name: t.name, expects: __cur, error: error };
});
`

var testPrelude = prelude + testRuntime
```

- [ ] **Step 3c: Emit TestDecl / TestStmt**

In `internal/emit/emit.go`, add a case to the `decl` switch (before `default`):

```go
	case *ast.TestDecl:
		return e.testDecl(d)
```

Add the two helpers (near `typeDecl`):

```go
// testDecl emits a __test registration, or "" in a non-test build (so test
// declarations are dropped from build/serve/dev bundles).
func (e *emitter) testDecl(d *ast.TestDecl) (string, error) {
	if !e.test {
		return "", nil
	}
	var b strings.Builder
	for _, s := range d.Body {
		js, err := e.testStmt(s)
		if err != nil {
			return "", err
		}
		b.WriteString(js)
		b.WriteByte(' ')
	}
	return fmt.Sprintf("__test(%s, () => { %s});", strconv.Quote(d.Name), b.String()), nil
}

func (e *emitter) testStmt(s ast.TestStmt) (string, error) {
	switch s := s.(type) {
	case *ast.TestLet:
		v, err := e.expr(s.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("const %s = %s;", mangle(s.Name), v), nil
	case *ast.TestExpect:
		x, err := e.expr(s.X)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("__expect(%s);", x), nil
	case *ast.TestRun:
		x, err := e.expr(s.X)
		if err != nil {
			return "", err
		}
		return x + ";", nil
	default:
		return "", fmt.Errorf("cannot emit test statement %T", s)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/emit/ && go build ./...`
Expected: PASS, build green. (The whole emit suite must stay green — `Bundle`/`BundleDev` now pass the extra `false` arg.)

- [ ] **Step 5: Commit**

```bash
git add internal/emit/
git commit -m "feat(emit): test prelude + test/expect lowering (BundleTest)"
```

---

### Task 4: `Program.BundleTest` in the loader

**Files:**
- Modify: `internal/load/load.go` (refactor `bundle(dev bool)` → `bundleWith`; add `BundleTest`)
- Test: `internal/load/load_test.go` (add a test using the existing `build` helper, extended to bundle-test)

**Interfaces:**
- Consumes: `emit.Bundle`, `emit.BundleDev`, `emit.BundleTest` (Task 3); existing `importBindings`, `exportNames`, `peval.NewEnv`.
- Produces: `(*Program).BundleTest() (string, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/load/load_test.go`:

```go
func TestBundleTestEmitsRunner(t *testing.T) {
	files := map[string]string{
		"main": "test \"trivial\" {\n  expect { pass = true, label = \"x\", got = \"1\", expected = \"1\" }\n}",
	}
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name+".sigil"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prog, err := Load(filepath.Join(dir, "main.sigil"), Options{Root: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	js, err := prog.BundleTest()
	if err != nil {
		t.Fatalf("bundle-test: %v", err)
	}
	if !strings.Contains(js, "__runTests") || !strings.Contains(js, "__test(") {
		t.Errorf("bundle missing test runner hooks:\n%s", js)
	}
}
```

Ensure `internal/load/load_test.go` imports `"strings"`, `"os"`, `"path/filepath"` (add any missing).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/load/ -run TestBundleTestEmitsRunner`
Expected: FAIL — `prog.BundleTest` undefined.

- [ ] **Step 3: Refactor `bundle` and add `BundleTest`**

In `internal/load/load.go`, replace the `Bundle`/`BundleDev`/`bundle(dev bool)` block with:

```go
// Bundle links the type-checked program into one JS program. A program-wide
// partial-evaluator environment (every module's top-level definitions) is built
// so the emitter can fold static styles across module boundaries.
func (p *Program) Bundle() (string, error)     { return p.bundleWith(emit.Bundle) }
func (p *Program) BundleDev() (string, error)  { return p.bundleWith(emit.BundleDev) }
func (p *Program) BundleTest() (string, error) { return p.bundleWith(emit.BundleTest) }

func (p *Program) bundleWith(emitFn func([]emit.LinkedModule, *peval.Env) (string, error)) (string, error) {
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
	return emitFn(linked, env)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/load/ && go build ./...`
Expected: PASS, build green.

- [ ] **Step 5: Commit**

```bash
git add internal/load/
git commit -m "feat(load): Program.BundleTest via bundleWith refactor"
```

---

### Task 5: `std/test.sigil` matcher library + end-to-end through load+goja

**Files:**
- Create: `std/test.sigil`
- Test: `internal/load/std_test.go` (add an end-to-end test that imports `std/test`, runs in goja, and checks reported results)

**Interfaces:**
- Consumes: existing language only (no test/expect syntax — must parse under the current tree-sitter grammar). `import "std/test" (eq, neq, isTrue, isFalse, gt, lt)`.
- Produces: `std/test.sigil` exporting matchers, each returning `{ pass: Bool, label: String, got: String, expected: String }`.

- [ ] **Step 1: Write the failing test**

Add to `internal/load/std_test.go` (uses the existing `repoRoot` constant in that file):

```go
func TestStdTestMatchersRun(t *testing.T) {
	entry := `import "std/test" (eq, gt, isTrue, isFalse)
test "matchers" {
  expect (eq (1 + 2) 3);
  expect (gt 5 3);
  expect (isTrue true);
  expect (isFalse false);
  expect (eq [1, 2] [1, 2])
}
test "fails" {
  expect (eq 1 2)
}`
	dir := t.TempDir()
	file := filepath.Join(dir, "matchers_test.sigil")
	if err := os.WriteFile(file, []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := Load(file, Options{Root: repoRoot})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	js, err := prog.BundleTest()
	if err != nil {
		t.Fatalf("bundle-test: %v", err)
	}
	vm := goja.New()
	v, err := vm.RunString(js + "\n;JSON.stringify(__runTests())")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := v.Export().(string)
	// "matchers" test: all five expects pass. "fails" test: the single expect
	// reports expected 2, got 1.
	if !strings.Contains(out, `"name":"matchers"`) {
		t.Errorf("missing matchers test in %s", out)
	}
	if !strings.Contains(out, `"got":"1"`) || !strings.Contains(out, `"expected":"2"`) {
		t.Errorf("failing eq should report got 1 / expected 2: %s", out)
	}
}
```

Ensure `internal/load/std_test.go` imports `"github.com/dop251/goja"`, `"strings"`, `"os"`, `"path/filepath"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/load/ -run TestStdTestMatchersRun`
Expected: FAIL — `cannot resolve import "std/test"` (file does not exist yet).

- [ ] **Step 3: Create `std/test.sigil`**

```sigil
# std/test — assertion matchers for sigil's test runner.
#
# Each matcher returns a Match record { pass, label, got, expected }. The test
# runner's `expect` consumes a Match and records pass/fail with a message built
# from `got` / `expected`. `${x}` interpolation stringifies any value, and `==`
# is structural, so `eq` works for any type.

type Match = { pass: Bool, label: String, got: String, expected: String }

pub let eq actual expected =
  { pass = actual == expected, label = "eq", got = "${actual}", expected = "${expected}" }

pub let neq actual expected =
  { pass = actual != expected, label = "neq", got = "${actual}", expected = "not ${expected}" }

pub let isTrue b =
  { pass = b, label = "isTrue", got = "${b}", expected = "true" }

pub let isFalse b =
  { pass = b == false, label = "isFalse", got = "${b}", expected = "false" }

pub let gt actual bound =
  { pass = actual > bound, label = "gt", got = "${actual}", expected = "> ${bound}" }

pub let lt actual bound =
  { pass = actual < bound, label = "lt", got = "${actual}", expected = "< ${bound}" }
```

Note: do **not** annotate the matcher return types — leaving them inferred keeps the type structural so it unifies with `expect`'s `TRecord`. The `type Match` declaration is documentation/export only.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/load/ -run TestStdTestMatchersRun && go build ./...`
Expected: PASS. If `gt`/`lt` fail to type-check because `>`/`<` are constrained to a specific numeric type, that is acceptable — the dogfood uses `gt`/`lt` only on `Int`. If the matcher file itself fails to type-check (e.g. `>` needs both args same type), adjust by keeping the matchers as-is; the test only applies them to `Int`.

- [ ] **Step 5: Verify tree-sitter is unaffected, then commit**

Run: `make tree-sitter-verify` (skips gracefully if tree-sitter is not installed; if it runs, it must stay green — `std/test.sigil` uses only existing syntax).

```bash
go test ./internal/load/ && go build ./...
git add std/test.sigil internal/load/std_test.go
git commit -m "feat(std): std/test matcher library (eq, neq, isTrue, isFalse, gt, lt)"
```

---

### Task 6: `internal/testrun` — discovery, run, report

**Files:**
- Create: `internal/testrun/testrun.go`
- Test: `internal/testrun/testrun_test.go`

**Interfaces:**
- Consumes: `load.Load`, `(*load.Program).BundleTest` (Task 4); `goja`.
- Produces:
  - `testrun.Run(w io.Writer, path, root string) (ok bool, err error)`.
  - `testrun.TestResult{ Name string; Expects []ExpectResult; Error string }` and `ExpectResult{ Pass bool; Label, Got, Expected string }` (exported for tests).

- [ ] **Step 1: Write the failing test**

Create `internal/testrun/testrun_test.go`:

```go
package testrun

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot holds std/ (three levels up from internal/testrun).
const repoRoot = "../.."

func writeTests(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRunReportsPassAndFail(t *testing.T) {
	dir := writeTests(t, map[string]string{
		"math_test.sigil": `import "std/test" (eq)
test "adds" { expect (eq (1 + 2) 3) }
test "wrong" { expect (eq (1 + 1) 3) }`,
	})
	var buf bytes.Buffer
	ok, err := Run(&buf, dir, repoRoot)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := buf.String()
	if ok {
		t.Errorf("expected ok=false (one test fails)\n%s", out)
	}
	if !strings.Contains(out, "adds") || !strings.Contains(out, "wrong") {
		t.Errorf("missing test names:\n%s", out)
	}
	if !strings.Contains(out, "expected 3, got 2") {
		t.Errorf("failing test should print expected/got:\n%s", out)
	}
}

func TestRunBrowserGuard(t *testing.T) {
	dir := writeTests(t, map[string]string{
		"dom_test.sigil": `import "std/test" (eq)
test "needs dom" {
  let n = __text (fun _ -> "hi");
  expect (eq 1 1)
}`,
	})
	var buf bytes.Buffer
	ok, err := Run(&buf, dir, repoRoot)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := buf.String()
	if ok {
		t.Errorf("browser test should not pass under goja:\n%s", out)
	}
	if !strings.Contains(out, "browser test") {
		t.Errorf("expected a browser-test hint:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/testrun/`
Expected: FAIL — package/`Run` undefined.

- [ ] **Step 3: Implement the runner**

Create `internal/testrun/testrun.go`:

```go
// Package testrun discovers *_test.sigil files, compiles each against the
// standard library, and runs it in goja (the non-browser tier). Tests that need
// a real DOM fail gracefully here; Slice B routes them to Chrome.
package testrun

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dop251/goja"
	"github.com/incantery/sigil/internal/load"
)

// ExpectResult is one `expect` outcome.
type ExpectResult struct {
	Pass     bool   `json:"pass"`
	Label    string `json:"label"`
	Got      string `json:"got"`
	Expected string `json:"expected"`
}

// TestResult is one `test "name" { ... }` outcome.
type TestResult struct {
	Name    string         `json:"name"`
	Expects []ExpectResult `json:"expects"`
	Error   string         `json:"error"`
}

// discover returns the *_test.sigil files under path. path may be a file.
func discover(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	var files []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, "_test.sigil") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// runFile compiles file as a test module and runs it in goja.
func runFile(file, root string) ([]TestResult, error) {
	prog, err := load.Load(file, load.Options{Root: root})
	if err != nil {
		return nil, err
	}
	js, err := prog.BundleTest()
	if err != nil {
		return nil, err
	}
	vm := goja.New()
	v, err := vm.RunString(js + "\n;JSON.stringify(__runTests())")
	if err != nil {
		return nil, err
	}
	var results []TestResult
	if err := json.Unmarshal([]byte(v.Export().(string)), &results); err != nil {
		return nil, err
	}
	return results, nil
}

// Run discovers and runs every *_test.sigil under path, writing a report to w.
// It returns true only if every test passed. Infrastructure failures (a path
// that cannot be walked) return an error; per-file compile/run failures are
// reported inline and make ok=false.
func Run(w io.Writer, path, root string) (bool, error) {
	files, err := discover(path)
	if err != nil {
		return false, err
	}
	total, passed, failed := 0, 0, 0
	allOK := true
	for _, file := range files {
		fmt.Fprintln(w, file)
		results, err := runFile(file, root)
		if err != nil {
			allOK = false
			fmt.Fprintf(w, "  ✗ failed to compile/run: %v\n", err)
			continue
		}
		for _, r := range results {
			total++
			if r.Error == "" && allExpectsPass(r.Expects) {
				passed++
				fmt.Fprintf(w, "  ✓ %s\n", r.Name)
				continue
			}
			failed++
			allOK = false
			fmt.Fprintf(w, "  ✗ %s\n", r.Name)
			if r.Error != "" {
				hint := ""
				if looksBrowser(r.Error) {
					hint = " (looks like a browser test — Slice B will route these to Chrome)"
				}
				fmt.Fprintf(w, "      error: %s%s\n", r.Error, hint)
			}
			for _, ex := range r.Expects {
				if !ex.Pass {
					fmt.Fprintf(w, "      %s: expected %s, got %s\n", ex.Label, ex.Expected, ex.Got)
				}
			}
		}
	}
	fmt.Fprintf(w, "\n%d files, %d tests, %d passed, %d failed\n", len(files), total, passed, failed)
	return allOK, nil
}

func allExpectsPass(es []ExpectResult) bool {
	for _, e := range es {
		if !e.Pass {
			return false
		}
	}
	return true
}

// looksBrowser detects a goja failure caused by touching the DOM/host globals,
// so the report can point at Slice B.
func looksBrowser(msg string) bool {
	return strings.Contains(msg, "document") ||
		strings.Contains(msg, "window") ||
		strings.Contains(msg, "is not defined")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/testrun/ && go build ./...`
Expected: PASS. Note: `TestRunBrowserGuard` relies on `looksBrowser` matching goja's "document is not defined" message — if goja phrases it differently, widen `looksBrowser` accordingly (the `is not defined` substring is the safety net).

- [ ] **Step 5: Commit**

```bash
git add internal/testrun/
git commit -m "feat(testrun): discover + run *_test.sigil in goja, report results"
```

---

### Task 7: `sigil test` CLI subcommand

**Files:**
- Create: `internal/cli/test.go`
- Modify: `internal/cli/root.go` (register `newTestCmd`)
- Test: `internal/cli/test_test.go`

**Interfaces:**
- Consumes: `testrun.Run` (Task 6); `ErrSilent` (existing in `internal/cli`).
- Produces: `newTestCmd() *cobra.Command` wired into the root command.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/test_test.go`:

```go
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTestCmdRunsAndFailsOnFailure(t *testing.T) {
	dir := t.TempDir()
	src := `import "std/test" (eq)
test "wrong" { expect (eq 1 2) }`
	if err := os.WriteFile(filepath.Join(dir, "x_test.sigil"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	// repoRoot for std/ resolution is two levels up from internal/cli.
	root.SetArgs([]string{"test", dir, "--root", "../.."})
	err := root.Execute()
	if err != ErrSilent {
		t.Fatalf("expected ErrSilent (nonzero exit) on failing tests, got %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("wrong")) {
		t.Errorf("output should name the failing test:\n%s", out.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestTestCmd`
Expected: FAIL — `unknown command "test"`.

- [ ] **Step 3a: Create the command**

Create `internal/cli/test.go`:

```go
package cli

import (
	"github.com/spf13/cobra"

	"github.com/incantery/sigil/internal/testrun"
)

func newTestCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "test [PATH]",
		Short: "Compile and run *_test.sigil files in goja",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			ok, err := testrun.Run(cmd.OutOrStdout(), path, root)
			if err != nil {
				return err
			}
			if !ok {
				return ErrSilent // already reported; nonzero exit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "module root directory (where std/ lives)")
	return cmd
}
```

- [ ] **Step 3b: Register it**

In `internal/cli/root.go`, add to `newRootCmd` alongside the other `AddCommand` calls:

```go
	root.AddCommand(newTestCmd())
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ && go build ./...`
Expected: PASS, build green.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/test.go internal/cli/root.go internal/cli/test_test.go
git commit -m "feat(cli): sigil test subcommand"
```

---

### Task 8: Dogfood `tests/`, CI wiring, docs

**Files:**
- Create: `tests/sanity_test.sigil`
- Create: `tests/reactive_test.sigil`
- Create: `internal/testrun/dogfood_test.go`
- Create: `docs/testing.md`
- Modify: `Makefile` (add a `test-sigil` target)
- Modify: `CLAUDE.md` (document the new capability)

**Interfaces:**
- Consumes: everything above (`sigil test`, `std/test`, the runner).

- [ ] **Step 1: Write the failing dogfood test**

Create `internal/testrun/dogfood_test.go`:

```go
package testrun

import (
	"bytes"
	"testing"
)

// TestDogfood runs the real tests/ suite through the runner, wiring the sigil
// test suite into `go test ./...`.
func TestDogfood(t *testing.T) {
	var buf bytes.Buffer
	ok, err := Run(&buf, "../../tests", "../..")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, buf.String())
	}
	if !ok {
		t.Fatalf("dogfood tests failed:\n%s", buf.String())
	}
	t.Logf("\n%s", buf.String())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/testrun/ -run TestDogfood`
Expected: FAIL — `../../tests` does not exist (`discover` returns a stat error).

- [ ] **Step 3a: Create the dogfood test files**

`tests/sanity_test.sigil` (proves syntax + matchers + runner, no stdlib-shape dependency):

```sigil
import "std/test" (eq, gt, isTrue, isFalse)

test "int arithmetic" {
  expect (eq (1 + 2 * 3) 7)
}

test "structural list equality" {
  expect (eq [1, 2, 3] [1, 2, 3])
}

test "booleans" {
  expect (isTrue true);
  expect (isFalse false)
}

test "ordering" {
  expect (gt 5 3)
}
```

`tests/reactive_test.sigil` (proves the test body is an effect context — drives a signal then asserts):

```sigil
import "std/test" (eq)

test "cell set updates get" {
  let c = __cell 0;
  __set c 1;
  __set c 2;
  expect (eq (__get c) 2)
}
```

- [ ] **Step 3b: Run the suite via the CLI to eyeball output**

Run: `go run ./cmd/sigil test tests --root .`
Expected output (order may vary):

```
tests/reactive_test.sigil
  ✓ cell set updates get
tests/sanity_test.sigil
  ✓ int arithmetic
  ✓ structural list equality
  ✓ booleans
  ✓ ordering

2 files, 5 tests, 5 passed, 0 failed
```

Exit code 0.

- [ ] **Step 3c: Add the Makefile target**

In `Makefile`, add (use a real tab for the recipe indent):

```make
test-sigil: build
	./bin/sigil test tests --root .
```

- [ ] **Step 3d: Write `docs/testing.md`**

```markdown
# Testing sigil with sigil

`sigil test [PATH]` discovers `*_test.sigil` files (default `PATH` is `.`),
compiles each against the standard library, and runs it. Slice A runs the
**non-browser tier** in goja; tests that touch the DOM are reported as browser
tests (Slice B will route those to Chrome).

## Writing a test

Test files import the module under test (only `pub` exports are visible) and
`std/test`:

```sigil
import "std/list" (reverse)
import "std/test" (eq, gt)

test "reverse swaps ends" {
  expect (eq (reverse [1, 2, 3]) [3, 2, 1])
}
```

- `test "name" { ... }` — the body is an effect context (you may drive
  `__cell`/`__set`/`__effect` and then assert). Statements are separated by `;`
  with an optional trailing `;` (layout is suspended inside the braces, like
  `effect { }`).
- `expect <matcher>` — records a `Match`. Matchers come from `std/test`:
  `eq`, `neq`, `isTrue`, `isFalse`, `gt`, `lt`. A custom matcher is any function
  returning `{ pass: Bool, label: String, got: String, expected: String }`.
- `let n = expr;` — binds a value for the rest of the test.

Test files live under `tests/` (kept out of `std/`+`examples/` so the
tree-sitter drift guard, which does not yet know the `test`/`expect` keywords,
stays green). Run the suite with `make test-sigil` or `go run ./cmd/sigil test
tests --root .`. The Go suite also runs it via `internal/testrun` `TestDogfood`.

## How it works

`test`/`expect` lower to test-only JS hooks (`__test`/`__expect`) injected by a
dedicated test prelude — the same swap pattern the dev server uses — so the
kernel intrinsics are untouched and `build`/`serve`/`dev` never ship test code.
```

- [ ] **Step 3e: Update CLAUDE.md**

In `CLAUDE.md`, under "Build / test / run", add a line for `sigil test`:

```
go run ./cmd/sigil test tests --root .                   # run *_test.sigil in goja
```

And under "What's next", note Slice A is done and Slice B (browser tier) is next:

```
5. Test framework — Slice A (goja tier) DONE: `test`/`expect` syntax, `std/test`
   matchers, test-only prelude, `sigil test` runner. Tests live in `tests/`
   (`*_test.sigil`); see docs/testing.md. Slice B next: static DOM-reachability
   classifier + chromedp driver to route DOM tests to Chrome.
```

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go build ./... && go run ./cmd/sigil test tests --root .`
Expected: all Go tests pass; `sigil test` prints all-pass and exits 0.

- [ ] **Step 5: Commit**

```bash
git add tests/ internal/testrun/dogfood_test.go docs/testing.md Makefile CLAUDE.md
git commit -m "feat(test): dogfood tests/ suite, make test-sigil, docs (Slice A complete)"
```

---

## Self-Review

**Spec coverage:**
- `test`/`expect` syntax → Task 1 (tokens/AST/parser), Task 2 (type-check).
- Matcher functions returning `Match` → Task 5 (`std/test`).
- `Match` shape + `expect` typing → Task 2 (`matchType` unify); structural-record decision honored (no nominal registration).
- Test-only runtime hooks, kernel untouched → Task 3 (`testRuntime`/`testPrelude`).
- Build never ships tests → Task 3 (`testDecl` returns "" when `!e.test`).
- Separate `*_test.sigil` files, discovery → Task 6 (`discover`), Task 8 (`tests/`).
- goja runner, JSON result bridge → Task 3 (`__runTests`), Task 6 (`runFile`).
- Human report, exit codes → Task 6 (`Run`), Task 7 (`ErrSilent`).
- Graceful browser-test failure → Task 6 (`looksBrowser`), Task 8 (`reactive`/guard tests) + Task 6 `TestRunBrowserGuard`.
- Effect-context test bodies (reactivity testable) → Task 2 (`walkTestStmt` depth 1), Task 8 (`reactive_test.sigil`).
- Testing the tester + dogfood → Task 6 tests, Task 8 `TestDogfood`.
- Out-of-scope items (classifier, chromedp, `--json`, internal/non-pub testing) → not implemented; Slice B noted in docs/CLAUDE.md.

**Placeholder scan:** none — every code step shows full code.

**Type consistency:** `TestDecl`/`TestLet`/`TestExpect`/`TestRun` names are used identically across Tasks 1–3. `BundleTest` signature `([]emit.LinkedModule, *peval.Env) (string, error)` matches its use in Task 4 `bundleWith`. `testrun.Run(io.Writer, string, string) (bool, error)` matches Tasks 6/7/8. `ExpectResult`/`TestResult` JSON tags match the `__runTests` object shape from Task 3.

**Risks flagged inline (not blocking):**
- `STRING` token `.Lit` decoding (Task 1 Step 3d) — caught by the parser test.
- `>`/`<` polymorphism for `gt`/`lt` (Task 5 Step 4) — dogfood uses them on `Int` only.
- goja's exact "document is not defined" wording (Task 6 Step 4) — `looksBrowser` has an `is not defined` safety net.
