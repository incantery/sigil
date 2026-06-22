# `sigil lsp` Completion (#4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `textDocument/completion` — prefix-filtered identifier completion offering in-scope locals, top-level declarations, selectively-imported names, and keywords, each with a completion-item kind.

**Architecture:** A parse-only candidate assembler in `internal/analysis/completion.go` (no `load`, no type-check — imported names come from the import statements' `Names`), reusing the 3a/3b binder helpers for locals. A thin `internal/lsp` handler serves `textDocument/completion` off the buffer text. The editor filters by the typed prefix.

**Tech Stack:** Go stdlib only. Reuses `internal/parse`, `internal/ast`, `internal/token` (`Keywords()`), the `internal/analysis` helpers (`paramBinders`/`patBinders`/`children`/`enc`), and the `internal/lsp` doc store + pipe harness. No new dependencies.

## Global Constraints

- No new Go module dependencies. Completion is **parse-only**: `parse.Module(text)` only — no `load`, no type checker. It survives type errors (needs only a successful parse).
- On parse failure → return **keywords only** (best-effort, never empty/error).
- Imported names come from `ast.Import.Names` directly (selective imports). Bare imports (empty `Names`) contribute nothing in v1. Imported-name kind is a case heuristic: uppercase-initial → `CompType`, else `CompFunction`.
- Locals are **function-scoped**: the binders of the top-level `LetDecl` with the greatest `Pos ≤ cursor` (params + every `let`/lambda/match binder in its body). Not position-precise.
- Candidates are deduped by label, first occurrence wins; emission order is locals → top-level → imports → keywords (so a local shadows a same-named top-level). The editor re-sorts/filters; reply `isIncomplete: false`.
- `CompletionKind` (analysis) → LSP `CompletionItemKind` (lsp layer): `CompFunction`→3, `CompVariable`→6, `CompType`→7 (Class), `CompConstructor`→20 (EnumMember), `CompKeyword`→14.
- Positions are 1-based in `ast`/`analysis`; the lsp layer converts 0-based LSP → 1-based on the way in.
- `token.Keywords()` already exists (exported, sorted) — no `internal/token` change.

## File structure

- `internal/analysis/completion.go` — `CompletionKind`, `Candidate`, `Completions`, `enclosingLocals`, `keywordCandidates` (Task 1).
- `internal/lsp/protocol.go` + `server.go` — capability + handler (Task 2).
- `editor/lsp.md` + `CLAUDE.md` — docs (Task 3).

---

### Task 1: `internal/analysis` — candidate assembly

**Files:**
- Create: `internal/analysis/completion.go`
- Test: `internal/analysis/completion_test.go` (create)

**Interfaces:**
- Consumes: `parse.Module`, `ast` nodes, `token.Keywords()`, and the package-internal `paramBinders`/`patBinders` (3b), `children` (3a), `enc` (3a), and the `binder` struct (`{name string; pos ast.Pos}`).
- Produces:
  - `type CompletionKind int` with `CompFunction=0`, `CompVariable=1`, `CompType=2`, `CompConstructor=3`, `CompKeyword=4`.
  - `type Candidate struct { Label string; Kind CompletionKind }`.
  - `func Completions(text string, line, col int) []Candidate`.

- [ ] **Step 1: Write the failing test**

Create `internal/analysis/completion_test.go`:

```go
package analysis

import (
	"testing"
)

func compMap(cs []Candidate) map[string]CompletionKind {
	m := map[string]CompletionKind{}
	for _, c := range cs {
		m[c.Label] = c.Kind
	}
	return m
}

func TestCompletions(t *testing.T) {
	src := "import \"std/ui\" (card, button)\n" +
		"pub let app = 1\n" +
		"type Color = Red | Green\n" +
		"let inc n =\n" +
		"  let m = n\n" +
		"  m\n"
	// cursor at line 6 col 3 (on `m`, inside inc's body).
	got := compMap(Completions(src, 6, 3))

	want := map[string]CompletionKind{
		"n":      CompVariable,    // param (local)
		"m":      CompVariable,    // let local
		"inc":    CompFunction,    // top-level fn (has params)
		"app":    CompVariable,    // top-level value
		"Color":  CompType,        // type decl
		"Red":    CompConstructor, // variant
		"Green":  CompConstructor, // variant
		"card":   CompFunction,    // import (lowercase)
		"button": CompFunction,    // import (lowercase)
		"match":  CompKeyword,     // keyword
		"let":    CompKeyword,     // keyword
	}
	for label, kind := range want {
		if got[label] != kind {
			t.Errorf("candidate %q = kind %d, want %d (present=%v)", label, got[label], kind, hasKey(got, label))
		}
	}
}

func hasKey(m map[string]CompletionKind, k string) bool { _, ok := m[k]; return ok }

func TestCompletionsParseErrorKeywordsOnly(t *testing.T) {
	got := Completions("let x = (", 1, 9)
	if len(got) == 0 {
		t.Fatal("expected keyword candidates on parse error, got none")
	}
	for _, c := range got {
		if c.Kind != CompKeyword {
			t.Errorf("parse-error completion returned non-keyword %q (kind %d)", c.Label, c.Kind)
		}
	}
	if !hasKey(compMap(got), "let") {
		t.Error("expected `let` among keyword candidates")
	}
}

func TestCompletionsLocalsAreFunctionScoped(t *testing.T) {
	// Cursor in g's body must not surface f's parameter `a`.
	got := compMap(Completions("let f a = a\nlet g b = b\n", 2, 11))
	if !hasKey(got, "b") {
		t.Error("expected g's param `b` to be offered")
	}
	if hasKey(got, "a") {
		t.Error("f's param `a` should NOT be offered while editing g")
	}
	// top-level functions are still offered regardless of cursor.
	if got["f"] != CompFunction || got["g"] != CompFunction {
		t.Errorf("expected f and g as top-level functions; got f=%d g=%d", got["f"], got["g"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/analysis/ -run TestCompletions -v`
Expected: FAIL — `Completions` / `CompletionKind` undefined.

- [ ] **Step 3: Implement `internal/analysis/completion.go`**

```go
package analysis

import (
	"github.com/incantery/sigil/internal/ast"
	"github.com/incantery/sigil/internal/parse"
	"github.com/incantery/sigil/internal/token"
)

// CompletionKind is the role of a completion candidate (mapped to an LSP
// CompletionItemKind in the lsp layer).
type CompletionKind int

const (
	CompFunction    CompletionKind = iota // 0
	CompVariable                          // 1
	CompType                              // 2
	CompConstructor                       // 3
	CompKeyword                           // 4
)

// Candidate is one completion suggestion.
type Candidate struct {
	Label string
	Kind  CompletionKind
}

// Completions returns prefix-unfiltered identifier candidates for the cursor at
// (line, col) (1-based). Parse-only: on a parse error it returns just keywords.
// Order is locals → top-level → imports → keywords; deduped by label (first
// wins, so a local shadows a same-named top-level). The editor filters by prefix.
func Completions(text string, line, col int) []Candidate {
	m, err := parse.Module(text)
	if err != nil {
		return keywordCandidates()
	}
	var cands []Candidate
	seen := map[string]bool{}
	add := func(label string, kind CompletionKind) {
		if label == "" || seen[label] {
			return
		}
		seen[label] = true
		cands = append(cands, Candidate{Label: label, Kind: kind})
	}

	// locals first (so they win dedup over same-named top-level/import)
	for _, c := range enclosingLocals(m, ast.Pos{Line: line, Col: col}) {
		add(c.Label, c.Kind)
	}
	// top-level declarations
	for _, d := range m.Decls {
		switch d := d.(type) {
		case *ast.LetDecl:
			if d.Name != "" {
				if len(d.Params) > 0 {
					add(d.Name, CompFunction)
				} else {
					add(d.Name, CompVariable)
				}
			}
		case *ast.TypeDecl:
			add(d.Name, CompType)
			for _, v := range d.Variants {
				add(v.Name, CompConstructor)
			}
		}
	}
	// selectively-imported names (from the import statements' Names)
	for _, imp := range m.Imports {
		for _, n := range imp.Names {
			if n != "" && n[0] >= 'A' && n[0] <= 'Z' {
				add(n, CompType)
			} else {
				add(n, CompFunction)
			}
		}
	}
	// keywords
	for _, k := range token.Keywords() {
		add(k, CompKeyword)
	}
	return cands
}

func keywordCandidates() []Candidate {
	ks := token.Keywords()
	out := make([]Candidate, 0, len(ks))
	for _, k := range ks {
		out = append(out, Candidate{Label: k, Kind: CompKeyword})
	}
	return out
}

// enclosingLocals returns the binders in scope within the top-level LetDecl whose
// body the cursor is in (the decl with the greatest Pos <= cursor): its
// parameters plus every let/lambda/match binder in its body.
func enclosingLocals(m *ast.Module, cursor ast.Pos) []Candidate {
	var encl *ast.LetDecl
	for _, d := range m.Decls {
		ld, ok := d.(*ast.LetDecl)
		if !ok {
			continue
		}
		if enc(ld.Pos) <= enc(cursor) && (encl == nil || enc(ld.Pos) > enc(encl.Pos)) {
			encl = ld
		}
	}
	if encl == nil {
		return nil
	}
	var out []Candidate
	emit := func(name string, kind CompletionKind) {
		if name != "" {
			out = append(out, Candidate{Label: name, Kind: kind})
		}
	}
	for _, b := range paramBinders(encl.Params) {
		emit(b.name, CompVariable)
	}
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		switch e := e.(type) {
		case *ast.Lambda:
			for _, b := range paramBinders(e.Params) {
				emit(b.name, CompVariable)
			}
			walk(e.Body)
		case *ast.Let:
			if e.Name != "" {
				if len(e.Params) > 0 {
					emit(e.Name, CompFunction)
				} else {
					emit(e.Name, CompVariable)
				}
			} else {
				for _, b := range patBinders(e.Pat) {
					emit(b.name, CompVariable)
				}
			}
			for _, b := range paramBinders(e.Params) {
				emit(b.name, CompVariable)
			}
			walk(e.Body)
			walk(e.In)
		case *ast.Match:
			walk(e.Scrut)
			for _, arm := range e.Arms {
				for _, b := range patBinders(arm.Pat) {
					emit(b.name, CompVariable)
				}
				if arm.Guard != nil {
					walk(arm.Guard)
				}
				walk(arm.Body)
			}
		default:
			for _, ch := range children(e) {
				walk(ch)
			}
		}
	}
	if encl.Body != nil {
		walk(encl.Body)
	}
	return out
}
```

Note: `paramBinders`, `patBinders`, `children`, `enc`, and the `binder` struct (with lowercase `name`/`pos` fields) are all package-internal to `internal/analysis` (from slices 3a/3b) — reuse them, do not redefine.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/analysis/`
Expected: PASS (the three completion tests + all prior analysis tests).

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/completion.go internal/analysis/completion_test.go
git commit -m "feat(analysis): parse-only completion candidate assembly"
```

---

### Task 2: `internal/lsp` — completion handler + capability + integration test

**Files:**
- Modify: `internal/lsp/protocol.go` (capability + params/items/list), `internal/lsp/server.go` (capability value + dispatch + handler)
- Test: `internal/lsp/completion_test.go` (create)

**Interfaces:**
- Consumes: `analysis.Completions` + `analysis.CompletionKind` consts (Task 1), the doc store, the pipe harness.
- Produces:
  - `ServerCapabilities.CompletionProvider *CompletionOptions`.
  - `CompletionOptions`, `CompletionParams{TextDocument, Position}`, `CompletionItem{Label, Kind}`, `CompletionList{IsIncomplete, Items}`.
  - `textDocument/completion` dispatch case + `handleCompletion`.

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/completion_test.go`:

```go
package lsp

import (
	"io"
	"path/filepath"
	"testing"
)

func TestCompletion(t *testing.T) {
	root := t.TempDir()
	src := "import \"std/ui\" (card)\nlet inc n = n\n"
	writeFile(t, filepath.Join(root, "app.sigil"), src)
	uri := "file://" + filepath.Join(root, "app.sigil")

	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()

	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file://`+root+`"}}`)
	send(cw, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","version":1,"text":"import \"std/ui\" (card)\nlet inc n = n\n"}}}`)
	// completion in inc's body (line 2, 0-based line 1, char 12 = on `n`).
	send(cw, `{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":1,"character":12}}}`)
	// The reply lists the local `n`, the top-level fn `inc`, the import `card`,
	// and a keyword. Assert a few labels appear in the items.
	waitFor(t, &out, `"label":"inc"`)
	waitFor(t, &out, `"label":"card"`)
	waitFor(t, &out, `"label":"match"`)
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}

