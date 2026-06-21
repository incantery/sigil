# Tree-sitter Grammar + Editor Highlighting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Syntax highlighting + folding for `.sigil` files in Neovim (tree-sitter) and VS Code (TextMate), drift-guarded against the real language.

**Architecture:** A tree-sitter grammar (`editor/tree-sitter-sigil`) with an external C scanner for the offside-rule layout tokens, plus nvim queries; a separate TextMate grammar for VS Code; Makefile targets and a Go keyword cross-check as drift guards. TDD via `tree-sitter test` corpus cases.

**Tech Stack:** tree-sitter (via `npx --yes tree-sitter-cli@0.25`), C (external scanner), Go (keyword-coverage test), node/npm (VS Code packaging), Make.

## Global Constraints

- **tree-sitter CLI:** invoke as `npx --yes tree-sitter-cli@0.25` (no global install assumed). Run from `editor/tree-sitter-sigil/`.
- **Grammar name:** `sigil` (tree-sitter `name: 'sigil'`).
- **Language reference (authoritative):** `docs/grammar.md` (EBNF) and `internal/parse/parse.go`. The grammar is *structural* — laxer than the real parser; it must not error on valid programs.
- **The 13 keywords (verbatim, from `internal/token/token.go`):** `let rec pub import as type fun if then else match with of`.
- **Layout:** the external scanner emits `INDENT`/`DEDENT`/`NEWLINE`; layout is suppressed inside `( ) [ ] { }`; blank lines and `//`-comment-only lines are skipped before measuring indent (mirrors `internal/lex/lex.go`).
- **Lexical classes (from `docs/grammar.md`):** `IDENT = (lower|_)(alnum|_|')*`; `UIDENT = upper (alnum|_)*`; `HOLE = __IDENT`; `INT = digit+`; `FLOAT = digit+ '.' digit+`; `STRING = "…"` with `${expr}` interpolation; `//` line comments; wildcard `_`.
- **Committed generated files:** `src/parser.c`, `src/grammar.json`, `src/node-types.json`. Gitignore `*.so`, `node_modules/`, `build/`.
- **Operator precedence (loosest→tightest, all binary left-assoc unless noted), from `docs/grammar.md`:** `|>` ; `||` ; `&&` ; `== != < > <= >=` (non-assoc) ; `++` (right-assoc) ; `+ -` ; `* /`. Unary `-` and application bind tighter than all binops; application is left-assoc.
- **Reference scaffolding in git history (pre-deletion commit `222a15d^`):** `git show 222a15d^:editor/tree-sitter-sigil/src/scanner.c`, `…/grammar.js`, `…/queries/*.scm`, `…/vscode-sigil/*`. These targeted the OLD language — reuse boilerplate/structure only, not rules.
- **`go test ./...` must stay green** (the new keyword test is the only Go addition).

---

### Task 1: Scaffold the grammar package + external scanner (layout)

The foundational, highest-risk piece: the offside-rule scanner and a minimal
grammar that exercises it. Everything else builds on a working INDENT/DEDENT.

**Files:**
- Create: `editor/tree-sitter-sigil/package.json`
- Create: `editor/tree-sitter-sigil/tree-sitter.json`
- Create: `editor/tree-sitter-sigil/.gitignore`
- Create: `editor/tree-sitter-sigil/grammar.js` (minimal)
- Create: `editor/tree-sitter-sigil/src/scanner.c`
- Create: `editor/tree-sitter-sigil/test/corpus/layout.txt`
- Generated (committed by the generate step): `src/parser.c`, `src/grammar.json`, `src/node-types.json`

**Interfaces:**
- Produces: external tokens `_newline`, `_indent`, `_dedent`; grammar rules `source_file`, `_statement`, `block`, `let_decl` (minimal), `identifier`, `number`. Later tasks extend `grammar.js`'s rule set and reuse the scanner unchanged.

- [ ] **Step 1: Create package.json**

```json
{
  "name": "tree-sitter-sigil",
  "version": "0.1.0",
  "description": "Tree-sitter grammar for the Sigil language",
  "main": "bindings/node",
  "scripts": { "test": "tree-sitter test" },
  "tree-sitter": [ { "scope": "source.sigil", "file-types": ["sigil"] } ]
}
```

- [ ] **Step 2: Create tree-sitter.json**

```json
{
  "grammars": [
    { "name": "sigil", "scope": "source.sigil", "file-types": ["sigil"], "path": "." }
  ],
  "metadata": { "version": "0.1.0", "license": "MIT" }
}
```

- [ ] **Step 3: Create .gitignore**

```
node_modules/
build/
*.so
*.dylib
*.wasm
```

