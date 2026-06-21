package lsp

import (
	"errors"
	"strings"

	"github.com/incantery/sigil/internal/lex"
	"github.com/incantery/sigil/internal/load"
	"github.com/incantery/sigil/internal/parse"
	"github.com/incantery/sigil/internal/types"
)

// diagnosticsFor maps a load.Load error to LSP diagnostics. A nil error yields
// an empty slice (which clears stale squiggles when published).
func diagnosticsFor(err error, text string) []Diagnostic {
	if err == nil {
		return []Diagnostic{}
	}
	line, col, msg := 1, 1, err.Error()
	positioned := false

	var le *lex.Error
	var pe *parse.Error
	var te *types.Error
	switch {
	case errors.As(err, &le):
		line, col, msg, positioned = le.Line, le.Col, le.Msg, true
	case errors.As(err, &pe):
		line, col, msg, positioned = pe.Line, pe.Col, pe.Msg, true
	case errors.As(err, &te):
		line, col, msg, positioned = te.Line, te.Col, te.Msg, true
	}

	if !positioned {
		// Import-resolution / read errors carry no position: pin to file top.
		return []Diagnostic{{
			Range:    Range{Start: Position{0, 0}, End: Position{0, lineLength(text, 0)}},
			Severity: SeverityError,
			Source:   "sigil",
			Message:  msg,
		}}
	}

	startLine := line - 1
	startChar := col - 1
	endChar := startChar
	if l := lineLength(text, startLine); l > startChar {
		endChar = l // extend to end of line so the squiggle is visible
	}
	return []Diagnostic{{
		Range:    Range{Start: Position{startLine, startChar}, End: Position{startLine, endChar}},
		Severity: SeverityError,
		Source:   "sigil",
		Message:  msg,
	}}
}

// lineLength returns the character length of the 0-based line in text, or 0 if
// the line is out of range.
func lineLength(text string, line int) int {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return 0
	}
	return len(strings.TrimRight(lines[line], "\r"))
}

// analyze loads the document at path with the given root and overlay, returning
// the first compiler error (or nil).
func analyze(path, root string, overlay map[string]string) error {
	_, err := load.Load(path, load.Options{Root: root, Overlay: overlay})
	return err
}
