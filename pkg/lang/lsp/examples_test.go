package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exampleSources loads every .sigil file under examples/ at the repo
// root — the corpus that keeps editor tooling honest against the real
// language surface.
func exampleSources(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	out := map[string]string{}
	err := filepath.WalkDir(filepath.Join(root, "examples"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sigil") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[path] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk examples: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no example .sigil files found")
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test dir")
		}
		dir = parent
	}
}
