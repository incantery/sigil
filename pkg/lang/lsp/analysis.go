package lsp

import (
	"errors"
	"fmt"

	"github.com/incantery/mako/pkg/lang/ast"
	"github.com/incantery/mako/pkg/lang/diag"
	"github.com/incantery/mako/pkg/lang/loader"
	"github.com/incantery/mako/pkg/lang/lower"
	"github.com/incantery/mako/pkg/lang/parser"
)

// analysis is everything derived from one buffer revision: the
// error-tolerant AST (always present — the parser pushes __error__
// placeholders and keeps going), the structured diagnostics from parse
// + lower, and the symbol index for navigation.
type analysis struct {
	root  *ast.Node
	diags []*diag.Diagnostic
	index *symbolIndex
}

// analyze parses the buffer and, when possible, runs the same
// loader+lower pipeline `sigil check` uses — with the buffer overlaid
// over its on-disk content so unsaved edits are what gets compiled.
func analyze(doc *document) *analysis {
	an := &analysis{}

	root, perr := parser.Parse(doc.text)
	an.root = root
	seen := map[string]bool{}
	for _, d := range flattenDiags(perr) {
		if d.File == "" {
			d.File = doc.path
		}
		an.diags = append(an.diags, d)
		seen[diagKey(d)] = true
	}

	// Full compile for lower-stage diagnostics. Files inside a sigil
	// module compile through the loader (imports resolve, the whole
	// package lowers); scratch buffers outside any module fall back to
	// lowering the single file when it has no imports.
	var lerr error
	switch {
	case doc.path != "":
		lerr = compileOverlay(doc.path, doc.text)
		if lerr != nil && !hasDiag(lerr) {
			// Infrastructure error (no sigil.mod, unreadable sibling, …):
			// fall back to single-file lowering rather than surfacing a
			// position-less error on every keystroke.
			lerr = nil
			if perr == nil && !hasImports(root) {
				_, lerr = lower.Lower(root)
			}
		}
	case perr == nil && !hasImports(root):
		_, lerr = lower.Lower(root)
	}

	for _, d := range flattenDiags(lerr) {
		// Keep findings for this buffer (or unattributed ones); a broken
		// sibling file's diagnostics belong to that file's buffer.
		if d.File != "" && doc.path != "" && d.File != doc.path {
			continue
		}
		if seen[diagKey(d)] {
			continue
		}
		an.diags = append(an.diags, d)
		seen[diagKey(d)] = true
	}

	an.index = buildIndex(doc, root)
	return an
}

func compileOverlay(path, text string) error {
	prog, err := loader.LoadWithOverlay(path, map[string]string{path: text})
	if err != nil {
		return err
	}
	merged, err := prog.Merge()
	if err != nil {
		return err
	}
	_, err = lower.Lower(merged)
	return err
}

func hasImports(root *ast.Node) bool {
	if root == nil {
		return false
	}
	nodes := []*ast.Node{root}
	if root.Kind == "module" || root.Kind == "__root__" {
		nodes = root.Children
	}
	for _, c := range nodes {
		if c.Kind == "import" {
			return true
		}
	}
	return false
}

func flattenDiags(err error) []*diag.Diagnostic {
	if err == nil {
		return nil
	}
	var multi *diag.MultiError
	if errors.As(err, &multi) {
		return multi.Items
	}
	var d *diag.Diagnostic
	if errors.As(err, &d) {
		return []*diag.Diagnostic{d}
	}
	return []*diag.Diagnostic{{Stage: "unknown", Line: 1, Col: 1, Message: err.Error()}}
}

func hasDiag(err error) bool {
	var multi *diag.MultiError
	var d *diag.Diagnostic
	return errors.As(err, &multi) || errors.As(err, &d)
}

func diagKey(d *diag.Diagnostic) string {
	return fmt.Sprintf("%s:%d:%d:%s", d.Stage, d.Line, d.Col, d.Message)
}

// diagnostics converts the collected diag values into LSP shapes. The
// reported range covers the identifier-ish token at the diagnostic's
// position so editors underline a word, not a single character.
func (an *analysis) diagnostics(doc *document) []Diagnostic {
	out := make([]Diagnostic, 0, len(an.diags))
	for _, d := range an.diags {
		line, col := d.Line, d.Col
		if line < 1 {
			line, col = 1, 1
		}
		if col < 1 {
			col = 1
		}
		length := tokenLenAt(doc.lines, line, col)
		msg := d.Message
		if d.Suggestion != "" {
			msg += " (" + d.Suggestion + ")"
		}
		sev := 1 // error
		switch d.Severity {
		case "warning":
			sev = 2
		case "info":
			sev = 3
		case "hint":
			sev = 4
		}
		out = append(out, Diagnostic{
			Range:    protoRange(doc.lines, line, col, length),
			Severity: sev,
			Source:   "sigil " + stageOr(d.Stage, "check"),
			Message:  msg,
		})
	}
	return out
}

func stageOr(stage, fallback string) string {
	if stage == "" {
		return fallback
	}
	return stage
}

// tokenLenAt returns the byte length of the word starting at the
// 1-based (line, col), minimum 1.
func tokenLenAt(lines []string, line, col int) int {
	if line-1 >= len(lines) {
		return 1
	}
	src := lines[line-1]
	if col-1 >= len(src) {
		return 1
	}
	n := 0
	for _, r := range src[col-1:] {
		if !isWordRune(r) {
			break
		}
		n += len(string(r))
	}
	if n == 0 {
		return 1
	}
	return n
}

func isWordRune(r rune) bool {
	return r == '_' || r == '-' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
