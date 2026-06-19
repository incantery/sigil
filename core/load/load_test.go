package load

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// build writes each named source to a temp module root, loads+links the entry
// module, and returns the bundled JS plus the entry module's JS id.
func build(t *testing.T, files map[string]string, entry string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, name+".mako")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prog, err := Load(filepath.Join(dir, entry+".mako"), Options{Root: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	js, err := prog.Bundle()
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	return js, prog.Entry.ID
}

// evalResult runs the bundle and returns the entry module's exported `result`.
func evalResult(t *testing.T, js, entryID string) any {
	t.Helper()
	vm := goja.New()
	v, err := vm.RunString(js + "\n;__m_" + entryID + ".$result")
	if err != nil {
		t.Fatalf("JS runtime error: %v\n--- emitted ---\n%s", err, js)
	}
	return v.Export()
}

func TestValueFlow(t *testing.T) {
	js, id := build(t, map[string]string{
		"lib":  "pub let inc x = x + 1",
		"main": "import \"lib\" (inc)\npub let result = inc 41",
	}, "main")
	if got := evalResult(t, js, id); got != int64(42) {
		t.Fatalf("result = %v, want 42", got)
	}
}

// TestTypeAndCtorFlow proves a public type, its constructors, exhaustive match,
// and a value all cross the module boundary — and that the importer can build a
// constructor it never declared.
func TestTypeAndCtorFlow(t *testing.T) {
	lib := strings.Join([]string{
		"pub type Color = Red | Green",
		"pub let name c =",
		"  match c with",
		"  | Red -> \"red\"",
		"  | Green -> \"green\"",
	}, "\n")
	js, id := build(t, map[string]string{
		"lib":  lib,
		"main": "import \"lib\" (name)\npub let result = name Green",
	}, "main")
	if got := evalResult(t, js, id); got != "green" {
		t.Fatalf("result = %v, want \"green\"", got)
	}
}

// TestTransitive checks dependency-order checking and transitive type flow over
// a three-module chain a <- b <- main.
func TestTransitive(t *testing.T) {
	js, id := build(t, map[string]string{
		"a":    "pub let base = 10",
		"b":    "import \"a\" (base)\npub let mid x = base + x",
		"main": "import \"b\" (mid)\npub let result = mid 5",
	}, "main")
	if got := evalResult(t, js, id); got != int64(15) {
		t.Fatalf("result = %v, want 15", got)
	}
}

// TestScopeIsolation confirms two modules with identically named non-public
// helpers do not collide (each lives in its own IIFE scope).
func TestScopeIsolation(t *testing.T) {
	js, id := build(t, map[string]string{
		"one":  "let helper = 1\npub let a = helper",
		"two":  "let helper = 2\npub let b = helper",
		"main": "import \"one\" (a)\nimport \"two\" (b)\npub let result = a + b",
	}, "main")
	if got := evalResult(t, js, id); got != int64(3) {
		t.Fatalf("result = %v, want 3", got)
	}
}

func TestUnknownSelectiveImport(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "lib.mako"), []byte("pub let inc x = x + 1"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.mako"), []byte("import \"lib\" (nope)\npub let result = 1"), 0o644)
	_, err := Load(filepath.Join(dir, "main.mako"), Options{Root: dir})
	if err == nil || !strings.Contains(err.Error(), "not exported") {
		t.Fatalf("want 'not exported' error, got %v", err)
	}
}

// TestNonPubNotExported confirms a non-public binding is invisible to importers.
func TestNonPubNotExported(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "lib.mako"), []byte("let secret = 1"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.mako"), []byte("import \"lib\" (secret)\npub let result = secret"), 0o644)
	_, err := Load(filepath.Join(dir, "main.mako"), Options{Root: dir})
	if err == nil || !strings.Contains(err.Error(), "not exported") {
		t.Fatalf("want 'not exported' error, got %v", err)
	}
}

func TestImportCycle(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.mako"), []byte("import \"b\" (bv)\npub let av = bv"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.mako"), []byte("import \"a\" (av)\npub let bv = av"), 0o644)
	_, err := Load(filepath.Join(dir, "a.mako"), Options{Root: dir})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestUnresolvedImport(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.mako"), []byte("import \"missing\" (x)\npub let result = x"), 0o644)
	_, err := Load(filepath.Join(dir, "main.mako"), Options{Root: dir})
	if err == nil || !strings.Contains(err.Error(), "cannot resolve") {
		t.Fatalf("want resolve error, got %v", err)
	}
}

// TestPrefixStripping checks that an in-repo module path prefix maps to a nested
// local file under Root.
func TestPrefixStripping(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "std"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "std", "math.mako"), []byte("pub let two = 2"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.mako"),
		[]byte("import \"example.com/proj/std/math\" (two)\npub let result = two"), 0o644)
	prog, err := Load(filepath.Join(dir, "main.mako"), Options{Root: dir, Prefix: "example.com/proj/"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	js, err := prog.Bundle()
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if got := evalResult(t, js, prog.Entry.ID); got != int64(2) {
		t.Fatalf("result = %v, want 2", got)
	}
}
