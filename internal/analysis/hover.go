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
