package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTestCmdRunsAndFailsOnFailure(t *testing.T) {
	dir := t.TempDir()
	src := `import "std/test" (eq)
test "wrong" { expect (eq 1 2) }`
	if err := os.WriteFile(filepath.Join(dir, "x_test.sigil"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	// repoRoot for std/ resolution is two levels up from internal/cli.
	root.SetArgs([]string{"test", dir, "--root", "../.."})
	err := root.Execute()
	if err != ErrSilent {
		t.Fatalf("expected ErrSilent (nonzero exit) on failing tests, got %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("wrong")) {
		t.Errorf("output should name the failing test:\n%s", out.String())
	}
}
