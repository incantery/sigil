package load

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// repoRoot is the module root that holds std/ (two levels up from internal/load).
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

func TestStdTestMatchersRun(t *testing.T) {
	entry := `import "std/test" (eq, gt, isTrue, isFalse)
test "matchers" {
  expect (eq (1 + 2) 3);
  expect (gt 5 3);
  expect (isTrue true);
  expect (isFalse false);
  expect (eq [1, 2] [1, 2])
}
test "fails" {
  expect (eq 1 2)
}`
	dir := t.TempDir()
	file := filepath.Join(dir, "matchers_test.sigil")
	if err := os.WriteFile(file, []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := Load(file, Options{Root: repoRoot})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	js, err := prog.BundleTest()
	if err != nil {
		t.Fatalf("bundle-test: %v", err)
	}
	vm := goja.New()
	v, err := vm.RunString(js + "\n;JSON.stringify(__runTests())")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := v.Export().(string)
	// "matchers" test: all five expects pass. "fails" test: the single expect
	// reports expected 2, got 1.
	if !strings.Contains(out, `"name":"matchers"`) {
		t.Errorf("missing matchers test in %s", out)
	}
	if !strings.Contains(out, `"got":"1"`) || !strings.Contains(out, `"expected":"2"`) {
		t.Errorf("failing eq should report got 1 / expected 2: %s", out)
	}
}
