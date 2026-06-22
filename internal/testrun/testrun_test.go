package testrun

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot holds std/ (two levels up from internal/testrun).
const repoRoot = "../.."

func writeTests(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRunReportsPassAndFail(t *testing.T) {
	dir := writeTests(t, map[string]string{
		"math_test.sigil": `import "std/test" (eq)
test "adds" { expect (eq (1 + 2) 3) }
test "wrong" { expect (eq (1 + 1) 3) }`,
	})
	var buf bytes.Buffer
	ok, err := Run(&buf, dir, repoRoot)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := buf.String()
	if ok {
		t.Errorf("expected ok=false (one test fails)\n%s", out)
	}
	if !strings.Contains(out, "adds") || !strings.Contains(out, "wrong") {
		t.Errorf("missing test names:\n%s", out)
	}
	if !strings.Contains(out, "expected 3, got 2") {
		t.Errorf("failing test should print expected/got:\n%s", out)
	}
}

func TestRunBrowserGuard(t *testing.T) {
	dir := writeTests(t, map[string]string{
		"dom_test.sigil": `import "std/test" (eq)
test "needs dom" {
  let n = __text (fun _ -> "hi");
  expect (eq 1 1)
}`,
	})
	var buf bytes.Buffer
	ok, err := Run(&buf, dir, repoRoot)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := buf.String()
	if ok {
		t.Errorf("browser test should not pass under goja:\n%s", out)
	}
	if !strings.Contains(out, "browser test") {
		t.Errorf("expected a browser-test hint:\n%s", out)
	}
}
