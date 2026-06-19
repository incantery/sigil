package vet

import (
	"strings"
	"testing"

	"github.com/incantery/mako/pkg/lang/parser"
)

func TestUnusedState(t *testing.T) {
	src := `view Demo =
  state used = 0
  state unused = "ignored"
  card
    text used
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := Run(root)
	if len(out) != 1 {
		t.Fatalf("want 1 warning, got %d: %v", len(out), out)
	}
	if !strings.Contains(out[0].Message, "unused") {
		t.Fatalf("want warning about `unused`, got %q", out[0].Message)
	}
	if out[0].Severity != "warning" {
		t.Fatalf("want severity=warning, got %q", out[0].Severity)
	}
}

func TestUnusedComponent(t *testing.T) {
	src := `component live -> x =
  text x

component dead -> y =
  text y

view App =
  state count = 0
  live count
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := Run(root)
	if len(out) != 1 {
		t.Fatalf("want 1 warning, got %d: %v", len(out), out)
	}
	if !strings.Contains(out[0].Message, "dead") {
		t.Fatalf("want warning about `dead`, got %q", out[0].Message)
	}
}

// Loop variables are not state cells — `for item in items` shouldn't
// flag `item` even though it never appears in any state decl.
func TestLoopVarNotFlagged(t *testing.T) {
	src := `view Demo =
  state items = [0, 0, 0]
  card
    for item in items
      stack horizontal gap=1
        text item
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := Run(root)
	if len(out) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(out), out)
	}
}

// Cell references inside ${interpolation} should count as uses.
func TestInterpolationCountsAsUse(t *testing.T) {
	src := `view Demo =
  state name = ""
  card
    text "hello, ${name}"
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := Run(root)
	if len(out) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(out), out)
	}
}

// Handler RHS reads should count as uses (cell = cell + 1 uses cell).
func TestHandlerReadCountsAsUse(t *testing.T) {
	src := `view Demo =
  state count = 0
  card
    button "+" on click { count = count + 1 }
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := Run(root)
	if len(out) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(out), out)
	}
}

func TestStorylessComponent(t *testing.T) {
	src := `component covered -> x =
  text x

component bare -> y =
  text y

view App =
  covered "a"
  bare "b"

story "Covered" =
  covered "hello"
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	warnings := Run(root)
	var hit bool
	for _, w := range warnings {
		if strings.Contains(w.Message, `component "bare" has no story`) {
			hit = true
		}
		if strings.Contains(w.Message, `component "covered" has no story`) {
			t.Fatalf("covered component wrongly flagged: %v", w)
		}
	}
	if !hit {
		t.Fatalf("want story-coverage hint for bare, got %v", warnings)
	}
}

func TestNoStoriesNoCoverageHints(t *testing.T) {
	src := `component solo -> x =
  text x

view App =
  solo "a"
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, w := range Run(root) {
		if strings.Contains(w.Message, "has no story") {
			t.Fatalf("coverage hint fired in a story-less file: %v", w)
		}
	}
}
