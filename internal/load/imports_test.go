package load

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModuleImportsExposesDeps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.sigil"), []byte("pub let answer = 42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte("import \"lib\" (answer)\nlet main = answer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := Load(entry, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	imps := prog.Entry.Imports()
	if len(imps) != 1 {
		t.Fatalf("got %d imports, want 1", len(imps))
	}
	if imps[0].Dep == nil || len(imps[0].Names) != 1 || imps[0].Names[0] != "answer" {
		t.Errorf("import = %+v, want Dep set + Names [answer]", imps[0])
	}
	if imps[0].Dep.File == "" {
		t.Error("dep File should be set for cross-file resolution")
	}
}
