package load

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot is the module root that holds std/ (two levels up from core/load).
const repoRoot = "../.."

// buildAgainstStd writes entrySrc to a temp file and loads it with the real std/
// directory as the resolution root, returning the bundle and entry id.
func buildAgainstStd(t *testing.T, entrySrc string) (string, string) {
	t.Helper()
	entry := filepath.Join(t.TempDir(), "main.sigil")
	if err := os.WriteFile(entry, []byte(entrySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := Load(entry, Options{Root: repoRoot})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	js, err := prog.Bundle()
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	return js, prog.Entry.ID
}

// TestStdReactiveCell proves the first Sigil-authored stdlib module compiles and
// runs through the loader: a (read, write) cell pair round-trips a write.
func TestStdReactiveCell(t *testing.T) {
	src := `import "std/reactive" (cell)
pub let result =
  let (count, setCount) = cell 10
  let before = count ()
  let u = setCount 42
  let after = count ()
  (before, after)`
	js, id := buildAgainstStd(t, src)
	got := evalResult(t, js, id)
	want := []any{int64(10), int64(42)}
	if !eqSlice(got, want) {
		t.Fatalf("result = %v, want %v", got, want)
	}
}

// TestStdReactiveComputed proves a derived signal recomputes after a dependency
// write — fine-grained reactivity, entirely in Sigil library code.
func TestStdReactiveComputed(t *testing.T) {
	src := `import "std/reactive" (cell, computed)
pub let result =
  let (count, setCount) = cell 10
  let doubled = computed (fun () -> count () * 2)
  let before = doubled ()
  let u = setCount 50
  let after = doubled ()
  (before, after)`
	js, id := buildAgainstStd(t, src)
	got := evalResult(t, js, id)
	want := []any{int64(20), int64(100)}
	if !eqSlice(got, want) {
		t.Fatalf("result = %v, want %v", got, want)
	}
}

func eqSlice(got any, want []any) bool {
	gs, ok := got.([]any)
	if !ok || len(gs) != len(want) {
		return false
	}
	for i := range want {
		if gs[i] != want[i] {
			return false
		}
	}
	return true
}