- [ ] **Step 4: Write the external scanner `src/scanner.c`**

Start from the git-history reference and simplify: keep ONLY the indentation
state machine (`NEWLINE`/`INDENT`/`DEDENT` with an indent stack and
bracket-depth suppression); REMOVE the old `code`-block verbatim token entirely.
Read the reference first:
```bash
git show 222a15d^:editor/tree-sitter-sigil/src/scanner.c > /tmp/old-scanner.c
```
The new scanner must:
- Declare exactly three external tokens in this order (matching grammar.js
  `externals`): `NEWLINE`, `INDENT`, `DEDENT`.
- Maintain an indent stack (vector of column widths), serialized in
  `tree_sitter_sigil_external_scanner_serialize/deserialize`.
- Track bracket depth: increment on `( [ {`, decrement on `) ] }`; while depth>0,
  emit no layout tokens (newlines are whitespace inside brackets).
- On a newline at bracket-depth 0: skip blank lines and lines whose first
  non-space content is `//`; measure the next line's indent; emit `INDENT` if
  deeper (push), `DEDENT` if shallower (pop, possibly multiple), else `NEWLINE`.
- At EOF, emit pending `DEDENT`s to unwind the stack.

(The reference's indent logic is directly reusable; the deletion is the
`code`-block token and its keyword-column tracking.)

- [ ] **Step 5: Write a minimal grammar.js that uses the scanner**

```js
module.exports = grammar({
  name: 'sigil',
  externals: $ => [$._newline, $._indent, $._dedent],
  extras: $ => [/[ \t\r]/, $.line_comment],
  word: $ => $.identifier,
  rules: {
    source_file: $ => repeat(seq($._statement, $._newline)),
    _statement: $ => choice($.let_decl, $._expression),
    let_decl: $ => seq('let', field('name', $.identifier), '=', $._expression),
    block: $ => seq($._indent, repeat(seq($._statement, $._newline)), $._dedent),
    _expression: $ => choice($.identifier, $.number, $.block),
    identifier: $ => /[a-z_][A-Za-z0-9_']*/,
    number: $ => /\d+/,
    line_comment: $ => token(seq('//', /.*/)),
  }
});
```

- [ ] **Step 6: Write the layout corpus test `test/corpus/layout.txt`**

```
==================
let with indented block
==================

let x =
  y

---

(source_file
  (let_decl
    name: (identifier)
    (block (identifier))))
```

- [ ] **Step 7: Generate and run the corpus test**

Run (from `editor/tree-sitter-sigil/`):
```bash
npx --yes tree-sitter-cli@0.25 generate && npx --yes tree-sitter-cli@0.25 test
```
Expected: generates `src/parser.c`, `src/grammar.json`, `src/node-types.json`; the
`layout` corpus test passes (1 passing).

- [ ] **Step 8: Commit**

```bash
git add editor/tree-sitter-sigil
git commit -m "feat(editor): tree-sitter scaffold + offside-rule external scanner

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Grammar — declarations + core expressions

Extends `grammar.js` to cover declarations and the non-operator expression forms,
each proven by a corpus test written first.

**Files:**
- Modify: `editor/tree-sitter-sigil/grammar.js`
- Create: `editor/tree-sitter-sigil/test/corpus/decls.txt`
- Create: `editor/tree-sitter-sigil/test/corpus/expressions.txt`

**Interfaces:**
- Consumes: the scanner externals + base rules from Task 1.
- Produces: rules `let_decl` (with `rec`/`pub`/params), `import_decl`, `type_decl`,
  `constructor`, `lambda`, `if`, `application`, `string`, `float`, `uident`, `hole`,
  `unit`, `list`, `paren`. Task 3 adds `binop`, `match`, `field`, interpolation.

- [ ] **Step 1: Write corpus tests for declarations `test/corpus/decls.txt`**

```
==================
pub rec let with params
==================

pub let rec map f xs =
  xs

---

(source_file
  (let_decl (pub) (rec)
    name: (identifier)
    (parameter (identifier))
    (parameter (identifier))
    (block (identifier))))

==================
import with alias
==================

import "std/ui" (card, button) as ui

---

(source_file
  (import_decl
    path: (string)
    (import_name (identifier))
    (import_name (identifier))
    alias: (identifier)))

==================
type with constructors
==================

type Color = Red | Green | Blue

---

(source_file
  (type_decl
    name: (uident)
    (constructor name: (uident))
    (constructor name: (uident))
    (constructor name: (uident))))
```

- [ ] **Step 2: Write corpus tests for core expressions `test/corpus/expressions.txt`**

```
==================
lambda and application
==================

let f =
  fun x -> g x y

