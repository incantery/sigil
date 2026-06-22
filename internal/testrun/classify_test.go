package testrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/incantery/sigil/internal/load"
)

// NOTE: `repoRoot` is already declared in internal/testrun/testrun_test.go
// (from Slice A). Do NOT redeclare it here — reuse the existing const.

func loadProg(t *testing.T, src string) *load.Program {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "x_test.sigil")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := load.Load(f, load.Options{Root: repoRoot})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return prog
}

func TestClassifyBrowserVsPure(t *testing.T) {
	browserSrc := `import "std/browser" (navigate, domText)
import "std/test" (eq)
test "b" { navigate "http://x"; expect (eq (domText "#h") "hi") }`
	if !isBrowserProgram(loadProg(t, browserSrc)) {
		t.Error("program using std/browser should classify as browser")
	}

	pureSrc := `import "std/test" (eq)
test "p" { expect (eq (1 + 1) 2) }`
	if isBrowserProgram(loadProg(t, pureSrc)) {
		t.Error("pure program should not classify as browser")
	}
}
