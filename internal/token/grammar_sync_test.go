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