---

(source_file
  (let_decl name: (identifier)
    (block
      (lambda (parameter (identifier))
        (application (application (identifier) (identifier)) (identifier))))))

==================
if then else
==================

let r = if a then b else c

---

(source_file
  (let_decl name: (identifier)
    (if condition: (identifier) consequence: (identifier) alternative: (identifier))))

==================
literals: string float uident hole unit list
==================

let v = [Some, __cell, 3.14, "hi", ()]

---

(source_file
  (let_decl name: (identifier)
    (list (uident) (hole) (float) (string) (unit))))
```

- [ ] **Step 3: Run the new corpus tests to confirm they FAIL**

Run: `npx --yes tree-sitter-cli@0.25 test`
Expected: the `decls`/`expressions` cases FAIL (rules `import_decl`, `type_decl`,
`lambda`, `if`, `application`, `string`, etc. not defined yet).

- [ ] **Step 4: Extend grammar.js with the declaration + core-expression rules**

Replace the `rules` object so it includes (keeping Task 1's scanner wiring,
`extras`, `word`):
```js
    source_file: $ => repeat(seq($._statement, $._newline)),
    _statement: $ => choice($.let_decl, $.import_decl, $.type_decl, $._expression),

    let_decl: $ => seq(
      optional($.pub), 'let', optional($.rec),
      field('name', $.identifier), repeat($.parameter), '=', $._expression),
    pub: $ => 'pub',
    rec: $ => 'rec',
    parameter: $ => $.identifier,

    import_decl: $ => seq(
      'import', field('path', $.string),
      optional(seq('(', commaSep1($.import_name), ')')),
      optional(seq('as', field('alias', $.identifier)))),
    import_name: $ => choice($.identifier, $.uident),

    type_decl: $ => seq(
      optional($.pub), 'type', field('name', $.uident), '=',
      $.constructor, repeat(seq('|', $.constructor))),
    constructor: $ => seq(field('name', $.uident), repeat($._atom)),

    _expression: $ => choice($.lambda, $.if, $.application, $._atom),
    lambda: $ => prec.right(seq('fun', repeat1($.parameter), '->', $._expression)),
    if: $ => prec.right(seq(
      'if', field('condition', $._expression),
      'then', field('consequence', $._expression),
      'else', field('alternative', $._expression))),
    application: $ => prec.left(seq($._atom, repeat1($._atom))),

    _atom: $ => choice(
      $.identifier, $.uident, $.hole, $.number, $.float, $.string,
      $.unit, $.list, $.paren, $.block),
    paren: $ => seq('(', $._expression, ')'),
    unit: $ => seq('(', ')'),
    list: $ => seq('[', optional(commaSep1($._expression)), ']'),

    identifier: $ => /[a-z_][A-Za-z0-9_']*/,
    uident: $ => /[A-Z][A-Za-z0-9_]*/,
    hole: $ => /__[A-Za-z_][A-Za-z0-9_']*/,
    number: $ => /\d+/,
    float: $ => /\d+\.\d+/,
    string: $ => /"[^"]*"/,
    line_comment: $ => token(seq('//', /.*/)),
```
And add at the top of the file (above `module.exports`):
```js
function commaSep1(rule) { return seq(rule, repeat(seq(',', rule))); }
```
Remove the old minimal `let_decl`/`_expression`/`block`-only rules from Task 1
(keep `block`, now referenced by `_atom`). Note `string` here is a single regex
placeholder; Task 3 replaces it with an interpolation-aware rule.

- [ ] **Step 5: Regenerate and run all corpus tests**

Run: `npx --yes tree-sitter-cli@0.25 generate && npx --yes tree-sitter-cli@0.25 test`
Expected: all `layout`, `decls`, `expressions` cases pass. Resolve any conflicts
the generator reports (e.g. `unit` vs `paren` — both start with `(`; the generator
handles this via GLR, but if it warns, keep both — `()` matches `unit`, `(expr)`
matches `paren`).

- [ ] **Step 6: Commit**

```bash
git add editor/tree-sitter-sigil
git commit -m "feat(editor): grammar for declarations + core expressions

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Grammar — operators, match, field access, string interpolation

The trickier expression forms: the precedence ladder, `match` arms, `.field`, and
`${…}` interpolation (which later feeds the injection query).

**Files:**
- Modify: `editor/tree-sitter-sigil/grammar.js`
- Create: `editor/tree-sitter-sigil/test/corpus/operators.txt`
- Create: `editor/tree-sitter-sigil/test/corpus/match.txt`
- Create: `editor/tree-sitter-sigil/test/corpus/strings.txt`

**Interfaces:**
- Consumes: rules from Task 2.
- Produces: rules `binop`, `unop`, `match`, `match_arm`, `pattern`, `field`,
  `string` (interpolation-aware) with `interpolation` nodes. These node names are
  what Task 4's queries reference.

- [ ] **Step 1: Write the operator corpus test `test/corpus/operators.txt`**

```
==================
precedence: + binds tighter than ==, |> loosest
==================

let r = a |> b == c + d * e

---

(source_file
  (let_decl name: (identifier)
    (binop
      left: (identifier)
      right: (binop
        left: (identifier)
        right: (binop
          left: (identifier)
          right: (binop left: (identifier) right: (identifier)))))))

==================
unary minus and concat right-assoc
==================

let r = -a ++ b ++ c

---

(source_file
  (let_decl name: (identifier)
    (binop
      left: (unop (identifier))
      right: (binop left: (identifier) right: (identifier)))))
```

- [ ] **Step 2: Write the match corpus test `test/corpus/match.txt`**

```
==================
match with arms and guard
==================

let r = match x with
  | Some y if y -> y
  | None -> z

---

(source_file
  (let_decl name: (identifier)
    (match scrutinee: (identifier)
      (match_arm pattern: (pattern (uident) (pattern (identifier))) guard: (identifier) body: (identifier))
      (match_arm pattern: (pattern (uident)) body: (identifier)))))
```

- [ ] **Step 3: Write the strings corpus test `test/corpus/strings.txt`**

```
==================
string interpolation and field access
==================

let r = "hi ${user.name}!"

---

(source_file
  (let_decl name: (identifier)
    (string
      (interpolation (field record: (identifier) field: (identifier))))))
```

- [ ] **Step 4: Run the new corpus tests to confirm they FAIL**

Run: `npx --yes tree-sitter-cli@0.25 test`
Expected: `operators`, `match`, `strings` cases FAIL (rules not yet defined).

- [ ] **Step 5: Extend grammar.js with operators, match, field, interpolation**

Add `$.binop`, `$.unop`, `$.match`, `$.field` to the `_expression` choice (binop
and match are expressions; field is postfix on an atom). Add these rules and a
precedence table:
```js
    // loosest → tightest; matches docs/grammar.md
    _expression: $ => choice($.lambda, $.if, $.match, $.binop, $.unop, $.application, $._postfix),

    binop: $ => choice(
      prec.left(1, seq($._expression, '|>', $._expression)),
      prec.left(2, seq($._expression, '||', $._expression)),
      prec.left(3, seq($._expression, '&&', $._expression)),
      prec.left(4, seq($._expression, choice('==','!=','<','>','<=','>='), $._expression)),
      prec.right(5, seq($._expression, '++', $._expression)),
      prec.left(6, seq($._expression, choice('+','-'), $._expression)),
      prec.left(7, seq($._expression, choice('*','/'), $._expression))),
    unop: $ => prec(8, seq('-', $._expression)),

    application: $ => prec.left(9, seq($._postfix, repeat1($._postfix))),
    _postfix: $ => choice($.field, $._atom),
    field: $ => prec.left(10, seq(field('record', $._atom), '.', field('field', $.identifier))),

    match: $ => prec.right(seq('match', field('scrutinee', $._expression), 'with', repeat1($.match_arm))),
    match_arm: $ => seq('|', field('pattern', $.pattern),
      optional(seq('if', field('guard', $._expression))), '->', field('body', $._expression)),
    pattern: $ => choice(
      seq($.uident, repeat($.pattern)), $.identifier, $.wildcard, $.number, $.string,
      seq('(', $.pattern, ')')),
    wildcard: $ => '_',
```
Replace the placeholder `string` rule with an interpolation-aware one:
```js
    string: $ => seq('"', repeat(choice($.interpolation, $._string_text)), '"'),
    _string_text: $ => token.immediate(prec(1, /[^"$\\]+|\$[^{]/)),
    interpolation: $ => seq('${', $._expression, '}'),
```
Remove the old single-regex `string` rule. Keep `application` from Task 2 but
update it to use `_postfix` as shown (so `f x.y` parses).

- [ ] **Step 6: Regenerate and run all corpus tests**

Run: `npx --yes tree-sitter-cli@0.25 generate && npx --yes tree-sitter-cli@0.25 test`
Expected: all corpus files pass. The generator may report precedence conflicts;
resolve by adjusting the numeric `prec` levels to the ladder in Global Constraints
(higher number = tighter). Do not silence conflicts with `conflicts:` unless a
genuine GLR ambiguity remains after precedence is correct.

- [ ] **Step 7: Commit**

```bash
git add editor/tree-sitter-sigil
git commit -m "feat(editor): grammar for operators, match, field access, interpolation

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Neovim queries + filetype detection

Highlighting, folding, injection, and locals queries for nvim, plus `ftdetect`.

**Files:**
- Create: `editor/tree-sitter-sigil/queries/highlights.scm`
- Create: `editor/tree-sitter-sigil/queries/folds.scm`
- Create: `editor/tree-sitter-sigil/queries/injections.scm`
- Create: `editor/tree-sitter-sigil/queries/locals.scm`
- Create: `editor/nvim/ftdetect/sigil.lua`

**Interfaces:**
- Consumes: node names from Tasks 1–3 (`let_decl`, `uident`, `constructor`,
  `hole`, `string`, `interpolation`, `line_comment`, `lambda`, `parameter`,
  keywords as anonymous nodes, operators as anonymous nodes).
- Produces: query files installed by Task 5's `nvim-install` target.

- [ ] **Step 1: Write highlights.scm**

```scheme
; keywords
["let" "rec" "pub" "import" "as" "type" "fun" "if" "then" "else" "match" "with" "of"] @keyword

; literals
(string) @string
(interpolation ["${" "}"] @punctuation.special)
(number) @number
(float) @number
(line_comment) @comment

; types vs values (capitalization)
(uident) @type
(constructor name: (uident) @constructor)
(hole) @function.builtin

; bindings and calls
(let_decl name: (identifier) @function)
(parameter (identifier) @variable.parameter)
(application (_) @function.call .)
(field field: (identifier) @property)
(identifier) @variable

; operators & punctuation
["|>" "||" "&&" "==" "!=" "<" ">" "<=" ">=" "++" "+" "-" "*" "/" "->" "="] @operator
["(" ")" "[" "]" "{" "}" "|" "," "."] @punctuation.delimiter
(wildcard) @variable.builtin
```
(Order matters in tree-sitter queries: specific captures before the catch-all
`(identifier) @variable`.)

- [ ] **Step 2: Write folds.scm**

```scheme
(block) @fold
(match) @fold
```

- [ ] **Step 3: Write injections.scm**

```scheme
; the body of ${...} is itself Sigil
(interpolation (_) @injection.content (#set! injection.language "sigil"))
```

- [ ] **Step 4: Write locals.scm**

```scheme
(let_decl) @local.scope
(lambda) @local.scope
(parameter (identifier) @local.definition.parameter)
(let_decl name: (identifier) @local.definition.var)
(identifier) @local.reference
```

- [ ] **Step 5: Write ftdetect/sigil.lua**

```lua
vim.filetype.add({ extension = { sigil = "sigil" } })
```

- [ ] **Step 6: Validate the queries against the grammar**

Run (from `editor/tree-sitter-sigil/`):
```bash
npx --yes tree-sitter-cli@0.25 generate
for q in queries/*.scm; do npx --yes tree-sitter-cli@0.25 query "$q" test/corpus/expressions.txt >/dev/null && echo "ok $q" || echo "FAIL $q"; done
```
Expected: every query prints `ok` — a query referencing a non-existent node or
capture name errors here. (If `tree-sitter query` requires a source file rather
than a corpus file, point it at `../../examples/counter/counter.sigil` instead.)

- [ ] **Step 7: Commit**

```bash
git add editor/tree-sitter-sigil/queries editor/nvim
git commit -m "feat(editor): nvim highlight/fold/injection/locals queries + ftdetect

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Drift guards — Makefile targets + Go keyword cross-check

Wire the grammar into the repo's build and add the two automated drift guards.

**Files:**
- Modify: `Makefile` (add targets)
- Create: `internal/token/grammar_sync_test.go`
- Modify: `internal/token/token.go` (add exported `Keywords()` accessor)
- Modify: `editor/tree-sitter-sigil/grammar.js` (add a machine-readable `KEYWORDS` const used by the keyword rule)

**Interfaces:**
- Consumes: the keyword set in `internal/token` and `grammar.js`.
- Produces: `token.Keywords() []string`; Makefile targets `tree-sitter`,
  `tree-sitter-test`, `tree-sitter-verify`, `nvim-install`, `vscode-ext`.

- [ ] **Step 1: Add an exported keyword accessor to token.go**

In `internal/token/token.go`, after the `keywords` map, add:
```go
// Keywords returns the language's reserved words, sorted. Editor tooling
// (the tree-sitter grammar) cross-checks against this set.
func Keywords() []string {
	ks := make([]string, 0, len(keywords))
	for k := range keywords {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
```
Add `"sort"` to the file's imports.

- [ ] **Step 2: Make grammar.js expose a machine-readable keyword list**

At the top of `editor/tree-sitter-sigil/grammar.js`, add:
```js
// KEYWORDS — kept in sync with internal/token (TestGrammarKeywordsMatch).
const KEYWORDS = ["let","rec","pub","import","as","type","fun","if","then","else","match","with","of"];
```
This is documentation + the cross-check target. (The grammar rules already list
these as string literals; the const is the stable, parseable manifest.)

- [ ] **Step 3: Write the failing keyword cross-check test**

Create `internal/token/grammar_sync_test.go`:
```go
package token

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestGrammarKeywordsMatch guards the tree-sitter grammar's KEYWORDS list
// against the language's actual keyword set. Adding a keyword to token.go
// without updating grammar.js fails here.
func TestGrammarKeywordsMatch(t *testing.T) {
	src, err := os.ReadFile("../../editor/tree-sitter-sigil/grammar.js")
	if err != nil {
		t.Fatalf("read grammar.js: %v", err)
	}
	m := regexp.MustCompile(`const KEYWORDS = \[([^\]]*)\]`).FindSubmatch(src)
	if m == nil {
		t.Fatal("KEYWORDS array not found in grammar.js")
	}
	var grammar []string
	for _, q := range strings.Split(string(m[1]), ",") {
		grammar = append(grammar, strings.Trim(strings.TrimSpace(q), `"`))
	}
	sort.Strings(grammar)
	got := Keywords()
	if strings.Join(grammar, " ") != strings.Join(got, " ") {
		t.Errorf("grammar KEYWORDS %v != token keywords %v", grammar, got)
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/token/ -run TestGrammarKeywordsMatch -v`
Expected: PASS (both lists are the 13 keywords, sorted-equal).

- [ ] **Step 5: Add the Makefile targets**

Append to `Makefile` (and add the new targets to the `.PHONY` line):
```make
TS_DIR := editor/tree-sitter-sigil
TS_CLI := npx --yes tree-sitter-cli@0.25

tree-sitter: ## Regenerate the tree-sitter parser from grammar.js
	@cd $(TS_DIR) && $(TS_CLI) generate
	@echo "→ $(TS_DIR)/src/parser.c"

tree-sitter-test: ## Run the tree-sitter grammar corpus tests
	@cd $(TS_DIR) && $(TS_CLI) test

tree-sitter-verify: ## Parse every std/ + examples/ .sigil; fail on ERROR nodes
	@cd $(TS_DIR) && $(TS_CLI) generate >/dev/null
	@found=0; for f in $$(find std examples -name '*.sigil'); do \
		if ! $(TS_CLI) parse -q "$$f" >/dev/null 2>&1; then echo "✗ parse error: $$f"; found=1; fi; \
	done; \
	if [ $$found -eq 0 ]; then echo "✓ all .sigil files parse clean"; else exit 1; fi

nvim-install: tree-sitter ## Build the parser .so + install queries + ftdetect into nvim site
	@cd $(TS_DIR) && cc -shared -fPIC -Wall -Os -I src src/parser.c src/scanner.c -o sigil.so
	@SITE=$${XDG_DATA_HOME:-$$HOME/.local/share}/nvim/site; \
	mkdir -p $$SITE/parser $$SITE/queries/sigil $$SITE/ftdetect; \
	cp $(TS_DIR)/sigil.so $$SITE/parser/; \
	cp $(TS_DIR)/queries/*.scm $$SITE/queries/sigil/; \
	cp editor/nvim/ftdetect/sigil.lua $$SITE/ftdetect/; \
	echo "→ installed parser, queries, ftdetect under $$SITE"

vscode-ext: ## Package the VS Code extension (.vsix) — needs npm
	@cd editor/vscode-sigil && npm install --no-audit --no-fund && npx --yes @vscode/vsce package --allow-missing-repository
	@echo "→ editor/vscode-sigil/*.vsix (install: code --install-extension <file>)"
```
Note: `tree-sitter-verify` runs the CLI from the repo root (paths `std`,
`examples`), so it does not `cd $(TS_DIR)` for the parse loop — it generates in
`$(TS_DIR)` first, then parses with the same CLI using the grammar discovered via
`--paths`/cwd. If `tree-sitter parse` cannot find the grammar from the repo root,
prefix the loop with `cd $(TS_DIR) &&` and adjust the `find` paths to `../../std
../../examples`. Verify which form works during the step below and keep that one.

- [ ] **Step 6: Run the drift guards**

Run:
```bash
make tree-sitter-test
make tree-sitter-verify
go test ./... 2>&1 | tail -8
```
Expected: corpus tests pass; `✓ all .sigil files parse clean`; all Go packages
green including `internal/token`.

- [ ] **Step 7: Commit**

```bash
git add Makefile internal/token editor/tree-sitter-sigil/grammar.js
git commit -m "feat(editor): Makefile targets + Go keyword cross-check drift guard

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: VS Code extension (TextMate) + editor README + final verification

The VS Code highlighting path (TextMate grammar) plus install docs and a manual
verification pass across both editors.

**Files:**
- Create: `editor/vscode-sigil/package.json`
- Create: `editor/vscode-sigil/language-configuration.json`
- Create: `editor/vscode-sigil/syntaxes/sigil.tmLanguage.json`
- Create: `editor/vscode-sigil/.vscodeignore`
- Create: `editor/vscode-sigil/README.md`
- Create: `editor/README.md`

**Interfaces:**
- Consumes: nothing from Go (independent TextMate grammar).
- Produces: a packageable VS Code extension.

- [ ] **Step 1: Create package.json (extension manifest)**

```json
{
  "name": "vscode-sigil",
  "displayName": "Sigil",
  "description": "Syntax highlighting for the Sigil language",
  "version": "0.1.0",
  "engines": { "vscode": "^1.75.0" },
  "categories": ["Programming Languages"],
  "contributes": {
    "languages": [{
      "id": "sigil",
      "aliases": ["Sigil", "sigil"],
      "extensions": [".sigil"],
      "configuration": "./language-configuration.json"
    }],
    "grammars": [{
      "language": "sigil",
      "scopeName": "source.sigil",
      "path": "./syntaxes/sigil.tmLanguage.json"
    }]
  }
}
```

- [ ] **Step 2: Create language-configuration.json**

```json
{
  "comments": { "lineComment": "//" },
  "brackets": [["{", "}"], ["[", "]"], ["(", ")"]],
  "autoClosingPairs": [
    { "open": "{", "close": "}" }, { "open": "[", "close": "]" },
    { "open": "(", "close": ")" }, { "open": "\"", "close": "\"" }
  ],
  "surroundingPairs": [["{","}"],["[","]"],["(",")"],["\"","\""]],
  "indentationRules": {
    "increaseIndentPattern": "(=|->|with|then|else)\\s*$",
    "decreaseIndentPattern": "^\\s*(else|\\|)"
  }
}
```

- [ ] **Step 3: Create syntaxes/sigil.tmLanguage.json**

A TextMate grammar mirroring the same token classes as highlights.scm. Include
patterns for: comments, strings (+`${…}` interpolation marked as embedded),
keywords, types/constructors (UIDENT), intrinsics (HOLE), numbers, operators.
```json
{
  "$schema": "https://raw.githubusercontent.com/martinring/tmlanguage/master/tmlanguage.json",
  "name": "Sigil",
  "scopeName": "source.sigil",
  "patterns": [
    { "include": "#comment" }, { "include": "#string" }, { "include": "#keyword" },
    { "include": "#type" }, { "include": "#hole" }, { "include": "#number" },
    { "include": "#operator" }
  ],
  "repository": {
    "comment": { "match": "//.*$", "name": "comment.line.double-slash.sigil" },
    "keyword": {
      "match": "\\b(let|rec|pub|import|as|type|fun|if|then|else|match|with|of)\\b",
      "name": "keyword.control.sigil"
    },
    "type": { "match": "\\b[A-Z][A-Za-z0-9_]*\\b", "name": "entity.name.type.sigil" },
    "hole": { "match": "\\b__[A-Za-z_][A-Za-z0-9_']*\\b", "name": "support.function.builtin.sigil" },
    "number": { "match": "\\b\\d+(\\.\\d+)?\\b", "name": "constant.numeric.sigil" },
    "operator": { "match": "(\\|>|\\|\\||&&|==|!=|<=|>=|<|>|\\+\\+|->|[+\\-*/=])", "name": "keyword.operator.sigil" },
    "string": {
      "begin": "\"", "end": "\"", "name": "string.quoted.double.sigil",
      "patterns": [
        { "begin": "\\$\\{", "end": "\\}", "name": "meta.embedded.sigil",
          "patterns": [{ "include": "$self" }] }
      ]
    }
  }
}
```

- [ ] **Step 4: Create .vscodeignore and the extension README**

`.vscodeignore`:
```
.vscode/**
node_modules/**
*.vsix
```
`editor/vscode-sigil/README.md`:
```markdown
# Sigil for VS Code

Syntax highlighting for `.sigil` files. Build the `.vsix` with `make vscode-ext`
from the repo root, then `code --install-extension editor/vscode-sigil/*.vsix`.
```

- [ ] **Step 5: Validate the TextMate grammar is well-formed JSON and packages**

Run:
```bash
node -e "JSON.parse(require('fs').readFileSync('editor/vscode-sigil/syntaxes/sigil.tmLanguage.json'))" && echo "tmLanguage JSON ok"
node -e "JSON.parse(require('fs').readFileSync('editor/vscode-sigil/language-configuration.json'))" && echo "lang-config JSON ok"
make vscode-ext
```
Expected: both `ok` lines; `vscode-ext` produces `editor/vscode-sigil/vscode-sigil-0.1.0.vsix`.

- [ ] **Step 6: Write the top-level editor/README.md**

```markdown
# Editor support for Sigil

## Neovim (tree-sitter)
1. `make nvim-install` — builds the parser and copies the parser, queries, and
   ftdetect into your nvim site directory.
2. Ensure tree-sitter highlighting is on (`vim.treesitter.start()` for filetype
   `sigil`, or your nvim-treesitter config). Open any `.sigil` file.

## VS Code (TextMate)
1. `make vscode-ext` — packages `editor/vscode-sigil/*.vsix`.
2. `code --install-extension editor/vscode-sigil/<file>.vsix`.

## Maintenance
- `make tree-sitter-test` — grammar corpus tests.
- `make tree-sitter-verify` — parse all std/ + examples/; fail on ERROR nodes.
- `go test ./internal/token/` — keyword cross-check (grammar vs language).
The tree-sitter grammar (nvim) and TextMate grammar (VS Code) are separate; keep
them aligned — the drift guards catch grammar/language divergence.
```

- [ ] **Step 7: Manual verification across both editors**

Perform and record results in the commit body:
- nvim: `make nvim-install`, open `examples/counter/counter.sigil`, confirm
  keywords, `uident` types/constructors, strings + `${}` interpolation, `__hole`
  intrinsics, `//` comments highlight, and indented blocks fold.
- VS Code: install the `.vsix`, open the same file, confirm the same token classes
  highlight via TextMate.
If a class is mis-highlighted, fix the relevant query (nvim) or tmLanguage pattern
(VS Code) and re-verify.

- [ ] **Step 8: Final full verification + commit**

Run:
```bash
make tree-sitter-test && make tree-sitter-verify && go test ./... 2>&1 | tail -8
```
Expected: all green.
```bash
git add editor/vscode-sigil editor/README.md
git commit -m "feat(editor): VS Code TextMate grammar + editor README + manual verification

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- Structure (`editor/` layout) → Tasks 1–6 create every file in the spec tree. ✓
- Tree-sitter grammar + external scanner (offside) → Tasks 1–3. ✓
- nvim queries (highlights/folds/injections/locals) + ftdetect → Task 4. ✓
- VS Code TextMate grammar + manifest + lang-config → Task 6. ✓
- Drift guard 1 (`tree-sitter test`) → Tasks 1–3 corpus + Task 5 `tree-sitter-test`. ✓
- Drift guard 2 (`tree-sitter-verify`) → Task 5. ✓
- Drift guard 3 (Go keyword cross-check) → Task 5. ✓
- Drift guard 4 (manual) → Task 6 Step 7. ✓
- Committed generated files / gitignore build outputs → Task 1 (.gitignore + generate). ✓
- Makefile targets (tree-sitter, -test, -verify, nvim-install, vscode-ext) → Task 5. ✓
- editor/README both install paths → Task 6. ✓
- Out-of-scope items (LSP, marketplace publish) → not present in any task. ✓

**Placeholder scan:** No TBD/TODO. Every code step shows complete content. The two
reference-derived artifacts (scanner.c, tmLanguage.json) ship complete code/structure
grounded in docs/grammar.md, with the git-history versions named as boilerplate
sources — not placeholders. The two `tree-sitter parse`/`query` invocation caveats
(Task 4 Step 6, Task 5 Step 5) instruct verifying which CLI form works and keeping
it — a real environment check, not a deferral.

**Type/name consistency:** Node names (`let_decl`, `import_decl`, `type_decl`,
`constructor`, `lambda`, `if`, `application`, `binop`, `unop`, `match`, `match_arm`,
`pattern`, `field`, `string`, `interpolation`, `hole`, `uident`, `identifier`,
`parameter`, `block`, `wildcard`) are introduced in Tasks 1–3 and referenced
consistently by the Task 4 queries. `token.Keywords()` defined in Task 5 Step 1,
used in Task 5 Step 3. The `KEYWORDS` const (Task 5 Step 2) matches the regex in
the Task 5 Step 3 test. The 13 keywords are identical across Global Constraints,
grammar rules, `KEYWORDS`, and the highlights query.
