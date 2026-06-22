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
