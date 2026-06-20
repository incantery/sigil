package lsp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/incantery/sigil/pkg/lang/lower"
)

// These tests are the drift guard between the Go compiler (source of
// truth) and the editor-facing tree-sitter artifacts. They fail the
// moment a new decl keyword or builtin component ships without the
// grammar/queries learning about it — the exact drift that left the
// L12 grammar 76 lessons behind.

func readEditorFile(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "editor", "tree-sitter-sigil", rel)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// declKeywords mirrors parser.declKeywords (unexported); parse.go's
// dispatch is the authority. If this list drifts from the parser the
// corpus/examples sweep fails too, so duplication here is low-risk and
// keeps the parser's internals private.
var declKeywords = []string{
	"view", "component", "state", "theme", "test", "story", "type",
	"query", "command", "stream", "app", "import", "icons", "backend",
	"session", "fonts",
}

func TestGrammarCoversDeclKeywords(t *testing.T) {
	grammar := readEditorFile(t, "grammar.js")
	for _, kw := range declKeywords {
		if !strings.Contains(grammar, "'"+kw+"'") {
			t.Errorf("grammar.js does not mention decl keyword %q — tree-sitter grammar has drifted from the parser", kw)
		}
	}
}

func TestHighlightsCoverDeclKeywords(t *testing.T) {
	hl := readEditorFile(t, filepath.Join("queries", "highlights.scm"))
	for _, kw := range declKeywords {
		if !strings.Contains(hl, `"`+kw+`"`) {
			t.Errorf("highlights.scm does not highlight decl keyword %q", kw)
		}
	}
}

// TestHighlightsCoverBuiltinKinds keeps the #any-of? builtin-component
// list in highlights.scm in lockstep with lower.BuiltinKinds().
func TestHighlightsCoverBuiltinKinds(t *testing.T) {
	hl := readEditorFile(t, filepath.Join("queries", "highlights.scm"))
	start := strings.Index(hl, "@type.builtin\n")
	if start < 0 {
		t.Fatal("highlights.scm lost its @type.builtin builtin-kind rule")
	}
	section := hl[start:]
	if end := strings.Index(section, "))"); end > 0 {
		section = section[:end]
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([a-z-]+)"`).FindAllStringSubmatch(section, -1) {
		listed[m[1]] = true
	}
	for _, k := range lower.BuiltinKinds() {
		if k == "if" || k == "for" || k == "code" {
			continue // dedicated grammar rules, not invocation kinds
		}
		if !listed[k] {
			t.Errorf("highlights.scm builtin list is missing kind %q (lower.BuiltinKinds is the source of truth)", k)
		}
	}
}
