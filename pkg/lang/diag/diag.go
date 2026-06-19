// Package diag carries structured compile diagnostics across stages.
//
// Parser, lower, and (later) typechecker all return *Diagnostic values that
// satisfy the error interface — so existing string-printing callers keep
// working — while richer callers (the `sigil check` JSON output, an LSP
// later) can pull file/line/col/stage/suggestion out structurally.
package diag

import "fmt"

// Diagnostic is one compile-time finding. Everything except Message is
// optional, but Line + Col + Stage are filled in in practice.
type Diagnostic struct {
	File       string `json:"file,omitempty"`
	Stage      string `json:"stage,omitempty"` // "parse", "lower", "check"
	Line       int    `json:"line,omitempty"`
	Col        int    `json:"col,omitempty"`
	Severity   string `json:"severity,omitempty"` // empty == "error"
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

func (d *Diagnostic) Error() string {
	prefix := ""
	if d.File != "" {
		prefix = d.File + ":"
	}
	out := fmt.Sprintf("%s%d:%d: %s", prefix, d.Line, d.Col, d.Message)
	if d.Suggestion != "" {
		out += " (" + d.Suggestion + ")"
	}
	return out
}

// New is the constructor every parser/lower error site uses.
func New(stage string, line, col int, msg string) *Diagnostic {
	return &Diagnostic{Stage: stage, Line: line, Col: col, Message: msg}
}

// Diagnostics collects multiple findings during a single parse + lower run.
//
// The parser and lower each take one and append into it on every error
// they hit, instead of bailing out at the first one. Callers get the full
// set via All(); a non-empty collector returns a *MultiError from Err()
// so existing error-handling code (errors.As, .Error()) still works.
type Diagnostics struct {
	items []*Diagnostic
}

// Add appends a diagnostic if non-nil.
func (d *Diagnostics) Add(item *Diagnostic) {
	if item != nil {
		d.items = append(d.items, item)
	}
}

// AddErr accepts an arbitrary error and converts it into a Diagnostic if
// possible (via errors.As); otherwise wraps the message in a synthetic
// stage="unknown" diagnostic at line 0.
func (d *Diagnostics) AddErr(err error) {
	if err == nil {
		return
	}
	var found *Diagnostic
	if asDiag(err, &found) {
		d.items = append(d.items, found)
		return
	}
	d.items = append(d.items, &Diagnostic{Stage: "unknown", Message: err.Error()})
}

// All returns the collected diagnostics in insertion order.
func (d *Diagnostics) All() []*Diagnostic { return d.items }

// Len returns the number of diagnostics so far.
func (d *Diagnostics) Len() int { return len(d.items) }

// Empty reports whether no diagnostics have been collected.
func (d *Diagnostics) Empty() bool { return len(d.items) == 0 }

// Err returns nil when empty, the single Diagnostic when there's one (so
// the existing `errors.As(err, &d)` pattern still works on the common
// case), or a *MultiError otherwise.
func (d *Diagnostics) Err() error {
	switch len(d.items) {
	case 0:
		return nil
	case 1:
		return d.items[0]
	default:
		return &MultiError{Items: d.items}
	}
}

// MultiError wraps multiple Diagnostics for callers that don't unpack
// them. Its Error() concatenates each with newlines so plain printing
// shows everything.
type MultiError struct {
	Items []*Diagnostic
}

func (m *MultiError) Error() string {
	parts := make([]string, len(m.Items))
	for i, d := range m.Items {
		parts[i] = d.Error()
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n"
		}
		out += p
	}
	return out
}

// asDiag is a local helper to avoid pulling errors.As into this package's
// API surface; we just walk the error chain looking for *Diagnostic.
func asDiag(err error, out **Diagnostic) bool {
	for cur := err; cur != nil; {
		if d, ok := cur.(*Diagnostic); ok {
			*out = d
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := cur.(unwrapper); ok {
			cur = u.Unwrap()
			continue
		}
		break
	}
	return false
}
