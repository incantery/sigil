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