func TestCompletionCapabilityAdvertised(t *testing.T) {
	cr, cw := io.Pipe()
	var out safeBuffer
	srv := NewServer(cr, &out)
	go srv.Run()
	send(cw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///tmp"}}`)
	waitFor(t, &out, "completionProvider")
	send(cw, `{"jsonrpc":"2.0","method":"exit"}`)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lsp/ -run TestCompletion -v`
Expected: FAIL — no handler / capability.

- [ ] **Step 3: Add protocol structs + capability in `internal/lsp/protocol.go`**

```go
type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

type CompletionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type CompletionItem struct {
	Label string `json:"label"`
	Kind  int    `json:"kind"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}
```

Add the field to `ServerCapabilities`:

```go
	CompletionProvider *CompletionOptions `json:"completionProvider,omitempty"`
```

- [ ] **Step 4: Advertise + handle in `internal/lsp/server.go`**

In the `initialize` reply capabilities, add the provider (empty options — no trigger chars in v1):

```go
			CompletionProvider: &CompletionOptions{},
```

Add a dispatch case (before `default`):

```go
	case "textDocument/completion":
		s.handleCompletion(msg)
```

Add the handler (alongside the others — `analysis` is already imported by server.go):

```go
// completionItemKind maps an analysis CompletionKind to an LSP CompletionItemKind.
func completionItemKind(k analysis.CompletionKind) int {
	switch k {
	case analysis.CompFunction:
		return 3 // Function
	case analysis.CompType:
		return 7 // Class
	case analysis.CompConstructor:
		return 20 // EnumMember
	case analysis.CompKeyword:
		return 14 // Keyword
	default:
		return 6 // Variable
	}
}

func (s *Server) handleCompletion(msg *Message) {
	var p CompletionParams
	_ = json.Unmarshal(msg.Params, &p)
	text, _ := s.docs.get(p.TextDocument.URI)
	cands := analysis.Completions(text, p.Position.Line+1, p.Position.Character+1)
	items := make([]CompletionItem, 0, len(cands))
	for _, c := range cands {
		items = append(items, CompletionItem{Label: c.Label, Kind: completionItemKind(c.Kind)})
	}
	_ = s.conn.Reply(msg.ID, CompletionList{IsIncomplete: false, Items: items})
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/lsp/`
Expected: PASS (completion + capability + all prior lsp tests).

- [ ] **Step 6: Commit**

```bash
git add internal/lsp/protocol.go internal/lsp/server.go internal/lsp/completion_test.go
git commit -m "feat(lsp): textDocument/completion"
```

---

### Task 3: Docs

**Files:**
- Modify: `editor/lsp.md`, `CLAUDE.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: Update `editor/lsp.md`**

Add a completion bullet to "What it provides (v1)":

```markdown
- **Completion** — prefix completion offering in-scope locals/parameters,
  top-level declarations (functions, values, types, constructors),
  selectively-imported names, and keywords, each with an item kind. Parse-only,
  so it works mid-edit as long as the buffer parses (a partial identifier does).
```

In "Not yet", remove "completion" if listed; add the v1 limitations: member/after-`.`
completion, bare-import name expansion, and completion detail (type signatures).

- [ ] **Step 2: Update `CLAUDE.md`**

In "What's next", mark **editor support #4 (completion) — DONE**, and note the
**editor roadmap is now complete** (#1 highlighting, #2 LSP foundation, #3
type-aware hover/go-to-def/semantic-tokens, #4 completion). Completion is
parse-only candidate assembly in `internal/analysis/completion.go`
(locals/top-level/imports/keywords) + a `textDocument/completion` handler.
Note the deferred follow-ups (member completion, bare-import expansion,
completion detail). Read CLAUDE.md first; make targeted edits matching the
surrounding prose; don't restructure.

- [ ] **Step 3: Full-repo validation**

Run: `go build ./... && go test ./...`
Expected: PASS (browser tests run or skip).

- [ ] **Step 4: Commit**

```bash
git add editor/lsp.md CLAUDE.md
git commit -m "docs: sigil lsp completion (#4) — editor roadmap complete"
```

---

## Self-Review

**Spec coverage:**
- §1 candidate assembly (CompletionKind, Candidate, Completions, parse-only, keyword fallback, four sources, dedup order, enclosing-decl locals, import case heuristic) → Task 1. ✓
- §2 lsp (CompletionProvider capability, params/item/list, handler off buffer, kind mapping) → Task 2. ✓
- §Edge cases (parse error → keywords; no enclosing decl → no locals; dedup; bare import → none; empty doc → keywords) → Task 1 (parse-error test + the `add`/dedup logic + bare-import empty Names contributing nothing). ✓
- §Testing (analysis fixture for all kinds; parse-error keywords-only; locals function-scoped; lsp integration + capability) → Tasks 1, 2. ✓
- §Docs → Task 3. ✓

**Placeholder scan:** No TBD/TODO. Every code step is complete; every run step has a command + expected result.

**Type consistency:** `analysis.CompletionKind` (CompFunction=0..CompKeyword=4) + `Candidate{Label, Kind}` + `Completions(text, line, col) []Candidate` (Task 1) consumed by `handleCompletion`/`completionItemKind` (Task 2). The lsp `CompletionItem.Kind` is the mapped LSP int (3/6/7/20/14). `paramBinders`/`patBinders`/`children`/`enc`/`binder.name` reused from prior analysis slices (same package). `CompletionList{IsIncomplete, Items}` replied. 0-based→1-based conversion (`+1`) isolated to the handler. ✓
```
